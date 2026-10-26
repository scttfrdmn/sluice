// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

// Command report writes runs/<id>/report.md, report.json and plots for E0
// (CC_PROMPT.md cmd/report). It prints the decision line with its numbers
// and never declares the gate passed or failed.
//
// The default split is calibration. The test split can be opened once per
// run (-split test); a marker file prevents a second opening.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/scttfrdmn/sluice/internal/config"
	"github.com/scttfrdmn/sluice/internal/corpus"
	"github.com/scttfrdmn/sluice/internal/jsonl"
	"github.com/scttfrdmn/sluice/internal/label"
	"github.com/scttfrdmn/sluice/internal/matrix"
	"github.com/scttfrdmn/sluice/internal/report"
	"github.com/scttfrdmn/sluice/internal/rundir"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "report:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		cfgPath   = flag.String("config", "config/sluice.yaml", "config file")
		runsRoot  = flag.String("runs", "runs", "runs root")
		runID     = flag.String("run", "", "run id (required)")
		split     = flag.String("split", "calibration", "calibration | test | all")
		taxonomy  = flag.String("taxonomy", "", "leaves version to report (default: config taxonomy.version)")
		iteration = flag.Int("iteration", -1, "taxonomy iteration number (default: 0 for v0, else 1)")
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
	tv := *taxonomy
	if tv == "" {
		tv = cfg.Taxonomy.Version
	}
	it := *iteration
	if it < 0 {
		it = 0
		if tv != "v0" {
			it = 1
		}
	}
	if *split == "test" {
		marker := dir.Path("TEST_SPLIT_OPENED")
		if b, err := os.ReadFile(marker); err == nil {
			return fmt.Errorf("the test split was already opened for this run (%s); it is opened once", strings.TrimSpace(string(b)))
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err := os.WriteFile(marker, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o444); err != nil {
			return err
		}
	} else if *split != "calibration" && *split != "all" {
		return fmt.Errorf("-split must be calibration, test or all")
	}

	in := report.Inputs{Config: cfg, Split: *split, Taxonomy: tv, Iteration: it}
	if in.Records, err = jsonl.Read[corpus.Record](dir.Records()); err != nil {
		return err
	}
	if in.Tags, err = jsonl.Read[corpus.TagResult](dir.Tags()); err != nil {
		return err
	}
	if in.Leaves, err = jsonl.Read[corpus.Leaf](dir.Leaves(tv)); err != nil {
		return err
	}
	if len(in.Leaves) == 0 {
		return fmt.Errorf("no leaves for taxonomy %s", tv)
	}
	if in.Cells, err = matrix.ReadAll(dir.MatrixDir()); err != nil {
		return err
	}
	labels := map[string]label.Label{}
	if err := jsonl.Each(dir.Labels(), func(l label.Label) error { labels[l.Key()] = l; return nil }); err != nil {
		return err
	}
	for _, l := range labels {
		in.Labels = append(in.Labels, l)
	}

	rep, err := report.Build(in)
	if err != nil {
		return err
	}
	suffix := ""
	if *split != "calibration" {
		suffix += "." + *split
	}
	if tv != "v0" {
		suffix += "." + tv
	}
	md, err := os.Create(strings.TrimSuffix(dir.ReportMD(), ".md") + suffix + ".md")
	if err != nil {
		return err
	}
	rep.Markdown(md)
	if err := md.Close(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(strings.TrimSuffix(dir.ReportJSON(), ".json")+suffix+".json", b, 0o644); err != nil {
		return err
	}
	if err := rep.PlotAccuracyCost(dir.Plot("accuracy_cost" + suffix + ".png")); err != nil {
		return err
	}
	if err := rep.PlotClassRho(dir.Plot("class_rho" + suffix + ".png")); err != nil {
		return err
	}
	fmt.Println(report.DecisionRule)
	fmt.Println(rep.Decision)
	return nil
}
