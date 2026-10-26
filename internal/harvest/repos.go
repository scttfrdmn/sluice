// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

package harvest

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/scttfrdmn/sluice/internal/corpus"
)

// Repo prompts: CC_PROMPT.md, PROJECT.md and *.prompt.md under each
// top-level directory of the repos root, plus owner-authored GitHub issue
// bodies. These are compound by construction (PROJECT.md §5).

var skipDirs = map[string]bool{".git": true, "node_modules": true, "vendor": true, ".venv": true, "venv": true, "target": true, "dist": true, "build": true, "__pycache__": true, ".terraform": true}

func isPromptFile(name string) bool {
	return name == "CC_PROMPT.md" || name == "PROJECT.md" || strings.HasSuffix(name, ".prompt.md")
}

// generatedFooter marks issue bodies written by an agent under the owner's
// account. They are not owner-typed traffic, so they are skipped and counted.
var generatedFooter = regexp.MustCompile(`(?i)generated with \[?claude code|co-authored-by: claude|🤖 generated with`)

// ReposOptions controls the repos source.
type ReposOptions struct {
	Root    string
	Owner   string
	Exclude []string
	Issues  bool // query GitHub issues with gh
}

func harvestRepos(ctx context.Context, o ReposOptions, st *Stats) ([]corpus.Record, error) {
	src := corpus.SourceRepos
	entries, err := os.ReadDir(o.Root)
	if err != nil {
		return nil, err
	}
	var out []corpus.Record
	var issueRepos []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if slices.Contains(o.Exclude, e.Name()) {
			st.skip(src, "", "excluded-repo", 1)
			continue
		}
		repoDir := filepath.Join(o.Root, e.Name())
		err := filepath.WalkDir(repoDir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // unreadable subtree; not fatal
			}
			if d.IsDir() {
				if skipDirs[d.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Type()&fs.ModeSymlink != 0 || !isPromptFile(d.Name()) {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			info, _ := d.Info()
			ts := ""
			if info != nil {
				ts = info.ModTime().UTC().Format(time.RFC3339)
			}
			rel, _ := filepath.Rel(o.Root, p)
			out = append(out, corpus.Record{
				Source:    src,
				OriginRef: "repos:file:" + filepath.ToSlash(rel),
				Timestamp: ts,
				Text:      string(b),
			})
			return nil
		})
		if err != nil {
			return nil, err
		}
		if o.Issues {
			if slug := githubSlug(ctx, repoDir); slug != "" {
				issueRepos = append(issueRepos, slug)
			}
		}
	}
	if o.Issues {
		recs, err := harvestIssues(ctx, issueRepos, o.Owner, st)
		if err != nil {
			return nil, err
		}
		out = append(out, recs...)
	}
	return out, nil
}

var reGitHubRemote = regexp.MustCompile(`github\.com[:/]([^/\s]+)/([^/\s]+?)(?:\.git)?/?$`)

func githubSlug(ctx context.Context, dir string) string {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return ""
	}
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "remote", "get-url", "origin")
	b, err := cmd.Output()
	if err != nil {
		return ""
	}
	m := reGitHubRemote.FindStringSubmatch(strings.TrimSpace(string(b)))
	if m == nil {
		return ""
	}
	return m[1] + "/" + m[2]
}

// GHAvailable reports whether gh is on PATH and authenticated.
func GHAvailable(ctx context.Context) bool {
	if _, err := exec.LookPath("gh"); err != nil {
		return false
	}
	return exec.CommandContext(ctx, "gh", "auth", "status").Run() == nil
}

func harvestIssues(ctx context.Context, repos []string, owner string, st *Stats) ([]corpus.Record, error) {
	src := corpus.SourceRepos
	sort.Strings(repos)
	repos = slices.Compact(repos)
	type issue struct {
		Number    int    `json:"number"`
		Body      string `json:"body"`
		CreatedAt string `json:"createdAt"`
		Author    struct {
			Login string `json:"login"`
		} `json:"author"`
	}
	var (
		mu   sync.Mutex
		out  []corpus.Record
		errs []string
		wg   sync.WaitGroup
		sem  = make(chan struct{}, 6)
	)
	for _, r := range repos {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			cmd := exec.CommandContext(ctx, "gh", "issue", "list", "-R", r, "--limit", "500", "--state", "all", "--json", "number,body,author,createdAt")
			b, err := cmd.Output()
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				// Repos with issues disabled or no access are reported, not fatal.
				errs = append(errs, r)
				st.skip(src, "", "gh-issue-list-failed", 1)
				return
			}
			var is []issue
			if err := json.Unmarshal(b, &is); err != nil {
				errs = append(errs, r)
				return
			}
			for _, i := range is {
				switch {
				case i.Author.Login != owner:
					st.skip(src, "", "issue-not-owner", 1)
				case generatedFooter.MatchString(i.Body):
					st.skip(src, "", "issue-agent-generated", 1)
				case strings.TrimSpace(i.Body) == "":
					st.skip(src, "", "issue-empty", 1)
				default:
					out = append(out, corpus.Record{
						Source:    src,
						OriginRef: fmt.Sprintf("repos:issue:%s#%d", r, i.Number),
						Timestamp: i.CreatedAt,
						Text:      i.Body,
					})
				}
			}
		}()
	}
	wg.Wait()
	if len(errs) > 0 {
		sort.Strings(errs)
		fmt.Fprintf(os.Stderr, "harvest: gh issue list failed for %d repos: %s\n", len(errs), strings.Join(errs, ", "))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OriginRef < out[j].OriginRef })
	return out, nil
}

// WildChat (public control): the Python helper writes rank-ordered JSONL;
// harvest keeps the first N by rank that pass the filters.

type wildchatLine struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	Timestamp string `json:"timestamp"`
	Rank      int    `json:"rank"`
	Dataset   string `json:"dataset"`
}

func harvestWildchat(path string, st *Stats) ([]corpus.Record, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []corpus.Record
	for i, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var w wildchatLine
		if err := json.Unmarshal([]byte(line), &w); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, i+1, err)
		}
		out = append(out, corpus.Record{
			Source:    corpus.SourceWildchat,
			OriginRef: fmt.Sprintf("%s:%s", w.Dataset, w.ID),
			Timestamp: w.Timestamp,
			Text:      w.Text,
			Rank:      w.Rank,
		})
	}
	return out, nil
}
