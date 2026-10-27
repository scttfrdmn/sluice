// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

// Command matrix calls every pool model on every leaf, twice, and writes
// the outcome matrix as parquet part files under runs/<id>/matrix/
// (CC_PROMPT.md cmd/matrix). Resumable: existing cells are skipped.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/scttfrdmn/sluice/internal/config"
	"github.com/scttfrdmn/sluice/internal/corpus"
	"github.com/scttfrdmn/sluice/internal/jsonl"
	"github.com/scttfrdmn/sluice/internal/llm"
	"github.com/scttfrdmn/sluice/internal/matrix"
	"github.com/scttfrdmn/sluice/internal/rundir"
	"github.com/scttfrdmn/sluice/internal/spend"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "matrix:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		cfgPath  = flag.String("config", "config/sluice.yaml", "config file")
		runsRoot = flag.String("runs", "runs", "runs root")
		runID    = flag.String("run", "", "run id (required)")
		split    = flag.String("split", "", "only leaves from this split (default: all)")
		limit    = flag.Int("limit", 0, "only the first N leaves (0 = all), for smoke tests")
		maxSpend = spend.Flag(nil)
	)
	flag.Parse()
	if *runID == "" {
		return errors.New("-run is required")
	}
	if err := spend.Require(*maxSpend); err != nil {
		return err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	if len(cfg.LocalModels) > 0 {
		return llm.ErrLocalLaneDisabled
	}
	dir, err := rundir.Open(*runsRoot, *runID)
	if err != nil {
		return err
	}
	leaves, err := jsonl.Read[corpus.Leaf](dir.Leaves("v0"))
	if err != nil {
		return err
	}
	if *split != "" {
		var f []corpus.Leaf
		for _, l := range leaves {
			if l.Split == *split {
				f = append(f, l)
			}
		}
		leaves = f
	}
	if *limit > 0 && *limit < len(leaves) {
		leaves = leaves[:*limit]
	}
	ctx := context.Background()
	be, err := llm.New(ctx, cfg)
	if err != nil {
		return err
	}
	return matrix.Run(ctx, cfg, be, leaves, matrix.Options{Dir: dir.MatrixDir(), ErrorsLog: dir.MatrixErrors(), Log: os.Stdout, MaxSpend: *maxSpend})
}
