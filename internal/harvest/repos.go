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
	"unicode/utf8"

	"github.com/scttfrdmn/sluice/internal/corpus"
)

// Repo prompts: prompt files (promptGlobs, PROJECT.md) under each top-level
// directory of the repos root, plus owner-authored GitHub issue bodies.
// These are compound by construction (PROJECT.md §5).

var skipDirs = map[string]bool{".git": true, "node_modules": true, ".venv": true, "venv": true, "target": true, "dist": true, "build": true, "__pycache__": true, ".terraform": true}

// vendoredDirs hold third-party code. They are walked so that prompt-file
// matches inside them are listed as excluded, but nothing in them is kept.
var vendoredDirs = map[string]bool{"vendor": true, ".hermit": true}

// promptGlobs match file basenames case-insensitively. *prompt*.md already
// covers the others; they are listed so the intent stays visible.
var promptGlobs = []string{"*kickoff*prompt*.md", "*claude_code_prompt*.md", "*prompt*.md", "*_prompt.md", "*.prompt.md"}

// excludedPrefixes are basenames never treated as prompt files.
var excludedPrefixes = []string{"readme", "changelog", "license"}

func isPromptFile(name string) bool {
	if name == "PROJECT.md" {
		return true
	}
	n := strings.ToLower(name)
	for _, p := range excludedPrefixes {
		if strings.HasPrefix(n, p) {
			return false
		}
	}
	for _, g := range promptGlobs {
		if ok, _ := filepath.Match(g, n); ok {
			return true
		}
	}
	return false
}

// generatedFooter marks issue bodies written by an agent under the owner's
// account.
var generatedFooter = regexp.MustCompile(`(?i)generated with \[?claude code|co-authored-by: claude|🤖 generated with`)

var reH2 = regexp.MustCompile(`(?m)^## `)

// issueAuthor assigns author_kind to an owner issue body (PROJECT.md §5):
// agent if the generated footer is present, or the body has ≥2 "## "
// headings and ≥400 characters; otherwise unknown.
func issueAuthor(body string) (kind, rule string) {
	switch {
	case generatedFooter.MatchString(body):
		return corpus.AuthorAgent, corpus.RuleIssueFooter
	case len(reH2.FindAllStringIndex(body, -1)) >= 2 && utf8.RuneCountInString(body) >= 400:
		return corpus.AuthorAgent, corpus.RuleIssueStructure
	}
	return corpus.AuthorUnknown, corpus.RuleIssueNone
}

// ReposOptions controls the repos source.
type ReposOptions struct {
	Root    string
	Owner   string
	Exclude []string
	Issues  bool // query GitHub issues with gh
	// PromptOwners are the remote owners whose prompt files are kept
	// (plus repos with no remote). Empty keeps every owner.
	PromptOwners []string
}

// remoteOwner returns the GitHub owner of dir's origin remote, "" if dir is
// a git repository without a GitHub origin, and ok false if dir is not a
// git repository.
func remoteOwner(ctx context.Context, dir string) (owner string, ok bool) {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return "", false
	}
	b, err := exec.CommandContext(ctx, "git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return "", true
	}
	m := reGitHubRemote.FindStringSubmatch(strings.TrimSpace(string(b)))
	if m == nil {
		return "", true
	}
	return m[1], true
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
		// owners maps each git root under repoDir to its remote owner; a
		// file belongs to its nearest enclosing git root.
		owners := map[string]string{}
		err := filepath.WalkDir(repoDir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // unreadable subtree; not fatal
			}
			if d.IsDir() {
				if skipDirs[d.Name()] {
					return filepath.SkipDir
				}
				if ow, ok := remoteOwner(ctx, p); ok {
					owners[p] = ow
				}
				return nil
			}
			if d.Type()&fs.ModeSymlink != 0 || !isPromptFile(d.Name()) {
				return nil
			}
			rel, _ := filepath.Rel(o.Root, p)
			if why := promptExclusion(o.Root, rel, owners, o.PromptOwners); why != "" {
				st.PromptExcluded[filepath.ToSlash(rel)] = why
				st.skip(src, "", "prompt-file-"+strings.SplitN(why, " ", 2)[0], 1)
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
			out = append(out, corpus.Record{
				Source:     src,
				AuthorKind: corpus.AuthorAgent,
				AuthorRule: corpus.RulePromptFile,
				OriginRef:  "repos:file:" + filepath.ToSlash(rel),
				Timestamp:  ts,
				Text:       string(b),
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

// promptExclusion says why a matched prompt file is not kept, or "".
func promptExclusion(root, rel string, owners map[string]string, allowed []string) string {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for _, seg := range parts[:len(parts)-1] {
		if vendoredDirs[seg] {
			return "excluded:vendored-dir " + seg
		}
	}
	if len(allowed) == 0 {
		return ""
	}
	for dir := filepath.Dir(filepath.Join(root, rel)); len(dir) >= len(root); dir = filepath.Dir(dir) {
		if ow, ok := owners[dir]; ok {
			if ow == "" || slices.Contains(allowed, ow) {
				return ""
			}
			return "excluded:remote-owner " + ow
		}
		if dir == root {
			break
		}
	}
	return "" // not in any git repository: no remote
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
				case strings.TrimSpace(i.Body) == "":
					st.skip(src, "", "issue-empty", 1)
				default:
					kind, rule := issueAuthor(i.Body)
					out = append(out, corpus.Record{
						Source:     src,
						AuthorKind: kind,
						AuthorRule: rule,
						OriginRef:  fmt.Sprintf("repos:issue:%s#%d", r, i.Number),
						Timestamp:  i.CreatedAt,
						Text:       i.Body,
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
			Source:     corpus.SourceWildchat,
			AuthorKind: corpus.AuthorHuman,
			AuthorRule: corpus.RuleWildchat,
			OriginRef:  fmt.Sprintf("%s:%s", w.Dataset, w.ID),
			Timestamp:  w.Timestamp,
			Text:       w.Text,
			Rank:       w.Rank,
		})
	}
	return out, nil
}
