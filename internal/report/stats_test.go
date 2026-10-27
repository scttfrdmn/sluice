// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

package report

import (
	"fmt"
	"math"
	"testing"
)

// Two classes, two models: model A is right on class x, model B on class y.
// Best single = 0.5, best-per-class = 1.0, oracle = 1.0, so ρ = 1.
func TestRhoPerfectSpecialists(t *testing.T) {
	var items []Item
	for i := 0; i < 40; i++ {
		c := "x"
		corr := []bool{true, false}
		if i%2 == 1 {
			c, corr = "y", []bool{false, true}
		}
		items = append(items, Item{LeafID: fmt.Sprint(i), RequestID: fmt.Sprint(i), Class: c, Correct: corr, Cost: []float64{1, 1}})
	}
	h := Compute(items, []string{"A", "B"}, 0.05, 200, 1)
	if h.BestSingleAcc != 0.5 || h.BestPerClass != 1 || h.Oracle != 1 {
		t.Fatalf("got bs %v bpc %v oracle %v", h.BestSingleAcc, h.BestPerClass, h.Oracle)
	}
	if h.Rho == nil || math.Abs(*h.Rho-1) > 1e-12 {
		t.Fatalf("rho = %v", h.Rho)
	}
	if h.GapCI.Lo < 0 || h.GapCI.Hi > 1 || h.GapCI.Lo > h.GapCI.Hi {
		t.Fatalf("gap interval %v", h.GapCI)
	}
}

// One class: best-per-class equals best single, so ρ = 0 when the oracle is higher.
func TestRhoOneClass(t *testing.T) {
	var items []Item
	for i := 0; i < 20; i++ {
		items = append(items, Item{LeafID: fmt.Sprint(i), RequestID: fmt.Sprint(i), Class: "x", Correct: []bool{i%2 == 0, i%2 == 1}, Cost: []float64{1, 1}})
	}
	h := Compute(items, []string{"A", "B"}, 0.05, 0, 0)
	if h.Rho == nil || *h.Rho != 0 {
		t.Fatalf("rho = %v", h.Rho)
	}
}

// Source-balanced weighting: source s1 (30 leaves) has specialists, source
// s2 (10 leaves) is one class where model A is always right. Natural and
// balanced numbers must differ, and equal-size sources must agree.
func TestBalanced(t *testing.T) {
	var items []Item
	for i := 0; i < 30; i++ {
		c, corr := "x", []bool{true, false}
		if i%2 == 1 {
			c, corr = "y", []bool{false, true}
		}
		items = append(items, Item{LeafID: fmt.Sprint(i), RequestID: fmt.Sprint(i), Class: c, Source: "s1", Correct: corr, Cost: []float64{1, 1}})
	}
	for i := 30; i < 40; i++ {
		items = append(items, Item{LeafID: fmt.Sprint(i), RequestID: fmt.Sprint(i), Class: "z", Source: "s2", Correct: []bool{true, false}, Cost: []float64{1, 1}})
	}
	models := []string{"A", "B"}
	nat := Compute(items, models, 0.05, 0, 0)
	bal := ComputeBalanced(items, models, 200, 3)
	// Natural: A = (15+10)/40; balanced: A = 0.5·0.5 + 0.5·1 = 0.75.
	if math.Abs(nat.BestSingleAcc-25.0/40) > 1e-12 || math.Abs(bal.BestSingleAcc-0.75) > 1e-12 {
		t.Fatalf("best single natural %v balanced %v", nat.BestSingleAcc, bal.BestSingleAcc)
	}
	if math.Abs(bal.Oracle-1) > 1e-9 || math.Abs(bal.BestPerClass-1) > 1e-9 || bal.Rho == nil || math.Abs(*bal.Rho-1) > 1e-9 {
		t.Fatalf("balanced oracle %v bpc %v rho %v", bal.Oracle, bal.BestPerClass, bal.Rho)
	}
	if bal.RhoCI == nil || bal.Weighting != "source-balanced" {
		t.Fatalf("balanced bootstrap missing: %+v", bal)
	}
	// Equal-size sources: balanced equals natural.
	eq := append(items[:0:0], items[:20]...)
	for i := 0; i < 20; i++ {
		it := items[30+i%10]
		it.LeafID, it.RequestID = fmt.Sprint(100+i), fmt.Sprint(100+i)
		eq = append(eq, it)
	}
	a, b := Compute(eq, models, 0.05, 0, 0), ComputeBalanced(eq, models, 0, 0)
	if math.Abs(a.BestSingleAcc-b.BestSingleAcc) > 1e-12 || math.Abs(a.BestPerClass-b.BestPerClass) > 1e-12 || math.Abs(a.Oracle-b.Oracle) > 1e-12 {
		t.Fatalf("equal sources: natural %+v balanced %+v", a, b)
	}
}
