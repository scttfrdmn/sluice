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
	Correct   []bool    // indexed by model
	Cost      []float64 // mean cost of one call (mean over the two draws)
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
}

func accuracies(items []Item, k int) []float64 {
	acc := make([]float64, k)
	if len(items) == 0 {
		return acc
	}
	for _, it := range items {
		for m, c := range it.Correct {
			if c {
				acc[m]++
			}
		}
	}
	for m := range acc {
		acc[m] /= float64(len(items))
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
	for _, it := range items {
		byClass[it.Class] = append(byClass[it.Class], it)
		for _, c := range it.Correct {
			if c {
				oracle++
				break
			}
		}
	}
	oracle /= float64(len(items))
	assign = map[string]int{}
	for c, its := range byClass {
		a := accuracies(its, k)
		m := argmax(a)
		assign[c] = m
		bpc += a[m] * float64(len(its))
	}
	bpc /= float64(len(items))
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

// Compute builds the headline for one group of items.
func Compute(items []Item, models []string, delta float64, boot int, seed uint64) Headline {
	k := len(models)
	h := Headline{N: len(items), K: k, Accuracy: map[string]float64{}, Assignment: map[string]string{}}
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
	if len(rhos) == 0 {
		return nil, undefined
	}
	sort.Float64s(rhos)
	q := func(p float64) float64 {
		i := int(math.Floor(p * float64(len(rhos)-1)))
		return rhos[i]
	}
	return &certify.Interval{Lo: q(0.025), Hi: q(0.975)}, undefined
}
