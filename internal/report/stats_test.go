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
