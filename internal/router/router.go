// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

// Package router is the per-leaf routing decision (PROJECT.md §3 "Class",
// "Quality constraint"; §4 E2, E3). Interfaces only: nothing here is built
// until E0 clears its gate.
//
// A class table row holds the default model (cheapest whose certified error
// upper bound clears the class threshold), that bound, the threshold, and
// the best available verifier rung. Where the default does not clear but a
// rung-1/2 verifier exists, the router cascades: run the default, verify,
// escalate with prompt + failed attempt + verifier output.
package router

import "context"

// Rung is a verifier rung (PROJECT.md §3 "Verifier rungs").
type Rung int

const (
	RungExecutable  Rung = 1 // tests, exact answer, schema + run
	RungPartial     Rung = 2 // compiles, parses, validates, citations resolve
	RungCalibrated  Rung = 3 // probe, k-sample consistency, judge (isotonic-calibrated per class)
	RungCertificate Rung = 4 // class certificate only
)

// ClassEntry is one row of the certified class table.
type ClassEntry struct {
	Class        string
	DefaultModel string
	CertifiedErr float64 // upper bound at the table's δ
	Threshold    float64 // supplied by the workload owner
	Rung         Rung
	Escalation   []string // models to escalate to, cheapest first
}

// Table looks up the class table.
type Table interface {
	Lookup(class string) (ClassEntry, bool)
}

// Verdict is a verifier's result on one attempt.
type Verdict struct {
	Pass   bool
	Detail string // fed back to the escalation model
}

// Verifier checks one leaf output at a given rung.
type Verifier interface {
	Rung() Rung
	Verify(ctx context.Context, prompt, output string) (Verdict, error)
}

// Decision is what the router does with a leaf.
type Decision struct {
	Model    string
	Cascade  bool     // run Model, verify, escalate on failure
	Escalate []string // escalation order when Cascade
}

// Router decides per leaf.
type Router interface {
	Route(ctx context.Context, class string) (Decision, error)
}
