// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

// Command estimate projects the E0 spend (tag, matrix, references, judges)
// from the price table and the token statistics of a run's E0 subsample,
// before any model call is made. It prints a markdown table split by model
// and stage for an expected and a high scenario.
//
// Measured: request and context lengths per stratum (chars / chars_per_token)
// and prompt-template sizes. Assumed (flags): leaves per request, the share
// of request text the leaf spans cover, output length per matrix call, the
// share of cells that reach the judges, and reference rates. Prices: batch
// where the price list has one (Nova matrix cells outside the on-demand
// latency subsample); on-demand everywhere else, which is the ceiling for
// Claude batch cells (no Claude batch price is listed).
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
	"github.com/scttfrdmn/sluice/internal/rundir"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "estimate:", err)
		os.Exit(1)
	}
}

// scenario holds the assumed quantities.
type scenario struct {
	name       string
	leaves     map[string]float64 // mean leaves per request, by stratum
	cover      float64            // share of request text inside leaf spans
	matrixOut  float64            // mean output tokens per matrix call
	judgeShare float64            // share of cells labeled by the judges
	judgeOut   float64            // output tokens per judge call
	codeShare  float64            // share of leaves needing a generated test
	numShare   float64            // share of leaves needing a numeric reference
	refOut     float64            // output tokens per reference call
	retry      float64            // multiplier for invalid-JSON retries
}

type stratum struct {
	n             int
	text, context float64 // mean tokens
}

