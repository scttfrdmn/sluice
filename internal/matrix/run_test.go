// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

package matrix

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"testing"

	"github.com/scttfrdmn/sluice/internal/config"
	"github.com/scttfrdmn/sluice/internal/corpus"
	"github.com/scttfrdmn/sluice/internal/llm"
	"github.com/scttfrdmn/sluice/internal/spend"
)

// A tiny cap stops the pass with ErrCap and leaves cells pending; a rerun
// with a larger cap finishes them, and the cap is cumulative.
func TestMaxSpend(t *testing.T) {
	cfg, err := config.Load("../../testdata/sluice.test.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Matrix.BatchThreshold = 1 << 30 // on demand only
	var leaves []corpus.Leaf
	for i := range 40 {
		leaves = append(leaves, corpus.Leaf{LeafID: fmt.Sprintf("l%d", i), RequestID: fmt.Sprint(i), Class: "converse/general", ModelText: "say hello in a friendly way please"})
	}
	be, err := llm.New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	o := Options{Dir: filepath.Join(dir, "m"), ErrorsLog: filepath.Join(dir, "e.jsonl"), Log: io.Discard, MaxSpend: 1e-9}
	err = Run(context.Background(), cfg, be, leaves, o)
	if !errors.Is(err, spend.ErrCap) {
		t.Fatalf("want ErrCap, got %v", err)
	}
	cells, _ := ReadAll(o.Dir)
	total := len(leaves) * len(cfg.Models) * cfg.Matrix.Draws
	if len(cells) == 0 || len(cells) >= total {
		t.Fatalf("%d of %d cells after a tiny cap", len(cells), total)
	}
	var spent float64
	for _, c := range cells {
		spent += c.CostUSD
	}
	// Cumulative: a cap below what is already spent runs nothing more.
	o.MaxSpend = spent / 2
	if err := Run(context.Background(), cfg, be, leaves, o); !errors.Is(err, spend.ErrCap) {
		t.Fatalf("want ErrCap on a cap below prior spend, got %v", err)
	}
	if again, _ := ReadAll(o.Dir); len(again) != len(cells) {
		t.Fatalf("cells grew from %d to %d under an exhausted cap", len(cells), len(again))
	}
	o.MaxSpend = 1e6
	if err := Run(context.Background(), cfg, be, leaves, o); err != nil {
		t.Fatal(err)
	}
	if done, _ := ReadAll(o.Dir); len(done) != total {
		t.Fatalf("%d of %d cells after resume", len(done), total)
	}
}

// Batch jobs reserve their worst case before submission: under a cap
// smaller than one job's worst case, no batch job is submitted.
func TestMaxSpendBatchReservation(t *testing.T) {
	cfg, err := config.Load("../../testdata/sluice.test.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Matrix.BatchThreshold, cfg.Matrix.BatchMinRecords, cfg.Matrix.OnDemandSubsample = 1, 10, 0
	var leaves []corpus.Leaf
	for i := range 20 {
		leaves = append(leaves, corpus.Leaf{LeafID: fmt.Sprintf("b%d", i), RequestID: fmt.Sprint(i), Class: "converse/general", ModelText: "hello"})
	}
	be, err := llm.New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	m := cfg.Models[0]
	one := worstCase(cfg, job{leaf: leaves[0], model: m})
	dir := t.TempDir()
	o := Options{Dir: filepath.Join(dir, "m"), ErrorsLog: filepath.Join(dir, "e.jsonl"), Log: io.Discard, MaxSpend: one * 5}
	if err := Run(context.Background(), cfg, be, leaves, o); !errors.Is(err, spend.ErrCap) {
		t.Fatalf("want ErrCap, got %v", err)
	}
	if cells, _ := ReadAll(o.Dir); len(cells) != 0 {
		t.Fatalf("%d cells written under a cap below one batch job's worst case", len(cells))
	}
	o.MaxSpend = 1e6
	if err := Run(context.Background(), cfg, be, leaves, o); err != nil {
		t.Fatal(err)
	}
}
