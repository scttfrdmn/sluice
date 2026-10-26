// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

// Command harvest builds the request corpus (PROJECT.md §5, CC_PROMPT.md
// cmd/harvest).
//
//	harvest pull-sessions [-config f] [-dest corpus/raw/claude-code]
//	harvest -run <id> [-claude-code dir] [-claude-export f] [-chatgpt-export f]
//	        [-repos dir] [-wildchat N] ...
//
// Output: runs/<id>/records.jsonl (local only) and runs/<id>/harvest_stats.json
// (aggregate counts, publishable).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/scttfrdmn/sluice/internal/config"
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
	st.PrintTable(os.Stdout)
	fmt.Printf("wrote %d records to %s\n", len(recs), dir.Records())
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
