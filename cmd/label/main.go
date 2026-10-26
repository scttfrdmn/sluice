// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

// Command label generates per-leaf references (tests, numeric answers) with
// the strong model, labels every matrix cell by the best available verifier
// rung, and writes runs/<id>/refs.jsonl, labels.jsonl and
// matrix_labeled.parquet (CC_PROMPT.md cmd/label). Resumable.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"github.com/parquet-go/parquet-go"
	"golang.org/x/sync/errgroup"

	"github.com/scttfrdmn/sluice/internal/cache"
	"github.com/scttfrdmn/sluice/internal/config"
	"github.com/scttfrdmn/sluice/internal/corpus"
	"github.com/scttfrdmn/sluice/internal/jsonl"
	"github.com/scttfrdmn/sluice/internal/label"
	"github.com/scttfrdmn/sluice/internal/llm"
	"github.com/scttfrdmn/sluice/internal/matrix"
	"github.com/scttfrdmn/sluice/internal/rundir"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "label:", err)
		os.Exit(1)
	}
}

// LabeledCell is a matrix row joined with its label.
type LabeledCell struct {
	matrix.Cell
	Label          string `parquet:"label" json:"label"` // correct | incorrect | unlabeled
	Rung           int32  `parquet:"rung" json:"rung"`
	LabelMethod    string `parquet:"label_method" json:"label_method"`
	JudgeAgreement *bool  `parquet:"judge_agreement,optional" json:"judge_agreement,omitempty"`
	Judges         string `parquet:"judges" json:"judges"`
}

func run() error {
	var (
		cfgPath  = flag.String("config", "config/sluice.yaml", "config file")
		runsRoot = flag.String("runs", "runs", "runs root")
		runID    = flag.String("run", "", "run id (required)")
	)
	flag.Parse()
	if *runID == "" {
		return errors.New("-run is required")
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	dir, err := rundir.Open(*runsRoot, *runID)
	if err != nil {
		return err
	}
	ctx := context.Background()
	client, err := llm.New(ctx, cfg)
	if err != nil {
		return err
	}
	sb, err := label.NewSandbox(cfg)
	if err != nil {
		return err
	}
	lb, err := label.New(cfg, client, cache.New(dir.Cache()), sb)
	if err != nil {
		return err
	}
	leaves, err := jsonl.Read[corpus.Leaf](dir.Leaves("v0"))
	if err != nil {
		return err
	}
	leafByID := make(map[string]corpus.Leaf, len(leaves))
	for _, l := range leaves {
		leafByID[l.LeafID] = l
	}
	workers := max(1, cfg.Label.Workers)

	// 1. References, once per leaf.
	refs := map[string]label.Ref{}
	if err := jsonl.Each(dir.Refs(), func(r label.Ref) error { refs[r.LeafID] = r; return nil }); err != nil {
		return err
	}
	refApp, err := jsonl.OpenAppender(dir.Refs())
	if err != nil {
		return err
	}
	var mu sync.Mutex
	var refErr atomic.Int64
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(workers)
	for _, l := range leaves {
		k := label.Kind(l)
		if (k != "code" && k != "numeric") || refs[l.LeafID].Status != "" {
			continue
		}
		g.Go(func() error {
			r, err := lb.MakeRef(gctx, l)
			if err != nil {
				refErr.Add(1)
				fmt.Fprintf(os.Stderr, "label: ref %s: %v\n", l.LeafID, err)
				return nil
			}
			mu.Lock()
			refs[r.LeafID] = r
			mu.Unlock()
			return refApp.Append(r)
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}
	refApp.Close()
	if n := refErr.Load(); n > 0 {
		return fmt.Errorf("%d references failed; rerun to retry", n)
	}

	// 2. Labels, once per cell.
	cells, err := matrix.ReadAll(dir.MatrixDir())
	if err != nil {
		return err
	}
	labels := map[string]label.Label{}
	if err := jsonl.Each(dir.Labels(), func(l label.Label) error { labels[l.Key()] = l; return nil }); err != nil {
		return err
	}
	var todo []matrix.Cell
	for _, c := range cells {
		if l, ok := labels[c.Key()]; ok && l.Correct != nil {
			continue
		}
		if _, ok := leafByID[c.LeafID]; ok {
			todo = append(todo, c)
		}
	}
	labApp, err := jsonl.OpenAppender(dir.Labels())
	if err != nil {
		return err
	}
	var nNew, nErr atomic.Int64
	g2, gctx2 := errgroup.WithContext(ctx)
	g2.SetLimit(workers)
	for _, c := range todo {
		g2.Go(func() error {
			l, err := lb.LabelCell(gctx2, leafByID[c.LeafID], c, refs[c.LeafID])
			if err != nil {
				nErr.Add(1)
				fmt.Fprintf(os.Stderr, "label: %s: %v\n", c.Key(), err)
				return nil
			}
			nNew.Add(1)
			mu.Lock()
			labels[l.Key()] = l
			mu.Unlock()
			return labApp.Append(l)
		})
	}
	if err := g2.Wait(); err != nil {
		return err
	}
	labApp.Close()

	// 3. Joined parquet.
	var joined []LabeledCell
	var unlabeled int
	for _, c := range cells {
		lc := LabeledCell{Cell: c, Label: "unlabeled"}
		if l, ok := labels[c.Key()]; ok {
			lc.Rung, lc.LabelMethod, lc.JudgeAgreement = int32(l.Rung), l.Method, l.JudgeAgreement
			for i, j := range l.Judges {
				if i > 0 {
					lc.Judges += ","
				}
				lc.Judges += j
			}
			if l.Correct != nil {
				lc.Label = map[bool]string{true: "correct", false: "incorrect"}[*l.Correct]
			}
		}
		if lc.Label == "unlabeled" {
			unlabeled++
		}
		joined = append(joined, lc)
	}
	if err := parquet.WriteFile(dir.LabeledMatrix(), joined); err != nil {
		return err
	}
	fmt.Printf("label: %d refs, %d new labels, %d errors, %d of %d cells unlabeled\n", len(refs), nNew.Load(), nErr.Load(), unlabeled, len(cells))
	if nErr.Load() > 0 {
		return fmt.Errorf("%d cells failed to label; rerun to retry", nErr.Load())
	}
	return nil
}
