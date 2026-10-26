// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

// Package dag is the leaf graph of one request and its scheduling model
// (PROJECT.md §3 "Scheduler"; §4 E2). Cost = Σ nodes; latency = the critical
// path. Joins are nodes with their own class and verifier.
//
// E2's runner is meant to sit on quarry's decomposition executor
// (github.com/scttfrdmn/quarry) and ember's batched scheduling model; until
// then this package holds only the graph and critical-path arithmetic, which
// E0 does not use.
package dag

import (
	"errors"
	"fmt"
)

// Node is one leaf (or join) with its estimated cost and latency.
type Node struct {
	ID      int
	Class   string
	Needs   []int
	CostUSD float64
	Latency float64 // milliseconds
}

// Graph is a request's leaves.
type Graph struct{ Nodes []Node }

// ErrCycle is returned for a graph whose needs edges form a cycle.
var ErrCycle = errors.New("dag: needs edges form a cycle")

// Order returns node indices in dependency order.
func (g Graph) Order() ([]int, error) {
	idx := map[int]int{}
	for i, n := range g.Nodes {
		idx[n.ID] = i
	}
	state := make([]int, len(g.Nodes)) // 0 new, 1 visiting, 2 done
	var out []int
	var visit func(i int) error
	visit = func(i int) error {
		switch state[i] {
		case 1:
			return ErrCycle
		case 2:
			return nil
		}
		state[i] = 1
		for _, d := range g.Nodes[i].Needs {
			j, ok := idx[d]
			if !ok {
				return fmt.Errorf("dag: node %d needs unknown node %d", g.Nodes[i].ID, d)
			}
			if err := visit(j); err != nil {
				return err
			}
		}
		state[i] = 2
		out = append(out, i)
		return nil
	}
	for i := range g.Nodes {
		if err := visit(i); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Cost is the sum of node costs.
func (g Graph) Cost() float64 {
	var s float64
	for _, n := range g.Nodes {
		s += n.CostUSD
	}
	return s
}

// CriticalPath is the longest latency path through the graph.
func (g Graph) CriticalPath() (float64, error) {
	order, err := g.Order()
	if err != nil {
		return 0, err
	}
	idx := map[int]int{}
	for i, n := range g.Nodes {
		idx[n.ID] = i
	}
	finish := make([]float64, len(g.Nodes))
	var best float64
	for _, i := range order {
		var start float64
		for _, d := range g.Nodes[i].Needs {
			start = max(start, finish[idx[d]])
		}
		finish[i] = start + g.Nodes[i].Latency
		best = max(best, finish[i])
	}
	return best, nil
}