func run() error {
	var (
		cfgPath  = flag.String("config", "config/sluice.yaml", "config file")
		runsRoot = flag.String("runs", "runs", "runs root")
		runID    = flag.String("run", "", "run id (required)")
	)
	exp := scenario{name: "expected", cover: 0.7, matrixOut: 800, judgeShare: 0.7, judgeOut: 100, codeShare: 0.3, numShare: 0.03, refOut: 1200, retry: 1.05,
		leaves: map[string]float64{corpus.SourceClaudeCode: 1.3, "repos:issue": 2.5, "repos:prompt-file": 5, corpus.SourceWildchat: 1.2}}
	flag.Float64Var(&exp.cover, "cover", exp.cover, "share of request text inside leaf spans")
	flag.Float64Var(&exp.matrixOut, "matrix-out", exp.matrixOut, "mean output tokens per matrix call")
	flag.Float64Var(&exp.judgeShare, "judge-share", exp.judgeShare, "share of cells labeled by judges")
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
	recs, err := jsonl.Read[corpus.Record](dir.Records())
	if err != nil {
		return err
	}
	ids, ss, err := corpus.ReadSubsample(dir.Subsample())
	if err != nil {
		return err
	}
	if ids == nil {
		return fmt.Errorf("%s missing; run harvest subsample first", dir.Subsample())
	}
	recs = corpus.FilterSubsample(recs, ids)
	cpt := cfg.Taxonomy.CharsPerToken
	strata := map[string]*stratum{}
	for _, r := range recs {
		s := corpus.Stratum(r)
		if strata[s] == nil {
			strata[s] = &stratum{}
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
	high.leaves = map[string]float64{}
	for k, v := range exp.leaves {
		high.leaves[k] = min(v*1.5, float64(cfg.Taxonomy.MaxLeaves))
	}
	high.cover, high.matrixOut, high.judgeShare, high.judgeOut, high.refOut, high.retry = 1, exp.matrixOut*2, 1, float64(cfg.Judges.MaxTokens), exp.refOut*2, 1.2

	names := sortedStrata(strata)
	fmt.Printf("## E0 cost projection: run `%s`, subsample split %s, %d requests\n\n", *runID, ss.Split, len(recs))
	fmt.Printf("Prices dated %s (%s). Tokens = chars / %.1f.\n\n", cfg.Prices.Date, cfg.Prices.Region, cpt)
	fmt.Println("| stratum | requests | mean request tok | mean context tok | leaves/request (expected / high) |\n|---|---:|---:|---:|---|")
	for _, s := range names {
		st := strata[s]
		fmt.Printf("| %s | %d | %.0f | %.0f | %.1f / %.1f |\n", s, st.n, st.text, st.context, exp.leaves[s], high.leaves[s])
	}
	fmt.Printf("\nTemplate overhead (tokens): tag %.0f, judge %.0f, test %.0f, numeric %.0f.\n\n", pTag, pJudge, pTest, pNum)

	type row struct{ tag, matrix, ref, judge float64 }
	results := map[string]map[string]*row{}
	var leavesOut = map[string]float64{}
	for _, sc := range []scenario{exp, high} {
		res := map[string]*row{}
		for _, m := range cfg.Models {
			res[m.Name] = &row{}
		}
		strong, _ := cfg.Model(cfg.Strong)
		var nLeaves float64
		for _, s := range names {
			st := strata[s]
			n, L := float64(st.n), sc.leaves[s]
			if L == 0 {
				L = 1.5
			}
			// Tag: one strong-model call per request; output is the JSON with
			// quoted spans plus ~40 tokens of fields per leaf.
			in := n * (pTag + st.text + st.context) * sc.retry
			out := n * (20 + 40*L + sc.cover*st.text) * sc.retry
			res[strong.Name].tag += price(strong, in, out, false)

			leaves := n * L
			nLeaves += leaves
			leafIn := st.context + sc.cover*st.text/L + 20 // + preamble
			for _, m := range cfg.Models {
				calls := leaves * float64(cfg.Matrix.Draws)
				od := calls * cfg.Matrix.OnDemandSubsample
				res[m.Name].matrix += price(m, od*leafIn, od*sc.matrixOut, false) + price(m, (calls-od)*leafIn, (calls-od)*sc.matrixOut, true)
				// Judges: two per judged cell, one per provider.
				js, err := cfg.JudgesFor(m.Name)
				if err != nil {
					return err
				}
				jc := calls * sc.judgeShare * sc.retry
				for _, j := range js {
					res[j.Name].judge += price(j, jc*(pJudge+leafIn+sc.matrixOut), jc*sc.judgeOut, false)
				}
			}
			// References from the strong model: tests (≤2 attempts), numeric (2 draws).
			tc := leaves * sc.codeShare * sc.retry
			nc := leaves * sc.numShare * 2
			res[strong.Name].ref += price(strong, tc*(pTest+leafIn), tc*sc.refOut, false) + price(strong, nc*(pNum+leafIn), nc*sc.refOut/2, false)
		}
		results[sc.name] = res
		leavesOut[sc.name] = nLeaves
	}

	fmt.Printf("Assumptions. Expected: span cover %.0f%%, %.0f output tok per matrix call, %.0f%% of cells judged, %.0f tok per judge verdict, %.0f%% code leaves and %.0f%% numeric leaves get references (%.0f tok), retry ×%.2f. High: leaves ×1.5, cover 100%%, output ×2, every cell judged at the %d-token judge cap, reference output ×2, retry ×%.1f.\n",
		100*exp.cover, exp.matrixOut, 100*exp.judgeShare, exp.judgeOut, 100*exp.codeShare, 100*exp.numShare, exp.refOut, exp.retry, cfg.Judges.MaxTokens, high.retry)
	fmt.Printf("Leaves: expected %.0f, high %.0f. Matrix: %d models × %d draws; Nova cells at batch prices except the %.0f%% on-demand latency subsample; Claude cells at on-demand prices (no Claude batch price listed; this is the recorded ceiling). Tag, reference and judge calls are on-demand.\n\n",
		leavesOut["expected"], leavesOut["high"], len(cfg.Models), cfg.Matrix.Draws, 100*cfg.Matrix.OnDemandSubsample)
	fmt.Println("| model | tag | matrix | references | judges | **total expected** | total high |\n|---|---:|---:|---:|---:|---:|---:|")
	var te, th row
	for _, m := range cfg.Models {
		e, h := results["expected"][m.Name], results["high"][m.Name]
		fmt.Printf("| %s | %s | %s | %s | %s | **%s** | %s |\n", m.Name, usd(e.tag), usd(e.matrix), usd(e.ref), usd(e.judge), usd(e.tag+e.matrix+e.ref+e.judge), usd(h.tag+h.matrix+h.ref+h.judge))
		te.tag += e.tag
		te.matrix += e.matrix
		te.ref += e.ref
		te.judge += e.judge
		th.tag += h.tag + h.matrix + h.ref + h.judge
	}
	fmt.Printf("| **total** | %s | %s | %s | %s | **%s** | %s |\n", usd(te.tag), usd(te.matrix), usd(te.ref), usd(te.judge), usd(te.tag+te.matrix+te.ref+te.judge), usd(th.tag))
	return nil
}

func price(m config.Model, in, out float64, batch bool) float64 {
	pi, po := m.InPerM, m.OutPerM
	if batch && m.BatchInPerM != nil && m.BatchOutPerM != nil {
		pi, po = *m.BatchInPerM, *m.BatchOutPerM
	}
	return (in*pi + out*po) / 1e6
}

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
