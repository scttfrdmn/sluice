// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

// Command harvest builds the request corpus (PROJECT.md §5, CC_PROMPT.md
// cmd/harvest).
//
//	harvest pull-sessions [-config f] [-dest corpus/raw/claude-code]
//	harvest subsample -run <id> [-per-stratum N] [-split calibration]
//	harvest -run <id> [-claude-code dir] [-claude-export f] [-chatgpt-export f]
//	        [-repos dir] [-wildchat N] ...
//
// Output: runs/<id>/records.jsonl and runs/<id>/repo_prompt_files.txt (local
// only) and runs/<id>/harvest_stats.json (aggregate counts, publishable).
// subsample writes runs/<id>/subsample.json, the E0 subsample that tag,
// matrix, label and report then restrict to.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/scttfrdmn/sluice/internal/config"
	"github.com/scttfrdmn/sluice/internal/corpus"
	"github.com/scttfrdmn/sluice/internal/harvest"
	"github.com/scttfrdmn/sluice/internal/jsonl"
	"github.com/scttfrdmn/sluice/internal/rundir"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "harvest:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()
	if len(os.Args) > 1 && os.Args[1] == "pull-sessions" {
		fs := flag.NewFlagSet("pull-sessions", flag.ExitOnError)
		cfgPath := fs.String("config", "config/sluice.yaml", "config file")
		dest := fs.String("dest", "corpus/raw/claude-code", "destination root")
		fs.Parse(os.Args[2:])
		cfg, err := config.Load(*cfgPath)
		if err != nil {
			return err
		}
		return harvest.PullSessions(ctx, cfg, *dest, os.Stdout)
	}
	if len(os.Args) > 1 && os.Args[1] == "subsample" {
		return runSubsample(os.Args[2:])
	}

	var (
		cfgPath      = flag.String("config", "config/sluice.yaml", "config file")
		runsRoot     = flag.String("runs", "runs", "runs root")
		runID        = flag.String("run", "", "run id (required)")
		claudeCode   = flag.String("claude-code", "", "Claude Code logs root (<host>/projects/...)")
		claudeExport = flag.String("claude-export", "", "claude.ai conversations.json")
		chatgpt      = flag.String("chatgpt-export", "", "ChatGPT conversations.json")
		repos        = flag.String("repos", "", "repos root (e.g. ~/src)")
		owner        = flag.String("owner", "", "GitHub login whose issues are kept (default: config harvest.owner)")
		issues       = flag.Bool("issues", true, "query GitHub issues with gh when -repos is set")
		wildchatN    = flag.Int("wildchat", 0, "public-control sample size N (0 disables)")
		wildchatFile = flag.String("wildchat-file", "corpus/raw/wildchat/sample.jsonl", "public-control sample JSONL")
		review       = flag.String("review", "corpus/review", "directory for drop samples (gitignored)")
		corpusMD     = flag.String("corpus-md", "", "also write the aggregate table as markdown to this path (e.g. docs/CORPUS.md)")
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
	o := harvest.Options{
		ClaudeCode:   *claudeCode,
		ClaudeExport: *claudeExport,
		ChatGPT:      *chatgpt,
		ReviewDir:    *review,
		WildchatN:    *wildchatN,
	}
	if *repos != "" {
		o.Repos = harvest.ReposOptions{Root: expand(*repos), Owner: *owner, Exclude: cfg.Harvest.ExcludeRepos}
		if o.Repos.Owner == "" {
			o.Repos.Owner = cfg.Harvest.Owner
		}
		if *issues {
			if harvest.GHAvailable(ctx) {
				o.Repos.Issues = true
			} else {
				fmt.Fprintln(os.Stderr, "harvest: gh not available or not authenticated; GitHub issues are NOT included")
			}
		}
	}
	if *wildchatN > 0 {
		if err := ensureWildchat(cfg, *wildchatFile, *wildchatN); err != nil {
			return err
		}
		o.Wildchat = *wildchatFile
	}

	recs, st, err := harvest.Run(ctx, cfg, o)
	if err != nil {
		return err
	}
	if err := jsonl.Write(dir.Records(), recs); err != nil {
		return err
	}
	if err := harvest.WriteStats(dir.HarvestStats(), st); err != nil {
		return err
	}
	if err := os.WriteFile(dir.PromptFiles(), []byte(strings.Join(append(st.PromptFiles, ""), "\n")), 0o644); err != nil {
		return err
	}
	st.PrintTable(os.Stdout)
	fmt.Printf("repo prompt files matched: %d (list with outcomes in %s)\n", len(st.PromptFiles), dir.PromptFiles())
	if *corpusMD != "" {
		f, err := os.Create(*corpusMD)
		if err != nil {
			return err
		}
		st.Markdown(f, fmt.Sprintf("Harvest run `%s`, %s.", *runID, time.Now().UTC().Format("2006-01-02")))
		if err := f.Close(); err != nil {
			return err
		}
	}
	fmt.Printf("wrote %d records to %s\n", len(recs), dir.Records())
	return nil
}

