// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

// Package report computes the E0 deliverable (PROJECT.md §4 E0,
// CC_PROMPT.md cmd/report): best single model, best fixed model per class,
// per-instance oracle, selection-valid intervals, the class-oracle fraction
// ρ with a paired bootstrap interval, and per-class diagnostics.
//
// Correctness for a (leaf, model) is correct on both draws, for every
// quantity here (PROJECT.md §6). A leaf enters the statistics only if all K
// models have both draws labeled.
package report

import (
	"math"
	"math/rand/v2"
	"sort"

	"github.com/scttfrdmn/sluice/internal/certify"
)

// Item is one leaf with its per-model correctness and cost.
type Item struct {
	LeafID    string
	RequestID string
	Class     string
	Source    string
	Correct   []bool    // indexed by model
	Cost      []float64 // mean cost of one call (mean over the two draws)
	// Weight is the item's weight in accuracies; 0 means 1. Source-balanced
	// groups set it to 1/(leaves of the item's source).
	Weight float64
}

func (it Item) w() float64 {
	if it.Weight == 0 {
		return 1
	}
	return it.Weight
}

// Headline is the three numbers with intervals for one group.
type Headline struct {
	N              int                `json:"n_leaves"`
	NRequests      int                `json:"n_requests"`
	K              int                `json:"k_models"`
	Accuracy       map[string]float64 `json:"accuracy_by_model"`
	BestSingle     string             `json:"best_single_model"`
	BestSingleAcc  float64            `json:"best_single"`
	BestSingleCI   certify.Interval   `json:"best_single_interval"`
	BestPerClass   float64            `json:"best_per_class"`
	BestPerClassCI certify.Interval   `json:"best_per_class_interval"`
	Oracle         float64            `json:"oracle"`
	OracleCI       certify.Interval   `json:"oracle_interval"`
	GapCI          certify.Interval   `json:"oracle_gap_interval"`
	Rho            *float64           `json:"rho"`
	RhoCI          *certify.Interval  `json:"rho_bootstrap_interval"`
	RhoUndefined   int                `json:"rho_bootstrap_undefined"`
	Classes        int                `json:"n_classes"`
	Assignment     map[string]string  `json:"best_per_class_assignment"`
	// Weighting is "natural" (every leaf weight 1) or "source-balanced"
	// (equal total weight per source). Clopper–Pearson intervals assume
	// natural weights and are omitted for source-balanced groups; the ρ
	// bootstrap there resamples requests within each source.
	Weighting string `json:"weighting"`
}

func accuracies(items []Item, k int) []float64 {
	acc := make([]float64, k)
	if len(items) == 0 {
		return acc
	}
	var tw float64
	for _, it := range items {
		w := it.w()
		tw += w
		for m, c := range it.Correct {
			if c {
				acc[m] += w
			}
		}
	}
	for m := range acc {
		acc[m] /= tw
	}
	return acc
}

func argmax(xs []float64) int {
	best := 0
	for i, x := range xs {
		if x > xs[best] {
			best = i
		}
	}
	return best
}

// point computes best-single, best-per-class and oracle accuracy, and the
// per-class assignment.
func point(items []Item, k int) (bs float64, bsModel int, bpc float64, oracle float64, assign map[string]int) {
	if len(items) == 0 {
		return 0, 0, 0, 0, nil
	}
	acc := accuracies(items, k)
	bsModel = argmax(acc)
	bs = acc[bsModel]
	byClass := map[string][]Item{}
	var tw float64
	for _, it := range items {
		byClass[it.Class] = append(byClass[it.Class], it)
		tw += it.w()
		for _, c := range it.Correct {
			if c {
				oracle += it.w()
				break
			}
		}
	}
	oracle /= tw
	assign = map[string]int{}
	for c, its := range byClass {
		a := accuracies(its, k)
		m := argmax(a)
		assign[c] = m
		var cw float64
		for _, it := range its {
			cw += it.w()
		}
		bpc += a[m] * cw
	}
	bpc /= tw
	return
}

func rho(bs, bpc, oracle float64) *float64 {
	den := oracle - bs
	if den <= 1e-12 {
		return nil
	}
	r := (bpc - bs) / den
	return &r
}

// balance sets each item's weight to 1/(items from its source), so every
// source carries equal total weight.
func balance(items []Item) []Item {
	n := map[string]int{}
	for _, it := range items {
		n[it.Source]++
	}
	out := make([]Item, len(items))
	for i, it := range items {
		it.Weight = 1 / float64(n[it.Source])
		out[i] = it
	}
	return out
}

// ComputeBalanced is Compute with equal weight per source: point estimates
// and a source-stratified paired bootstrap for ρ, no Clopper–Pearson
// intervals.
func ComputeBalanced(items []Item, models []string, boot int, seed uint64) Headline {
	k := len(models)
	h := Headline{N: len(items), K: k, Accuracy: map[string]float64{}, Assignment: map[string]string{}, Weighting: "source-balanced"}
	reqs := map[string][]int{}
	for i, it := range items {
		reqs[it.RequestID] = append(reqs[it.RequestID], i)
	}
	h.NRequests = len(reqs)
	if len(items) == 0 {
		return h
	}
	bal := balance(items)
	bs, bsm, bpc, oracle, assign := point(bal, k)
	for m, name := range models {
		h.Accuracy[name] = accuracies(bal, k)[m]
	}
	h.BestSingle, h.BestSingleAcc, h.BestPerClass, h.Oracle = models[bsm], bs, bpc, oracle
	for c, m := range assign {
		h.Assignment[c] = models[m]
	}
	h.Classes = len(assign)
	h.Rho = rho(bs, bpc, oracle)
	if boot > 0 {
		h.RhoCI, h.RhoUndefined = bootstrapRhoStratified(items, k, boot, seed)
	}
	return h
}

