// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

// Package spend enforces the per-stage dollar cap (-max-spend). Costs come
// from measured token counts and the price table as calls complete. The cap
// is per stage and cumulative across resumed passes: the stage's existing
// cost counts against it.
//
// On-demand work checks Exhausted before each dispatch, so the overshoot is
// bounded by the calls already in flight. Batch jobs, whose cost is known
// only when they finish, reserve their worst-case cost before submission
// and settle to the measured cost afterwards.
package spend

import (
	"errors"
	"flag"
	"fmt"
	"sync"
)

// ErrCap is returned when a stage stops because the cap was reached. The
// stage is resumable: rerun with a higher cap to continue.
var ErrCap = errors.New("spend cap reached")

// Flag registers -max-spend on fs (or the default flag set when fs is nil).
// The default, -1, means unset; Require rejects it.
func Flag(fs *flag.FlagSet) *float64 {
	if fs == nil {
		fs = flag.CommandLine
	}
	return fs.Float64("max-spend", -1, "stage spend cap in USD (required; set per stage on the spend-approval issue)")
}

// Require returns an error if the flag was not set.
func Require(v float64) error {
	if v < 0 {
		return errors.New("-max-spend <usd> is required: nothing calls a model without a cap (set per stage on issue #7)")
	}
	return nil
}

// Budget tracks one stage's spend against its cap.
type Budget struct {
	mu       sync.Mutex
	cap      float64
	prior    float64 // cost already recorded by earlier passes
	spent    float64 // this pass, measured
	reserved float64 // batch worst-case reservations outstanding
	stopped  bool
}

// New starts a budget with cap and the stage's prior cost.
func New(cap, prior float64) *Budget { return &Budget{cap: cap, prior: prior} }

// Add records a measured cost.
func (b *Budget) Add(usd float64) {
	b.mu.Lock()
	b.spent += usd
	b.mu.Unlock()
}

// Exhausted reports whether committed spend (prior + measured + reserved)
// has reached the cap, and latches the stop.
func (b *Budget) Exhausted() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.prior+b.spent+b.reserved >= b.cap {
		b.stopped = true
	}
	return b.stopped
}

// Reserve reserves worst-case costs in order while they fit under the cap
// and returns how many fit and their total. If fewer than minUnits fit it
// reserves nothing and latches the stop.
func (b *Budget) Reserve(costs []float64, minUnits int) (int, float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	room := b.cap - b.prior - b.spent - b.reserved
	k, total := 0, 0.0
	for _, c := range costs {
		if total+c > room {
			break
		}
		total += c
		k++
	}
	if k < max(1, minUnits) {
		b.stopped = true
		return 0, 0
	}
	b.reserved += total
	return k, total
}

// Settle releases a reservation and records the measured cost instead.
func (b *Budget) Settle(reserved, actual float64) {
	b.mu.Lock()
	b.reserved -= reserved
	b.spent += actual
	b.mu.Unlock()
}

// Stopped reports whether any work was withheld because of the cap.
func (b *Budget) Stopped() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.stopped
}

// Summary is the line every capped stage prints at the end.
func (b *Budget) Summary(stage string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := fmt.Sprintf("%s: spend $%.4f this pass, $%.4f stage total (cap $%.2f)", stage, b.spent, b.prior+b.spent, b.cap)
	if b.stopped {
		s += "; STOPPED at the cap, rerun with a higher -max-spend to continue"
	}
	return s
}
