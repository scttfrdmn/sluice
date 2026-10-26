// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

package dag

import "testing"

func TestCriticalPath(t *testing.T) {
	g := Graph{Nodes: []Node{
		{ID: 0, Latency: 100, CostUSD: 1},
		{ID: 1, Latency: 50, CostUSD: 2},
		{ID: 2, Needs: []int{0, 1}, Latency: 30, CostUSD: 3},
	}}
	cp, err := g.CriticalPath()
	if err != nil || cp != 130 {
		t.Fatalf("critical path = %v, %v; want 130", cp, err)
	}
	if g.Cost() != 6 {
		t.Fatalf("cost = %v", g.Cost())
	}
	g.Nodes[0].Needs = []int{2}
	if _, err := g.CriticalPath(); err != ErrCycle {
		t.Fatalf("want ErrCycle, got %v", err)
	}
}
