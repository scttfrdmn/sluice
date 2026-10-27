// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

// Command tag segments every harvested request into leaves with the strong
// model (CC_PROMPT.md cmd/tag) and writes runs/<id>/tags.jsonl and
// runs/<id>/leaves.jsonl. Resumable: requests already in tags.jsonl are
// skipped, and every tagger output is cached by SHA-256 of its input. If
// runs/<id>/subsample.json exists only those requests are tagged (-all
// overrides).
//
// With -reclass, it instead re-assigns classes to the existing leaves under
// the taxonomy in -config (one taxonomy iteration, PROJECT.md §4 E0) and
// writes runs/<id>/leaves.<version>.jsonl with unchanged leaf IDs.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"sync/atomic"

	"golang.org/x/sync/errgroup"

	"github.com/scttfrdmn/sluice/internal/cache"
	"github.com/scttfrdmn/sluice/internal/config"
	"github.com/scttfrdmn/sluice/internal/corpus"
	"github.com/scttfrdmn/sluice/internal/jsonl"
	"github.com/scttfrdmn/sluice/internal/llm"
	"github.com/scttfrdmn/sluice/internal/rundir"
	"github.com/scttfrdmn/sluice/internal/spend"
	"github.com/scttfrdmn/sluice/internal/tagger"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "tag:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		cfgPath  = flag.String("config", "config/sluice.yaml", "config file")
		runsRoot = flag.String("runs", "runs", "runs root")
		runID    = flag.String("run", "", "run id (required)")
		reclass  = flag.String("reclass", "", "re-tag the leaves of this taxonomy version under the config's taxonomy")
		all      = flag.Bool("all", false, "tag every record, ignoring the E0 subsample (E1 tagger training)")
		which    = flag.String("subsample", "calibration", "E0 subsample to tag: calibration (subsample.json) or test (subsample_test.json)")
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
	dir, err := rundir.Open(*runsRoot, *runID)
	if err != nil {
		return err
	}
	ctx := context.Background()
	client, err := llm.New(ctx, cfg)
	if err != nil {
		return err
	}
	c := cache.New(dir.Cache())
	strong, _ := cfg.Model(cfg.Strong)
	workers := max(1, strong.Concurrency)

	if *reclass != "" {
		return runReclass(ctx, cfg, dir, client, c, *reclass, workers, *maxSpend)
	}

	recs, err := jsonl.Read[corpus.Record](dir.Records())
	if err != nil {
		return err
	}
	// Leaves are built from every tagged request, so the record map covers
	// the whole run; recs is what this pass tags.
	byID := make(map[string]corpus.Record, len(recs))
	for _, r := range recs {
		byID[r.ID] = r
	}
	if !*all {
		var path string
		switch *which {
		case "calibration":
			path = dir.Subsample()
		case "test":
			path = dir.SubsampleTest()
		default:
			return fmt.Errorf("-subsample must be calibration or test")
		}
		ids, ss, err := corpus.ReadSubsample(path)
		if err != nil {
			return err
		}
		if ids == nil && *which == "test" {
			return fmt.Errorf("%s missing; run harvest subsample first", path)
		}
		if ids != nil {
			recs = corpus.FilterSubsample(recs, ids)
			fmt.Printf("tag: E0 subsample: %d requests (split %s, %d per stratum)\n", len(recs), ss.Split, ss.PerStratum)
		}
	}
	inScope := make(map[string]bool, len(recs))
	for _, r := range recs {
		inScope[r.ID] = true
	}
	done := map[string]bool{}
	var prior float64 // earlier passes' tag cost for these requests counts against the cap
	if err := jsonl.Each(dir.Tags(), func(t corpus.TagResult) error {
		done[t.RequestID] = true
		if inScope[t.RequestID] {
			prior += t.CostUSD
		}
		return nil
	}); err != nil {
		return err
	}
	budget := spend.New(*maxSpend, prior)
	tg, err := tagger.NewStrongModel(cfg, client, c)
	if err != nil {
		return err
	}
	tg.Spend = budget
	app, err := jsonl.OpenAppender(dir.Tags())
	if err != nil {
		return err
	}
	var nOK, nFailed, nErr atomic.Int64
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(workers)
	for _, r := range recs {
		if done[r.ID] {
			continue
		}
		if budget.Exhausted() {
			break
		}
		g.Go(func() error {
			res, err := tg.Tag(gctx, r)
			if err != nil {
				nErr.Add(1)
				fmt.Fprintf(os.Stderr, "tag: %s: %v (will retry on rerun)\n", r.ID, err)
				return nil
			}
			if res.Status == corpus.TagOK {
				nOK.Add(1)
			} else {
				nFailed.Add(1)
			}
			return app.Append(res)
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}
	if err := app.Close(); err != nil {
		return err
	}

	tags, err := jsonl.Read[corpus.TagResult](dir.Tags())
	if err != nil {
		return err
	}
	sort.Slice(tags, func(i, j int) bool { return tags[i].RequestID < tags[j].RequestID })
	leaves := tagger.Leaves(byID, tags)
	if err := jsonl.Write(dir.Leaves(cfg.Taxonomy.Version), leaves); err != nil {
		return err
	}
	var failed, total int
	for _, t := range tags {
		total++
		if t.Status != corpus.TagOK {
			failed++
		}
	}
	fmt.Printf("tagged this pass: %d ok, %d tag_failed, %d transport errors\n", nOK.Load(), nFailed.Load(), nErr.Load())
	fmt.Println(budget.Summary("tag"))
	fmt.Printf("tags.jsonl: %d requests (%d tag_failed); leaves.jsonl: %d leaves\n", total, failed, len(leaves))
	if nErr.Load() > 0 {
		return fmt.Errorf("%d requests hit transport errors; rerun to retry them", nErr.Load())
	}
	if budget.Stopped() {
		return fmt.Errorf("tag: %w", spend.ErrCap)
	}
	return nil
}

func runReclass(ctx context.Context, cfg *config.Config, dir rundir.Dir, client llm.Client, c *cache.Cache, from string, workers int, maxSpend float64) error {
	if from == cfg.Taxonomy.Version {
		return fmt.Errorf("-reclass %s: config taxonomy is the same version; bump taxonomy.version", from)
	}
	leaves, err := jsonl.Read[corpus.Leaf](dir.Leaves(from))
	if err != nil {
		return err
	}
	rc, err := tagger.NewReclassifier(cfg, client, c)
	if err != nil {
		return err
	}
	// Reclass outputs are cached and not costed per leaf, so the cap covers
	// this pass only; a rerun pays only for leaves not yet cached.
	budget := spend.New(maxSpend, 0)
	rc.Spend = budget
	out := make([]corpus.Leaf, len(leaves))
	ok := make([]bool, len(leaves))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(workers)
	for i, l := range leaves {
		if budget.Exhausted() {
			break
		}
		g.Go(func() error {
			nl, good, err := rc.Reclass(gctx, l)
			out[i], ok[i] = nl, good
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}
	var kept []corpus.Leaf
	for i := range out {
		if ok[i] {
			kept = append(kept, out[i])
		}
	}
	fmt.Println(budget.Summary("reclass"))
	if budget.Stopped() {
		return fmt.Errorf("reclass: %w; leaves.%s.jsonl not written", spend.ErrCap, cfg.Taxonomy.Version)
	}
	if err := jsonl.Write(dir.Leaves(cfg.Taxonomy.Version), kept); err != nil {
		return err
	}
	fmt.Printf("reclass %s -> %s: %d of %d leaves reclassified\n", from, cfg.Taxonomy.Version, len(kept), len(leaves))
	return nil
}
