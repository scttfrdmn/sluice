// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

package spend

import "testing"

func TestBudget(t *testing.T) {
	b := New(10, 2)
	if b.Exhausted() {
		t.Fatal("exhausted at start")
	}
	costs := make([]float64, 100)
	for i := range costs {
		costs[i] = 1.5
	}
	// 8 left: 1.5 each fits 5; min 6 fits none.
	if k, _ := b.Reserve(costs, 6); k != 0 {
		t.Fatalf("reserved %d below min", k)
	}
	b = New(10, 2)
	if k, tot := b.Reserve(costs, 2); k != 5 || tot != 7.5 {
		t.Fatalf("reserved %d ($%v), want 5", k, tot)
	}
	b.Settle(7.5, 1)
	if b.Exhausted() {
		t.Fatal("exhausted after settle")
	}
	b.Add(7)
	if !b.Exhausted() || !b.Stopped() {
		t.Fatal("not exhausted at cap")
	}
	if err := Require(-1); err == nil {
		t.Fatal("unset cap accepted")
	}
}
