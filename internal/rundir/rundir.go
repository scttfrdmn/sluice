// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

// Package rundir names the files every stage reads and writes under
// runs/<run-id>/. Stages communicate only through these files.
package rundir

import (
	"os"
	"path/filepath"
)

// Dir is one run directory.
type Dir string

// Open creates the run directory if needed.
func Open(root, id string) (Dir, error) {
	d := filepath.Join(root, id)
	return Dir(d), os.MkdirAll(d, 0o755)
}

func (d Dir) p(name string) string { return filepath.Join(string(d), name) }

// Records is harvest output.
func (d Dir) Records() string { return d.p("records.jsonl") }

// HarvestStats is the aggregate harvest table (safe to publish).
func (d Dir) HarvestStats() string { return d.p("harvest_stats.json") }

// Tags is one row per request: standalone flag, status, leaves.
func (d Dir) Tags() string { return d.p("tags.jsonl") }

// Leaves is one row per leaf. Taxonomy iterations write leaves.<version>.jsonl.
func (d Dir) Leaves(version string) string {
	if version == "" || version == "v0" {
		return d.p("leaves.jsonl")
	}
	return d.p("leaves." + version + ".jsonl")
}

// MatrixDir holds the matrix as parquet part files.
func (d Dir) MatrixDir() string { return d.p("matrix") }

// MatrixErrors records cells that failed after all retries.
func (d Dir) MatrixErrors() string { return d.p("matrix_errors.jsonl") }

// Refs holds per-leaf references (generated tests, numeric answers).
func (d Dir) Refs() string { return d.p("refs.jsonl") }

// Labels is one row per matrix cell.
func (d Dir) Labels() string { return d.p("labels.jsonl") }

// LabeledMatrix is the matrix joined with labels, as parquet.
func (d Dir) LabeledMatrix() string { return d.p("matrix_labeled.parquet") }

// Cache is the SHA-256-keyed model-output cache.
func (d Dir) Cache() string { return d.p("cache") }

// Report outputs.
func (d Dir) ReportMD() string        { return d.p("report.md") }
func (d Dir) ReportJSON() string      { return d.p("report.json") }
func (d Dir) Plot(name string) string { return d.p(name) }

// Path joins an arbitrary name under the run directory.
func (d Dir) Path(name string) string { return d.p(name) }

// Subsample is the E0 calibration subsample: seed, strata counts and ids.
func (d Dir) Subsample() string { return d.p("subsample.json") }

// SubsampleTest is the matching test-split subsample, drawn with the
// calibration one and not tagged until the test split is opened.
func (d Dir) SubsampleTest() string { return d.p("subsample_test.json") }

// Pilot is the matrix+label pilot: a seeded, stratified draw of leaf ids
// that matrix, label and estimate restrict to with -pilot.
func (d Dir) Pilot() string { return d.p("pilot.json") }

// PromptFiles lists the repo prompt files the harvest kept (local only).
func (d Dir) PromptFiles() string { return d.p("repo_prompt_files.txt") }
