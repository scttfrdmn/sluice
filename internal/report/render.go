// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

package report

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/scttfrdmn/sluice/internal/certify"
)

func iv(i certify.Interval) string { return fmt.Sprintf("[%.3f, %.3f]", i.Lo, i.Hi) }

// Markdown writes report.md.
func (r *Report) Markdown(w io.Writer) {
	p := func(format string, a ...any) { fmt.Fprintf(w, format, a...) }
	p("# sluice E0 report\n\n")
	p("Split: **%s**. Taxonomy **%s** (iteration %d). δ = %.2f. Correctness = correct on both draws.\n\n", r.RunSplit, r.TaxonomyVersion, r.Iteration, r.Delta)

	p("## Decision\n\n%s\n\n%s\n\n", r.DecisionRule, r.Decision)

	p("## Pool and prices\n\nPrices dated %s (%s), source: %s.\n", r.Prices.Date, r.Prices.Region, r.Prices.Source)
	if r.Prices.Note != "" {
		p("%s\n", r.Prices.Note)
	}
	p("\n| model | id | provider | tier | $/M in | $/M out |\n|---|---|---|---|---:|---:|\n")
	for _, m := range r.Models {
		p("| %s | `%s` | %s | %s | %.4g | %.4g |\n", m.Name, m.ID, m.Provider, m.Tier, m.InPerM, m.OutPerM)
	}

	p("\n## 1. Corpus\n\n")
	if ss := r.Subsample; ss != nil {
		p("E0 subsample: split %s, seed %d, up to %d requests per stratum (ids in `subsample.json`).\n\n| stratum | selected | available |\n|---|---:|---:|\n", ss.Split, ss.Seed, ss.PerStratum)
		for _, k := range sortedKeys(ss.Available) {
			p("| %s | %d | %d |\n", k, ss.Selected[k], ss.Available[k])
		}
		p("\n")
	}
	corpusTable := func(title string, rows []CorpusRow) {
		p("%s\n\n| source | author_kind | requests | tagged | tag_failed | leaves | mean leaves/request | compound (≥2 leaves) | standalone |\n|---|---|---:|---:|---:|---:|---:|---:|---:|\n", title)
		for _, c := range rows {
			p("| %s | %s | %d | %d | %d | %d | %.2f | %.1f%% | %.1f%% |\n", orAll(c.Source), orAll(c.AuthorKind), c.Requests, c.Tagged, c.TagFailed, c.Leaves, c.MeanLeaves, 100*c.FracCompound, 100*c.FracStandalone)
		}
		p("\n")
	}
	corpusTable("By source:", r.Corpus)
	corpusTable("By author_kind:", r.CorpusByAuthor)
	corpusTable("By source × author_kind:", r.CorpusCross)
	p("Class histogram (leaves; total and by group):\n\n| class | leaves |")
	for _, g := range r.BreakdownCols {
		p(" %s |", g)
	}
	p("\n|---|---:|%s\n", strings.Repeat("---:|", len(r.BreakdownCols)))
	type kv struct {
		k string
		v int
	}
	var hist []kv
	for k, v := range r.ClassHistogram {
		hist = append(hist, kv{k, v})
	}
	sort.Slice(hist, func(i, j int) bool { return hist[i].v > hist[j].v || (hist[i].v == hist[j].v && hist[i].k < hist[j].k) })
	for _, h := range hist {
		p("| %s | %d |", h.k, h.v)
		for _, g := range r.BreakdownCols {
			p(" %d |", r.ClassByGroup[h.k][g])
		}
		p("\n")
	}
	p("\nClass × source × author_kind (leaves, n only). Per-class accuracy below is pooled: at this scale a class × source × model cell is too thin to certify, and this table shows how thin.\n\n| class |")
	for _, col := range r.ClassCrossCols {
		p(" %s |", col)
	}
	p("\n|---|%s\n", strings.Repeat("---:|", len(r.ClassCrossCols)))
	for _, h := range hist {
		p("| %s |", h.k)
		for _, col := range r.ClassCrossCols {
			p(" %d |", r.ClassCross[h.k][col])
		}
		p("\n")
	}
	c := r.Coverage
	p("\nCoverage (split %s): %d leaves, %d with all cells labeled; %d missing cells, %d unlabeled cells.\n", r.RunSplit, c.LeavesInSplit, c.LeavesComplete, c.MissingCells, c.UnlabeledCells)
	p("Label methods: %s. Rungs: %s.\n", fmtCounts(c.MethodCounts), fmtIntCounts(c.RungCounts))

	p("\n## 2. Noise\n\n| class | (leaf, model) pairs | draw flip rate | judged cells | judge disagreement |\n|---|---:|---:|---:|---:|\n")
	for _, n := range r.Noise {
		p("| %s | %d | %.3f | %d | %.3f |\n", n.Class, n.Pairs, n.FlipRate, n.JudgedCells, n.JudgeDisagree)
	}
	p("\nBy group:\n\n| group | (leaf, model) pairs | draw flip rate | judged cells | judge disagreement |\n|---|---:|---:|---:|---:|\n")
	for _, n := range r.NoiseByGroup {
		p("| %s | %d | %.3f | %d | %.3f |\n", n.Class, n.Pairs, n.FlipRate, n.JudgedCells, n.JudgeDisagree)
	}

	p("\n## 3. Per class\n\nAccuracy with Clopper–Pearson 95%% CI; certified error = one-sided %.0f%% upper bound.\n\n", 100*(1-r.Delta))
	names := make([]string, len(r.Models))
	for i, m := range r.Models {
		names[i] = m.Name
	}
	for _, cl := range r.Classes {
		rho := "undefined"
		if v := r.ClassRho[cl.Class]; v != nil {
			rho = fmt.Sprintf("%.3f", *v)
		}
		p("### %s (n=%d)\n\nbest: %s %.3f; oracle %.3f; within-class residual %.3f; ρ_c %s; rungs %s; cheapest clearing 5%%: %s; 10%%: %s.\n\n",
			cl.Class, cl.N, cl.Best, cl.BestAcc, cl.Oracle, cl.Residual, rho, cl.Rung, orNone(cl.Cheapest["0.05"]), orNone(cl.Cheapest["0.10"]))
		p("| model | accuracy | 95%% CI | certified error ≤ | mean $/call |\n|---|---:|---|---:|---:|\n")
		for _, n := range names {
			m := cl.PerModel[n]
			p("| %s | %.3f | %s | %.3f | %.5f |\n", n, m.Accuracy, iv(m.CI), m.ErrUpper, m.MeanCost)
		}
		p("\n")
	}

	p("## 4. Headline\n\nSelection-valid intervals: each of K models at δ/(2K), oracle at δ/2, best-per-class Bonferroni over classes × models. ρ interval: paired bootstrap over requests. `pooled (source-balanced)` gives every source equal total weight (point estimates; ρ bootstrap resamples requests within each source; Clopper–Pearson intervals do not apply and are shown as -). Groups: `source:*` by source, `author:*` by author_kind.\n\n")
	p("| group | leaves | requests | best single | best-per-class | oracle | oracle − best gap | ρ | ρ 95%% |\n|---|---:|---:|---|---|---|---|---:|---|\n")
	for _, g := range r.GroupOrder {
		h := r.Groups[g]
		rho, rhoCI := "undefined", "-"
		if h.Rho != nil {
			rho = fmt.Sprintf("%.3f", *h.Rho)
		}
		if h.RhoCI != nil {
			rhoCI = iv(*h.RhoCI)
		}
		civ := iv
		if h.Weighting == "source-balanced" {
			civ = func(certify.Interval) string { return "-" }
		}
		p("| %s | %d | %d | %.3f %s (%s) | %.3f %s | %.3f %s | %s | %s | %s |\n", g, h.N, h.NRequests,
			h.BestSingleAcc, civ(h.BestSingleCI), h.BestSingle, h.BestPerClass, civ(h.BestPerClassCI), h.Oracle, civ(h.OracleCI), civ(h.GapCI), rho, rhoCI)
	}

	p("\n## 5. Cost and latency (measured)\n\nLatency is on-demand calls only, including service-side queueing; batch cells carry batch wall time separately and are excluded.\n\n")
	p("| model | cells | total $ | mean $/call | mean tok in | mean tok out | on-demand n | p50 ms | p90 ms | mean ms | batch cells | batch priced on-demand |\n|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, m := range r.CostLatency {
		p("| %s | %d | %.2f | %.5f | %.0f | %.0f | %d | %.0f | %.0f | %.0f | %d | %d |\n", m.Model, m.Cells, m.TotalUSD, m.MeanUSDPerCall, m.MeanTokensIn, m.MeanTokensOut, m.OnDemandN, m.LatP50, m.LatP90, m.LatMean, m.BatchCells, m.FallbackPriced)
	}
	pol := r.Policy
	p("\nBest-per-class policy (pooled assignment) vs always-%s over %d leaves, one call per leaf: $%.4f vs $%.4f (ratio %.3f). Excludes tagger ($%.2f total) and labeling ($%.2f total).\n",
		pol.StrongModel, pol.Leaves, pol.BestPerClassUSD, pol.AlwaysStrongUSD, pol.Ratio, r.TaggerCostUSD, r.LabelCostUSD)
	p("\nPlots: `accuracy_cost.png`, `class_rho.png`.\n")
}

func orAll(s string) string {
	if s == "" {
		return "all"
	}
	return s
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func fmtCounts(m map[string]int) string {
	var parts []string
	for _, k := range sortedKeys(m) {
		parts = append(parts, fmt.Sprintf("%s %d", k, m[k]))
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

func fmtIntCounts(m map[int]int) string {
	var ks []int
	for k := range m {
		ks = append(ks, k)
	}
	sort.Ints(ks)
	var parts []string
	for _, k := range ks {
		parts = append(parts, fmt.Sprintf("rung %d: %d", k, m[k]))
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}