// bootstrapRhoStratified resamples requests within each source (so every
// replicate keeps all sources), re-balances, and recomputes ρ.
func bootstrapRhoStratified(items []Item, k, b int, seed uint64) (*certify.Interval, int) {
	bySrc := map[string]map[string][]int{}
	for i, it := range items {
		if bySrc[it.Source] == nil {
			bySrc[it.Source] = map[string][]int{}
		}
		bySrc[it.Source][it.RequestID] = append(bySrc[it.Source][it.RequestID], i)
	}
	srcs := make([]string, 0, len(bySrc))
	ids := map[string][]string{}
	for s, reqs := range bySrc {
		srcs = append(srcs, s)
		for id := range reqs {
			ids[s] = append(ids[s], id)
		}
		sort.Strings(ids[s])
	}
	sort.Strings(srcs)
	r := rand.New(rand.NewPCG(seed, 0xba1))
	var rhos []float64
	undefined := 0
	sample := make([]Item, 0, len(items))
	for range b {
		sample = sample[:0]
		for _, s := range srcs {
			for range ids[s] {
				for _, i := range bySrc[s][ids[s][r.IntN(len(ids[s]))]] {
					sample = append(sample, items[i])
				}
			}
		}
		bs, _, bpc, oracle, _ := point(balance(sample), k)
		if v := rho(bs, bpc, oracle); v != nil {
			rhos = append(rhos, *v)
		} else {
			undefined++
		}
	}
	return quantiles(rhos), undefined
}

func quantiles(rhos []float64) *certify.Interval {
	if len(rhos) == 0 {
		return nil
	}
	sort.Float64s(rhos)
	q := func(p float64) float64 {
		return rhos[int(math.Floor(p*float64(len(rhos)-1)))]
	}
	return &certify.Interval{Lo: q(0.025), Hi: q(0.975)}
}

// Compute builds the headline for one group of items.
func Compute(items []Item, models []string, delta float64, boot int, seed uint64) Headline {
	k := len(models)
	h := Headline{N: len(items), K: k, Accuracy: map[string]float64{}, Assignment: map[string]string{}, Weighting: "natural"}
	reqs := map[string][]int{}
	for i, it := range items {
		reqs[it.RequestID] = append(reqs[it.RequestID], i)
	}
	h.NRequests = len(reqs)
	if len(items) == 0 {
		return h
	}
	bs, bsm, bpc, oracle, assign := point(items, k)
	acc := accuracies(items, k)
	for m, name := range models {
		h.Accuracy[name] = acc[m]
	}
	h.BestSingle, h.BestSingleAcc, h.BestPerClass, h.Oracle = models[bsm], bs, bpc, oracle
	for c, m := range assign {
		h.Assignment[c] = models[m]
	}
	h.Classes = len(assign)

	// Selection-valid intervals: each model at δ/(2K), oracle at δ/2.
	n := len(items)
	var maxL, maxU float64
	for m := range models {
		kc := int(math.Round(acc[m] * float64(n)))
		iv := certify.ClopperPearson(kc, n, delta/(2*float64(k)))
		maxL, maxU = math.Max(maxL, iv.Lo), math.Max(maxU, iv.Hi)
	}
	h.BestSingleCI = certify.Interval{Lo: maxL, Hi: maxU}
	h.OracleCI = certify.ClopperPearson(int(math.Round(oracle*float64(n))), n, delta/2)
	h.GapCI = certify.Interval{Lo: math.Max(0, h.OracleCI.Lo-maxU), Hi: math.Min(1, h.OracleCI.Hi-maxL)}

	// Best-per-class: Bonferroni over classes × models, then the
	// n-weighted sum of per-class [max L, max U].
	byClass := map[string][]Item{}
	for _, it := range items {
		byClass[it.Class] = append(byClass[it.Class], it)
	}
	alpha := delta / (2 * float64(k) * float64(len(byClass)))
	var lo, hi float64
	for _, its := range byClass {
		a := accuracies(its, k)
		var cl, cu float64
		for m := range models {
			iv := certify.ClopperPearson(int(math.Round(a[m]*float64(len(its)))), len(its), alpha)
			cl, cu = math.Max(cl, iv.Lo), math.Max(cu, iv.Hi)
		}
		lo += cl * float64(len(its))
		hi += cu * float64(len(its))
	}
	h.BestPerClassCI = certify.Interval{Lo: lo / float64(n), Hi: hi / float64(n)}

	h.Rho = rho(bs, bpc, oracle)
	if boot > 0 {
		h.RhoCI, h.RhoUndefined = bootstrapRho(items, reqs, k, boot, seed)
	}
	return h
}

// bootstrapRho resamples requests (leaves of one request stay together)
// and recomputes best-single, best-per-class (re-selected) and oracle
// jointly, so numerator and denominator are paired.
func bootstrapRho(items []Item, reqs map[string][]int, k, b int, seed uint64) (*certify.Interval, int) {
	ids := make([]string, 0, len(reqs))
	for id := range reqs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	r := rand.New(rand.NewPCG(seed, 0xb007))
	var rhos []float64
	undefined := 0
	sample := make([]Item, 0, len(items))
	for range b {
		sample = sample[:0]
		for range ids {
			for _, i := range reqs[ids[r.IntN(len(ids))]] {
				sample = append(sample, items[i])
			}
		}
		bs, _, bpc, oracle, _ := point(sample, k)
		if v := rho(bs, bpc, oracle); v != nil {
			rhos = append(rhos, *v)
		} else {
			undefined++
		}
	}
	return quantiles(rhos), undefined
}
