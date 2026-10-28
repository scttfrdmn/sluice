// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

// Command estimate projects the E0 spend (tag, matrix, references, judges)
// from the price table and a run's E0 subsample, and prints a markdown
// table split by model and stage for an expected and a high scenario.
//
// Before tagging, request and context lengths are measured and the leaf
// structure is assumed (leaves per request, span cover, reference and judge
// shares). After the tag pass, those are measured from leaves.jsonl: leaves
// per request and leaf prompt tokens per stratum, and reference and judge
// shares from each leaf's labeling plan; the tag column is the measured
// spend. Once matrix and label have run on some leaves (the pilot), matrix
// output tokens per stratum and model, label cost per cell, and reference
// cost are measured too, and a pilot-measurements section is printed.
//
// Prices: batch where the price list has one (Nova matrix cells outside the
// on-demand latency subsample); on-demand everywhere else, which is the
// recorded ceiling for Claude batch cells (no Claude batch price is listed).
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/scttfrdmn/sluice/internal/config"
	"github.com/scttfrdmn/sluice/internal/corpus"
	"github.com/scttfrdmn/sluice/internal/jsonl"
	"github.com/scttfrdmn/sluice/internal/label"
	"github.com/scttfrdmn/sluice/internal/matrix"
	"github.com/scttfrdmn/sluice/internal/rundir"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "estimate:", err)
		os.Exit(1)
	}
}

// scenario holds the assumed output lengths and multipliers.
type scenario struct {
	name      string
	matrixOut float64 // mean output tokens per matrix call
	judgeOut  float64 // output tokens per judge call
	refOut    float64 // output tokens per reference call
	retry     float64 // multiplier for invalid-JSON retries
	leafMul   float64 // multiplier on assumed leaves per request (pre-tag only)
	cover     float64 // assumed share of request text in leaf spans (pre-tag only)
	judgeMin  float64 // assumed judge share floor (pre-tag only)
}

// stratum holds what is measured or assumed for one stratum.
type stratum struct {
	n             int
	text, context float64 // mean request tokens
	// Measured after tagging (tagged > 0).
	tagged, leaves         int
	leafTokSum             float64
	tests, numerics, judge int
	tagUSD                 float64
	// Measured after the matrix and label run on some of its leaves, per
	// pool model: output tokens and label cost per cell; and per reference.
	cells, labeled   map[string]int
	outTok, labelUSD map[string]float64
	refs             int
	refUSD           float64
	judged, agree    int
	judgeFailed      int
}

// modelStat is what the pilot measured for one pool model.
type modelStat struct {
	n, empty int
	out      float64
	stop     map[string]int
}

// measuredScale is the high-scenario multiplier on pilot-measured output
// tokens and label and reference costs: the full subsample can differ from
// the pilot draw.
const measuredScale = 1.5

// Pre-tag assumptions, used only for strata with no tagged requests.
var assumedLeaves = map[string]float64{corpus.SourceClaudeCode: 1.3, "repos:issue": 2.5, "repos:prompt-file": 5, corpus.SourceWildchat: 1.2, corpus.SourceChatGPT: 1.3, corpus.SourceClaudeExport: 1.3}

const (
	assumedCodeShare = 0.3
	assumedNumShare  = 0.03
)

