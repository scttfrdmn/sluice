// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

package report

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/scttfrdmn/sluice/internal/certify"
	"github.com/scttfrdmn/sluice/internal/config"
	"github.com/scttfrdmn/sluice/internal/corpus"
	"github.com/scttfrdmn/sluice/internal/label"
	"github.com/scttfrdmn/sluice/internal/matrix"
)

// DecisionRule is PROJECT.md §4 E0's decision text, printed verbatim.
const DecisionRule = "Decision: ρ ≥ 0.50 → proceed to E1. ρ < 0.50 → iterate the taxonomy once on the same matrix (re-tag only) and re-measure. After that one iteration, the report prints the number and the rule; Scott decides whether to proceed. Nothing in the pipeline declares the gate passed or failed on its own."

// Inputs are the run artifacts the report reads.
type Inputs struct {
	Config    *config.Config
	Records   []corpus.Record
	Tags      []corpus.TagResult
	Leaves    []corpus.Leaf
	Cells     []matrix.Cell
	Labels    []label.Label
	Split     string // calibration | test | all
	Taxonomy  string
	Iteration int // 0 for taxonomy v0, 1 after the one iteration
}

// Report is report.json.
type Report struct {
	RunSplit        string              `json:"split"`
	TaxonomyVersion string              `json:"taxonomy_version"`
	Iteration       int                 `json:"taxonomy_iteration"`
	Models          []ModelInfo         `json:"models"`
	Prices          config.Prices       `json:"prices"`
	Delta           float64             `json:"delta"`
	Corpus          []CorpusRow         `json:"corpus"`
	ClassHistogram  map[string]int      `json:"class_histogram"`
	Coverage        Coverage            `json:"coverage"`
	Noise           []NoiseRow          `json:"noise"`
	Classes         []ClassRow          `json:"classes"`
	Groups          map[string]Headline `json:"groups"`
	GroupOrder      []string            `json:"group_order"`
	CostLatency     []ModelCost         `json:"cost_latency"`
	Policy          PolicyCost          `json:"policy_cost"`
	TaggerCostUSD   float64             `json:"tagger_cost_usd"`
	LabelCostUSD    float64             `json:"label_cost_usd"`
	Decision        string              `json:"decision"`
	DecisionRule    string              `json:"decision_rule"`
	ClassRho        map[string]*float64 `json:"class_rho"`
}

// ModelInfo is one pool entry as printed.
type ModelInfo struct {
	Name, ID, Provider, Tier string
	InPerM, OutPerM          float64
}

// CorpusRow is one source in the corpus table.
type CorpusRow struct {
	Source         string  `json:"source"`
	Requests       int     `json:"requests"`
	Tagged         int     `json:"tagged"`
	TagFailed      int     `json:"tag_failed"`
	MeanLeaves     float64 `json:"mean_leaves"`
	FracCompound   float64 `json:"frac_compound"`
	FracStandalone float64 `json:"frac_standalone"`
	Leaves         int     `json:"leaves"`
}

// Coverage counts what was excluded from the statistics and why.
type Coverage struct {
	LeavesInSplit   int            `json:"leaves_in_split"`
	LeavesComplete  int            `json:"leaves_complete"`
	MissingCells    int            `json:"missing_cells"`
	UnlabeledCells  int            `json:"unlabeled_cells"`
	MethodCounts    map[string]int `json:"label_method_counts"`
	RungCounts      map[int]int    `json:"rung_counts"`
	RefStatusCounts map[string]int `json:"ref_status_counts,omitempty"`
}

// NoiseRow is the per-class draw flip rate and judge disagreement.
type NoiseRow struct {
	Class         string  `json:"class"`
	Pairs         int     `json:"pairs"`
	FlipRate      float64 `json:"flip_rate"`
	JudgedCells   int     `json:"judged_cells"`
	JudgeDisagree float64 `json:"judge_disagreement"`
}

// ModelClass is one model's numbers within a class.
type ModelClass struct {
	Accuracy float64          `json:"accuracy"`
	CI       certify.Interval `json:"ci95"`
	ErrUpper float64          `json:"certified_error_upper"`
	MeanCost float64          `json:"mean_cost_usd"`
}

// ClassRow is the per-class diagnostic.
type ClassRow struct {
	Class    string                `json:"class"`
	N        int                   `json:"n"`
	PerModel map[string]ModelClass `json:"per_model"`
	Best     string                `json:"best_model"`
	BestAcc  float64               `json:"best_accuracy"`
	Oracle   float64               `json:"oracle"`
	Residual float64               `json:"within_class_residual"`
	Cheapest map[string]string     `json:"cheapest_clearing"` // threshold -> model or ""
	Rung     string                `json:"verifier_rung"`
}