func runSubsample(args []string) error {
	fs := flag.NewFlagSet("subsample", flag.ExitOnError)
	cfgPath := fs.String("config", "config/sluice.yaml", "config file")
	runsRoot := fs.String("runs", "runs", "runs root")
	runID := fs.String("run", "", "run id (required)")
	per := fs.Int("per-stratum", 0, "requests per stratum (default: config harvest.e0_per_stratum)")
	split := fs.String("split", "", "calibration | test | all (default: config harvest.e0_split)")
	corpusMD := fs.String("corpus-md", "", "append the subsample table to this markdown file (e.g. docs/CORPUS.md)")
	fs.Parse(args)
	if *runID == "" {
		return errors.New("-run is required")
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	if *per == 0 {
		*per = cfg.Harvest.E0PerStratum
	}
	if *split == "" {
		*split = cfg.Harvest.E0Split
	}
	if *per <= 0 {
		return errors.New("per-stratum must be positive")
	}
	dir, err := rundir.Open(*runsRoot, *runID)
	if err != nil {
		return err
	}
	if _, err := os.Stat(dir.Subsample()); err == nil {
		return fmt.Errorf("%s exists; the subsample is drawn once per run", dir.Subsample())
	}
	recs, err := jsonl.Read[corpus.Record](dir.Records())
	if err != nil {
		return err
	}
	s := corpus.DrawSubsample(recs, *per, *split, cfg.Seeds.E0Sample)
	if err := corpus.WriteSubsample(dir.Subsample(), s); err != nil {
		return err
	}
	fmt.Printf("subsample (split %s, seed %d, %d per stratum):\n", s.Split, s.Seed, s.PerStratum)
	for _, k := range slices.Sorted(maps.Keys(s.Available)) {
		fmt.Printf("  %-20s %6d of %6d\n", k, s.Selected[k], s.Available[k])
	}
	fmt.Printf("wrote %d ids to %s\n", len(s.IDs), dir.Subsample())
	if *corpusMD != "" {
		f, err := os.OpenFile(*corpusMD, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		fmt.Fprintf(f, "\n## E0 subsample\n\nSeeded (seed %d), stratified draw from the %s split, up to %d requests per stratum (PROJECT.md §5). E0 runs on these requests only.\n\n| stratum | selected | available |\n|---|---:|---:|\n", s.Seed, s.Split, s.PerStratum)
		for _, k := range slices.Sorted(maps.Keys(s.Available)) {
			fmt.Fprintf(f, "| %s | %d | %d |\n", k, s.Selected[k], s.Available[k])
		}
		fmt.Fprintf(f, "| **total** | %d | |\n", len(s.IDs))
		if err := f.Close(); err != nil {
			return err
		}
	}
	return nil
}

// ensureWildchat runs tools/wildchat.py if the sample file is missing.
func ensureWildchat(cfg *config.Config, path string, n int) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	over := int(float64(n) * cfg.Harvest.WildchatOversample)
	cmd := exec.Command("uv", "run", "--script", "tools/wildchat.py",
		"--out", path, "--n", strconv.Itoa(over), "--seed", strconv.FormatUint(cfg.Seeds.Wildchat, 10),
		"--dataset", cfg.Harvest.WildchatDataset, "--fallback", cfg.Harvest.WildchatFallback)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tools/wildchat.py: %w", err)
	}
	return nil
}

func expand(p string) string {
	if len(p) > 1 && p[:2] == "~/" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}