func run() error {
	var (
		cfgPath  = flag.String("config", "config/sluice.yaml", "config file")
		runsRoot = flag.String("runs", "runs", "runs root")
		runID    = flag.String("run", "", "run id (required)")
		which    = flag.String("subsample", "calibration", "calibration (subsample.json) or test (subsample_test.json)")
	)
	exp := scenario{name: "expected", matrixOut: 800, judgeOut: 100, refOut: 1200, retry: 1.05, leafMul: 1, cover: 0.7, judgeMin: 0.7}
	flag.Float64Var(&exp.matrixOut, "matrix-out", exp.matrixOut, "expected mean output tokens per matrix call")
	flag.Float64Var(&exp.judgeOut, "judge-out", exp.judgeOut, "expected output tokens per judge verdict")
	flag.Parse()
	if *runID == "" {
		return errors.New("-run is required")
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	dir, err := rundir.Open(*runsRoot, *runID)
	if err != nil {
		return err
	}
	path := map[string]string{"calibration": dir.Subsample(), "test": dir.SubsampleTest()}[*which]
	if path == "" {
		return errors.New("-subsample must be calibration or test")
	}
	ids, ss, err := corpus.ReadSubsample(path)
	if err != nil {
		return err
	}
	if ids == nil {
		return fmt.Errorf("%s missing; run harvest subsample first", path)
	}
	recs, err := jsonl.Read[corpus.Record](dir.Records())
	if err != nil {
		return err
	}
	recs = corpus.FilterSubsample(recs, ids)
	cpt := cfg.Taxonomy.CharsPerToken
	strata := map[string]*stratum{}
	stratumOf := map[string]string{}
	for _, r := range recs {
		s := corpus.Stratum(r)
		stratumOf[r.ID] = s
		if strata[s] == nil {
			strata[s] = &stratum{cells: map[string]int{}, labeled: map[string]int{}, outTok: map[string]float64{}, labelUSD: map[string]float64{}}
		}
		st := strata[s]
		st.n++
		st.text += float64(len(r.Text)) / cpt
		st.context += float64(len(r.ContextText)) / cpt
	}
	for _, st := range strata {
		st.text /= float64(st.n)
		st.context /= float64(st.n)
	}

	// Measured leaf structure, if the tag pass has run.
	if err := jsonl.Each(dir.Tags(), func(t corpus.TagResult) error {
		if s, ok := stratumOf[t.RequestID]; ok {
			strata[s].tagged++
			strata[s].tagUSD += t.CostUSD
		}
		return nil
	}); err != nil {
		return err
	}
	if err := jsonl.Each(dir.Leaves(cfg.Taxonomy.Version), func(l corpus.Leaf) error {
		s, ok := stratumOf[l.RequestID]
		if !ok {
			return nil
		}
		st := strata[s]
		st.leaves++
		st.leafTokSum += float64(len(l.Prompt())) / cpt
		ref, judged := label.Plan(l)
		switch ref {
		case "test":
			st.tests++
		case "numeric":
			st.numerics++
		}
		if judged {
			st.judge++
		}
		return nil
	}); err != nil {
		return err
	}
	measured := false
	for _, st := range strata {
		measured = measured || st.tagged > 0
	}

	// Pilot measurements, if the matrix and label have run on some leaves.
	leafStratum := map[string]string{}
	if err := jsonl.Each(dir.Leaves(cfg.Taxonomy.Version), func(l corpus.Leaf) error {
		if s, ok := stratumOf[l.RequestID]; ok {
			leafStratum[l.LeafID] = s
		}
		return nil
	}); err != nil {
		return err
	}
	cells, err := matrix.ReadAll(dir.MatrixDir())
	if err != nil {
		return err
	}
	byModel := map[string]*modelStat{}
	for _, c := range cells {
		s, ok := leafStratum[c.LeafID]
		if !ok {
			continue
		}
		st := strata[s]
		st.cells[c.Model]++
		st.outTok[c.Model] += float64(c.TokensOut)
		ms := byModel[c.Model]
		if ms == nil {
			ms = &modelStat{stop: map[string]int{}}
			byModel[c.Model] = ms
		}
		ms.n++
		ms.out += float64(c.TokensOut)
		ms.stop[c.StopReason]++
		if strings.TrimSpace(c.Output) == "" {
			ms.empty++
		}
	}
	methods := map[string]int{}
	if err := jsonl.Each(dir.Labels(), func(l label.Label) error {
		s, ok := leafStratum[l.LeafID]
		if !ok {
			return nil
		}
		st := strata[s]
		st.labeled[l.Model]++
		st.labelUSD[l.Model] += l.CostUSD
		methods[l.Method]++
		switch {
		case l.Method == label.MethodJudgeFailed:
			st.judgeFailed++
		case l.JudgeAgreement != nil:
			st.judged++
			if *l.JudgeAgreement {
				st.agree++
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if err := jsonl.Each(dir.Refs(), func(r label.Ref) error {
		if s, ok := leafStratum[r.LeafID]; ok {
			strata[s].refs++
			strata[s].refUSD += r.CostUSD
		}
		return nil
	}); err != nil {
		return err
	}
	piloted := len(byModel) > 0

	tmpl := func(name string) (float64, error) {
		b, err := os.ReadFile(cfg.Path(name))
		return float64(len(b)) / cpt, err
	}
	var pTag, pJudge, pTest, pNum float64
	for _, x := range []struct {
		dst  *float64
		name string
	}{{&pTag, cfg.Prompts.Tag}, {&pJudge, cfg.Prompts.Judge}, {&pTest, cfg.Prompts.Test}, {&pNum, cfg.Prompts.Numeric}} {
		if *x.dst, err = tmpl(x.name); err != nil {
			return err
		}
	}
	high := exp
	high.name = "high"
	high.matrixOut, high.judgeOut, high.refOut, high.retry = exp.matrixOut*2, float64(cfg.Judges.MaxTokens), exp.refOut*2, 1.2
	high.leafMul, high.cover, high.judgeMin = 1.5, 1, 1

	names := sortedStrata(strata)
	fmt.Printf("## E0 cost projection: run `%s`, %s subsample, %d requests\n\n", *runID, ss.Split, len(recs))
	basis := "pre-tag: leaf structure assumed"
	if measured {
		basis = "post-tag: leaves per request, leaf prompt tokens, reference and judge shares, and tag spend measured from `tags.jsonl` / `leaves.jsonl`"
	}
	fmt.Printf("Prices dated %s (%s). Tokens = chars / %.1f. Basis: %s.\n\n", cfg.Prices.Date, cfg.Prices.Region, cpt, basis)
	fmt.Println("| stratum | requests | tagged | mean request tok | mean context tok | leaves/request | mean leaf prompt tok | test refs | numeric refs | judged leaves |\n|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|")
	for _, s := range names {
		st := strata[s]
		if st.tagged > 0 {
			L := float64(st.leaves) / float64(st.tagged)
			fmt.Printf("| %s | %d | %d | %.0f | %.0f | %.2f | %.0f | %.1f%% | %.1f%% | %.1f%% |\n", s, st.n, st.tagged, st.text, st.context, L,
				st.leafTokSum/max(1, float64(st.leaves)), pct(st.tests, st.leaves), pct(st.numerics, st.leaves), pct(st.judge, st.leaves))
		} else {
			fmt.Printf("| %s | %d | 0 | %.0f | %.0f | %.1f (assumed) | - | %.0f%% (assumed) | %.0f%% (assumed) | ≥%.0f%% (assumed) |\n", s, st.n, st.text, st.context,
				assumedLeaves[s], 100*assumedCodeShare, 100*assumedNumShare, 100*exp.judgeMin)
		}
	}
	fmt.Printf("\nTemplate overhead (tokens): tag %.0f, judge %.0f, test %.0f, numeric %.0f.\n\n", pTag, pJudge, pTest, pNum)

	type row struct{ tag, matrix, ref, judge float64 }
	results := map[string]map[string]*row{}
	leavesOut := map[string]float64{}
	strong, _ := cfg.Model(cfg.Strong)
	for _, sc := range []scenario{exp, high} {
		res := map[string]*row{}
		for _, m := range cfg.Models {
			res[m.Name] = &row{}
		}
		var nLeaves float64
		for _, s := range names {
			st := strata[s]
			n := float64(st.n)
			var L, leafIn, testShare, numShare, judgeShare float64
			if st.tagged > 0 {
				// Measured; untagged requests in the stratum are projected
				// at the measured rates.
				L = float64(st.leaves) / float64(st.tagged)
				leafIn = st.leafTokSum / max(1, float64(st.leaves))
				testShare, numShare, judgeShare = frac(st.tests, st.leaves), frac(st.numerics, st.leaves), frac(st.judge, st.leaves)
				res[strong.Name].tag += st.tagUSD
				untagged := n - float64(st.tagged)
				if untagged > 0 {
					res[strong.Name].tag += st.tagUSD / float64(st.tagged) * untagged
				}
			} else {
				L = assumedLeaves[s] * sc.leafMul
				if L == 0 {
					L = 1.5 * sc.leafMul
				}
				L = min(L, float64(cfg.Taxonomy.MaxLeaves))
				leafIn = st.context + sc.cover*st.text/L + 20
				testShare, numShare, judgeShare = assumedCodeShare, assumedNumShare, sc.judgeMin
				in := n * (pTag + st.text + st.context) * sc.retry
				out := n * (20 + 40*L + sc.cover*st.text) * sc.retry
				res[strong.Name].tag += price(strong, in, out, false)
			}
			leaves := n * L
			nLeaves += leaves
			scale := 1.0
			if sc.name == "high" {
				scale = measuredScale
			}
			for _, m := range cfg.Models {
				calls := leaves * float64(cfg.Matrix.Draws)
				od := calls * cfg.Matrix.OnDemandSubsample
				out := sc.matrixOut
				if k := st.cells[m.Name]; k > 0 {
					out = st.outTok[m.Name] / float64(k) * scale
				}
				res[m.Name].matrix += price(m, od*leafIn, od*out, false) + price(m, (calls-od)*leafIn, (calls-od)*out, true)
				if k := st.labeled[m.Name]; k > 0 {
					// Measured label cost per cell, attributed to the judges
					// in proportion to their share of a two-judge call.
					per := st.labelUSD[m.Name] / float64(k) * scale
					js, _ := cfg.JudgesFor(m.Name)
					var w []float64
					var wsum float64
					for _, j := range js {
						x := price(j, pJudge+leafIn+out, sc.judgeOut, false)
						w = append(w, x)
						wsum += x
					}
					for i, j := range js {
						res[j.Name].judge += calls * per * w[i] / max(wsum, 1e-12)
					}
					continue
				}
				js, err := cfg.JudgesFor(m.Name)
				if err != nil {
					return err
				}
				jc := calls * judgeShare * sc.retry
				for _, j := range js {
					res[j.Name].judge += price(j, jc*(pJudge+leafIn+out), jc*sc.judgeOut, false)
				}
			}
			if st.refs > 0 {
				res[strong.Name].ref += leaves * (testShare + numShare) * st.refUSD / float64(st.refs) * scale
			} else {
				tc := leaves * testShare * sc.retry
				nc := leaves * numShare * 2
				res[strong.Name].ref += price(strong, tc*(pTest+leafIn), tc*sc.refOut, false) + price(strong, nc*(pNum+leafIn), nc*sc.refOut/2, false)
			}
		}
		results[sc.name] = res
		leavesOut[sc.name] = nLeaves
	}

	if piloted {
		fmt.Printf("Pilot-measured where the matrix and label have run: matrix output tokens per stratum and model, label cost per cell, cost per reference; high = measured ×%.1f. Unpiloted strata or models fall back to: ", measuredScale)
	}
	if measured {
		fmt.Printf("Assumptions (output lengths only): expected %.0f output tok per matrix call, %.0f tok per judge verdict, %.0f tok per reference, retry ×%.2f. High: output ×2, judges at the %d-token cap, references ×2, retry ×%.1f.\n",
			exp.matrixOut, exp.judgeOut, exp.refOut, exp.retry, cfg.Judges.MaxTokens, high.retry)
	} else {
		fmt.Printf("Assumptions. Expected: span cover %.0f%%, %.0f output tok per matrix call, ≥%.0f%% of cells judged, %.0f tok per judge verdict, %.0f%% code and %.0f%% numeric leaves get references (%.0f tok), retry ×%.2f. High: leaves ×1.5, cover 100%%, output ×2, every cell judged at the %d-token cap, references ×2, retry ×%.1f.\n",
			100*exp.cover, exp.matrixOut, 100*exp.judgeMin, exp.judgeOut, 100*assumedCodeShare, 100*assumedNumShare, exp.refOut, exp.retry, cfg.Judges.MaxTokens, high.retry)
	}
	fmt.Printf("Leaves: expected %.0f, high %.0f. Matrix: %d models × %d draws; Nova cells at batch prices except the %.0f%% on-demand latency subsample; Claude cells at on-demand prices (no Claude batch price listed; the recorded ceiling). Tag, reference and judge calls are on demand.\n\n",
		leavesOut["expected"], leavesOut["high"], len(cfg.Models), cfg.Matrix.Draws, 100*cfg.Matrix.OnDemandSubsample)
	tagHdr := "tag"
	if measured {
		tagHdr = "tag (measured)"
	}
	fmt.Printf("| model | %s | matrix | references | judges | **total expected** | total high |\n|---|---:|---:|---:|---:|---:|---:|\n", tagHdr)
	var te row
	var th float64
	for _, m := range cfg.Models {
		e, h := results["expected"][m.Name], results["high"][m.Name]
		fmt.Printf("| %s | %s | %s | %s | %s | **%s** | %s |\n", m.Name, usd(e.tag), usd(e.matrix), usd(e.ref), usd(e.judge), usd(e.tag+e.matrix+e.ref+e.judge), usd(h.tag+h.matrix+h.ref+h.judge))
		te.tag += e.tag
		te.matrix += e.matrix
		te.ref += e.ref
		te.judge += e.judge
		th += h.tag + h.matrix + h.ref + h.judge
	}
	fmt.Printf("| **total** | %s | %s | %s | %s | **%s** | %s |\n", usd(te.tag), usd(te.matrix), usd(te.ref), usd(te.judge), usd(te.tag+te.matrix+te.ref+te.judge), usd(th))
	var ht row
	for _, m := range cfg.Models {
		h := results["high"][m.Name]
		ht.matrix += h.matrix
		ht.ref += h.ref
		ht.judge += h.judge
	}
	fmt.Printf("\nBy stage (for the per-stage `-max-spend` caps), expected / high: matrix %s / %s; label (references + judges) %s / %s.\n",
		usd(te.matrix), usd(ht.matrix), usd(te.ref+te.judge), usd(ht.ref+ht.judge))
	if piloted {
		printPilot(cfg, names, strata, byModel, methods)
	}
	return nil
}

// printPilot prints what the pilot measured beyond cost: output lengths and
// stop reasons by model, and judge agreement and label methods by stratum.
func printPilot(cfg *config.Config, names []string, strata map[string]*stratum, byModel map[string]*modelStat, methods map[string]int) {
	fmt.Print("\n## Pilot measurements\n\n")
	fmt.Println("| model | cells | mean output tok | empty output | stop reasons |\n|---|---:|---:|---:|---|")
	for _, m := range cfg.Models {
		ms := byModel[m.Name]
		if ms == nil {
			continue
		}
		var stops []string
		for _, r := range sortedKeys(ms.stop) {
			stops = append(stops, fmt.Sprintf("%s %d", r, ms.stop[r]))
		}
		fmt.Printf("| %s | %d | %.0f | %.1f%% | %s |\n", m.Name, ms.n, ms.out/float64(ms.n), pct(ms.empty, ms.n), strings.Join(stops, ", "))
	}
	fmt.Println("\n| stratum | labeled cells | judged (two judges) | judges agree | judge failed |\n|---|---:|---:|---:|---:|")
	for _, s := range names {
		st := strata[s]
		var n int
		for _, k := range st.labeled {
			n += k
		}
		if n == 0 {
			continue
		}
		agree := "-"
		if st.judged > 0 {
			agree = fmt.Sprintf("%.1f%%", pct(st.agree, st.judged))
		}
		fmt.Printf("| %s | %d | %d | %s | %d |\n", s, n, st.judged, agree, st.judgeFailed)
	}
	var ms []string
	for _, k := range sortedKeys(methods) {
		ms = append(ms, fmt.Sprintf("%s %d", k, methods[k]))
	}
	fmt.Printf("\nLabel methods: %s.\n", strings.Join(ms, ", "))
}

func sortedKeys(m map[string]int) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
func price(m config.Model, in, out float64, batch bool) float64 {
	pi, po := m.InPerM, m.OutPerM
	if batch && m.BatchInPerM != nil && m.BatchOutPerM != nil {
		pi, po = *m.BatchInPerM, *m.BatchOutPerM
	}
	return (in*pi + out*po) / 1e6
}

func frac(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}

func pct(a, b int) float64 { return 100 * frac(a, b) }

func usd(v float64) string {
	if v == 0 {
		return "-"
	}
	return "$" + strings.TrimSuffix(fmt.Sprintf("%.2f", v), ".00")
}

func sortedStrata(m map[string]*stratum) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