// ModelCost is measured cost and latency for one model.
type ModelCost struct {
	Model          string  `json:"model"`
	Cells          int     `json:"cells"`
	TotalUSD       float64 `json:"total_usd"`
	MeanUSDPerCall float64 `json:"mean_usd_per_call"`
	MeanTokensIn   float64 `json:"mean_tokens_in"`
	MeanTokensOut  float64 `json:"mean_tokens_out"`
	OnDemandN      int     `json:"on_demand_n"`
	LatP50         float64 `json:"latency_p50_ms"`
	LatP90         float64 `json:"latency_p90_ms"`
	LatMean        float64 `json:"latency_mean_ms"`
	BatchCells     int     `json:"batch_cells"`
	FallbackPriced int     `json:"batch_cells_priced_on_demand"`
}

// PolicyCost compares the best-per-class policy to always-strong.
type PolicyCost struct {
	Leaves          int     `json:"leaves"`
	BestPerClassUSD float64 `json:"best_per_class_usd"`
	AlwaysStrongUSD float64 `json:"always_strong_usd"`
	Ratio           float64 `json:"ratio"`
	StrongModel     string  `json:"strong_model"`
}

// Build computes the whole report.
func Build(in Inputs) (*Report, error) {
	cfg := in.Config
	models := make([]string, len(cfg.Models))
	mIdx := map[string]int{}
	rep := &Report{RunSplit: in.Split, TaxonomyVersion: in.Taxonomy, Iteration: in.Iteration, Prices: cfg.Prices,
		Delta: cfg.Report.Delta, Groups: map[string]Headline{}, ClassRho: map[string]*float64{}, DecisionRule: DecisionRule,
		ClassHistogram: map[string]int{}}
	for i, m := range cfg.Models {
		models[i] = m.Name
		mIdx[m.Name] = i
		rep.Models = append(rep.Models, ModelInfo{m.Name, m.ID, m.Provider, m.Tier, m.InPerM, m.OutPerM})
	}
	k := len(models)

	// Corpus table over all records (not only the split).
	recByID := map[string]corpus.Record{}
	for _, r := range in.Records {
		recByID[r.ID] = r
	}
	type agg struct {
		req, tagged, failed, leaves, compound, standalone int
	}
	src := map[string]*agg{}
	get := func(s string) *agg {
		if src[s] == nil {
			src[s] = &agg{}
		}
		return src[s]
	}
	for _, r := range in.Records {
		get(r.Source).req++
	}
	for _, t := range in.Tags {
		a := get(recByID[t.RequestID].Source)
		rep.TaggerCostUSD += t.CostUSD
		if t.Status != corpus.TagOK {
			a.failed++
			continue
		}
		a.tagged++
		a.leaves += len(t.Leaves)
		if len(t.Leaves) >= 2 {
			a.compound++
		}
		if t.Standalone {
			a.standalone++
		}
	}
	for _, s := range sortedKeys(src) {
		a := src[s]
		row := CorpusRow{Source: s, Requests: a.req, Tagged: a.tagged, TagFailed: a.failed, Leaves: a.leaves}
		if a.tagged > 0 {
			row.MeanLeaves = float64(a.leaves) / float64(a.tagged)
			row.FracCompound = float64(a.compound) / float64(a.tagged)
			row.FracStandalone = float64(a.standalone) / float64(a.tagged)
		}
		rep.Corpus = append(rep.Corpus, row)
	}
	for _, l := range in.Leaves {
		rep.ClassHistogram[l.Class]++
	}

	// Labels by cell, cells by key.
	labByKey := map[string]label.Label{}
	for _, l := range in.Labels {
		labByKey[l.Key()] = l
		rep.LabelCostUSD += l.CostUSD
	}
	cellByKey := map[string]matrix.Cell{}
	for _, c := range in.Cells {
		cellByKey[c.Key()] = c
	}

	// Items: leaves in the split with all K×2 cells labeled.
	cov := Coverage{MethodCounts: map[string]int{}, RungCounts: map[int]int{}}
	var items []Item
	leafMeta := map[string]corpus.Leaf{}
	noise := map[string]*struct{ pairs, flips, judged, disagree int }{}
	for _, l := range in.Leaves {
		if in.Split != "all" && l.Split != in.Split {
			continue
		}
		cov.LeavesInSplit++
		leafMeta[l.LeafID] = l
		it := Item{LeafID: l.LeafID, RequestID: l.RequestID, Class: l.Class, Correct: make([]bool, k), Cost: make([]float64, k)}
		complete := true
		nz := noise[l.Class]
		if nz == nil {
			nz = &struct{ pairs, flips, judged, disagree int }{}
			noise[l.Class] = nz
		}
		for m, name := range models {
			var draws [2]*bool
			var cost float64
			for d := 0; d < 2; d++ {
				key := matrix.CellKey(l.LeafID, name, d)
				c, ok := cellByKey[key]
				if !ok {
					cov.MissingCells++
					complete = false
					continue
				}
				cost += c.CostUSD / 2
				lab, ok := labByKey[key]
				if !ok || lab.Correct == nil {
					cov.UnlabeledCells++
					complete = false
					continue
				}
				cov.MethodCounts[lab.Method]++
				cov.RungCounts[lab.Rung]++
				if lab.JudgeAgreement != nil {
					nz.judged++
					if !*lab.JudgeAgreement {
						nz.disagree++
					}
				}
				draws[d] = lab.Correct
			}
			if draws[0] != nil && draws[1] != nil {
				nz.pairs++
				if *draws[0] != *draws[1] {
					nz.flips++
				}
				it.Correct[m] = *draws[0] && *draws[1]
			}
			it.Cost[m] = cost
		}
		if complete {
			items = append(items, it)
		}
	}
	cov.LeavesComplete = len(items)
	rep.Coverage = cov
	for _, c := range sortedKeys(noise) {
		nz := noise[c]
		row := NoiseRow{Class: c, Pairs: nz.pairs, JudgedCells: nz.judged}
		if nz.pairs > 0 {
			row.FlipRate = float64(nz.flips) / float64(nz.pairs)
		}
		if nz.judged > 0 {
			row.JudgeDisagree = float64(nz.disagree) / float64(nz.judged)
		}
		rep.Noise = append(rep.Noise, row)
	}

	// Groups.
	groups := []struct {
		name string
		keep func(corpus.Leaf) bool
	}{
		{"pooled", func(corpus.Leaf) bool { return true }},
		{"owner", func(l corpus.Leaf) bool { return corpus.Owner(l.Source) }},
		{"public", func(l corpus.Leaf) bool { return !corpus.Owner(l.Source) }},
		{"standalone", func(l corpus.Leaf) bool { return l.Standalone }},
		{"context-dependent", func(l corpus.Leaf) bool { return !l.Standalone }},
	}
	for _, s := range []string{corpus.SourceClaudeCode, corpus.SourceClaudeExport, corpus.SourceChatGPT, corpus.SourceRepos} {
		groups = append(groups, struct {
			name string
			keep func(corpus.Leaf) bool
		}{"source:" + s, func(l corpus.Leaf) bool { return l.Source == s }})
	}
	for gi, g := range groups {
		var sub []Item
		for _, it := range items {
			if g.keep(leafMeta[it.LeafID]) {
				sub = append(sub, it)
			}
		}
		if len(sub) == 0 && gi > 0 {
			continue
		}
		rep.Groups[g.name] = Compute(sub, models, cfg.Report.Delta, cfg.Report.Bootstrap, cfg.Seeds.Bootstrap+uint64(gi))
		rep.GroupOrder = append(rep.GroupOrder, g.name)
	}

	// Per class.
	byClass := map[string][]Item{}
	for _, it := range items {
		byClass[it.Class] = append(byClass[it.Class], it)
	}
	rungByClass := map[string]map[int]int{}
	for _, it := range items {
		for _, name := range models {
			for d := 0; d < 2; d++ {
				lab := labByKey[matrix.CellKey(it.LeafID, name, d)]
				if rungByClass[it.Class] == nil {
					rungByClass[it.Class] = map[int]int{}
				}
				rungByClass[it.Class][lab.Rung]++
			}
		}
	}
	for _, c := range sortedKeys(byClass) {
		its := byClass[c]
		n := len(its)
		acc := accuracies(its, k)
		row := ClassRow{Class: c, N: n, PerModel: map[string]ModelClass{}, Cheapest: map[string]string{}}
		var oracle float64
		for _, it := range its {
			for _, ok := range it.Correct {
				if ok {
					oracle++
					break
				}
			}
		}
		row.Oracle = oracle / float64(n)
		meanCost := make([]float64, k)
		for m, name := range models {
			for _, it := range its {
				meanCost[m] += it.Cost[m] / float64(n)
			}
			kc := int(math.Round(acc[m] * float64(n)))
			row.PerModel[name] = ModelClass{
				Accuracy: acc[m],
				CI:       certify.ClopperPearson(kc, n, 0.05),
				ErrUpper: certify.UpperBound(n-kc, n, cfg.Report.Delta),
				MeanCost: meanCost[m],
			}
		}
		bm := argmax(acc)
		row.Best, row.BestAcc = models[bm], acc[bm]
		row.Residual = row.Oracle - row.BestAcc
		for _, th := range cfg.Report.Thresholds {
			best, bestCost := "", math.Inf(1)
			for m, name := range models {
				if row.PerModel[name].ErrUpper <= th && meanCost[m] < bestCost {
					best, bestCost = name, meanCost[m]
				}
			}
			row.Cheapest[fmt.Sprintf("%.2f", th)] = best
		}
		row.Rung = rungSummary(rungByClass[c])
		rep.Classes = append(rep.Classes, row)
		h := Compute(its, models, cfg.Report.Delta, 0, 0)
		rep.ClassRho[c] = h.Rho
	}

	// Cost and latency per model (all cells in the split).
	for _, name := range models {
		mc := ModelCost{Model: name}
		var lats []float64
		var tin, tout float64
		for _, c := range in.Cells {
			if c.Model != name {
				continue
			}
			if l, ok := leafMeta[c.LeafID]; !ok || (in.Split != "all" && l.Split != in.Split) {
				continue
			}
			mc.Cells++
			mc.TotalUSD += c.CostUSD
			tin += float64(c.TokensIn)
			tout += float64(c.TokensOut)
			if c.LatencyMs != nil {
				lats = append(lats, float64(*c.LatencyMs))
			}
			if c.Mode == matrix.ModeBatch {
				mc.BatchCells++
				if c.PriceBasis == "on-demand-fallback" {
					mc.FallbackPriced++
				}
			}
		}
		if mc.Cells > 0 {
			mc.MeanUSDPerCall = mc.TotalUSD / float64(mc.Cells)
			mc.MeanTokensIn, mc.MeanTokensOut = tin/float64(mc.Cells), tout/float64(mc.Cells)
		}
		mc.OnDemandN = len(lats)
		if len(lats) > 0 {
			sort.Float64s(lats)
			mc.LatP50 = lats[len(lats)/2]
			mc.LatP90 = lats[int(0.9*float64(len(lats)-1))]
			var s float64
			for _, v := range lats {
				s += v
			}
			mc.LatMean = s / float64(len(lats))
		}
		rep.CostLatency = append(rep.CostLatency, mc)
	}

	// Best-per-class policy cost vs always-strong, per leaf (one call each).
	strong := mIdx[cfg.Strong]
	pol := PolicyCost{StrongModel: cfg.Strong, Leaves: len(items)}
	assign := rep.Groups["pooled"].Assignment
	for _, it := range items {
		pol.AlwaysStrongUSD += it.Cost[strong]
		pol.BestPerClassUSD += it.Cost[mIdx[assign[it.Class]]]
	}
	if pol.AlwaysStrongUSD > 0 {
		pol.Ratio = pol.BestPerClassUSD / pol.AlwaysStrongUSD
	}
	rep.Policy = pol
	rep.Decision = decisionLine(rep, cfg.Report.RhoProceed)
	return rep, nil
}

func rungSummary(m map[int]int) string {
	if len(m) == 0 {
		return "-"
	}
	var parts []string
	for _, r := range []int{1, 2, 3} {
		if m[r] > 0 {
			parts = append(parts, fmt.Sprintf("r%d:%d", r, m[r]))
		}
	}
	return strings.Join(parts, " ")
}

// decisionLine states the observed numbers against the rule. It reports
// the comparison; it does not declare the gate passed or failed.
func decisionLine(rep *Report, threshold float64) string {
	h := rep.Groups["pooled"]
	var b strings.Builder
	fmt.Fprintf(&b, "Observed (pooled, split=%s, taxonomy %s, iteration %d, n=%d leaves / %d requests): ",
		rep.RunSplit, rep.TaxonomyVersion, rep.Iteration, h.N, h.NRequests)
	fmt.Fprintf(&b, "best-single %.4f (%s) [%.4f, %.4f]; best-per-class %.4f [%.4f, %.4f]; oracle %.4f [%.4f, %.4f]; ",
		h.BestSingleAcc, h.BestSingle, h.BestSingleCI.Lo, h.BestSingleCI.Hi,
		h.BestPerClass, h.BestPerClassCI.Lo, h.BestPerClassCI.Hi,
		h.Oracle, h.OracleCI.Lo, h.OracleCI.Hi)
	if h.Rho == nil {
		b.WriteString("ρ undefined (oracle equals best-single).")
		return b.String()
	}
	fmt.Fprintf(&b, "ρ = %.4f", *h.Rho)
	if h.RhoCI != nil {
		fmt.Fprintf(&b, " (paired bootstrap 95%% [%.4f, %.4f])", h.RhoCI.Lo, h.RhoCI.Hi)
	}
	cmp := "≥"
	if *h.Rho < threshold {
		cmp = "<"
	}
	fmt.Fprintf(&b, ". Point estimate %s %.2f. ", cmp, threshold)
	if rep.Iteration == 0 && *h.Rho < threshold {
		b.WriteString("Under the rule this is the branch that calls for one taxonomy iteration before Scott decides.")
	} else {
		b.WriteString("Scott decides whether to proceed.")
	}
	return b.String()
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
