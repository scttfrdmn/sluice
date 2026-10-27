// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

package harvest

import (
	"context"
	"strings"
	"testing"

	"github.com/scttfrdmn/sluice/internal/config"
	"github.com/scttfrdmn/sluice/internal/corpus"
)

// Credential-shaped strings are assembled at run time so that no
// credential-shaped literal exists in the repository.
func fakeKey(prefix string, n int) string { return prefix + strings.Repeat("Q7", n/2) }

func TestSecretPatterns(t *testing.T) {
	cases := []struct {
		name, text, want string
	}{
		{"access key", "key " + fakeKey("AKIA", 16) + " here", "aws-access-key-id"},
		{"secret shape", "secret=" + strings.Repeat("aB3/", 10) + " end", "aws-secret-key-shape"},
		{"git sha is not a secret", "commit " + strings.Repeat("a1b2c3d4", 5) + " fixed", ""},
		{"bearer", "Authorization: Bearer " + strings.Repeat("x9", 15), "bearer-token"},
		{"private key", "-----BEGIN " + "RSA PRIVATE KEY-----", "private-key"},
		{"account in arn", "arn:aws:iam::" + strings.Repeat("1", 12) + ":role/x", "aws-account-id-in-context"},
		{"account word", "our AWS account " + strings.Repeat("4", 12), "aws-account-id-in-context"},
		{"bare 12 digits", "order number " + strings.Repeat("7", 12) + " shipped", ""},
		{"12 digits far from context", "aws" + strings.Repeat(" filler", 10) + " " + strings.Repeat("3", 12), ""},
		{"bucket uri", "copy to s3://" + "my-data-bucket/x", "s3-bucket-uri"},
		{"placeholder bucket", "copy to s3://" + "bucket/prefix/", ""},
		{"example account", "arn:aws:iam::" + "1234" + "56789012" + ":role/x", ""},
		{"github token", "token " + fakeKey("ghp_", 36), "github-token"},
		{"plain prose", "Please refactor the parser and add tests.", ""},
	}
	for _, c := range cases {
		got, _ := secretMatch(c.text)
		if got != c.want {
			t.Errorf("%s: secretMatch = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestDetectEnglish(t *testing.T) {
	yes := []string{
		"Write a Python function that returns the n-th Fibonacci number.",
		"fix the bug in ./internal/foo/bar.go where it panics on nil",
		"now add tests for this:\n```go\nfunc f(x int) int { return x * 2 }\n```",
		"something different happened",
		"update GitHub issue #95",
		"Describe Earth became graveyard planet",
		"need to align normally these previously recieved images from api backend",
		"merge #546, #547, #552",
		"open the project site in the browser",
		"merge #149 and cut lagotto v0.58.1.",
		"invert it (or just use IP)",
	}
	no := []string{
		"¿Cuál es la capital de Australia y por qué no es Sídney la capital del país?",
		"这是一个中文问题，用来测试语言过滤器是否正确工作。",
		"Wie kann ich in Python eine Liste nach dem zweiten Element sortieren und dabei Duplikate entfernen?",
		"Les éléments suivants sont les éléments de contrôle de la transcription",
		"Напиши код, который использует случайную величину 0 или 1.",
	}
	for _, s := range yes {
		if r := detectEnglish(s); !r.English {
			t.Errorf("want English (%s): %q", r.Lang, s)
		}
	}
	for _, s := range no {
		if r := detectEnglish(s); r.English {
			t.Errorf("want non-English (%s): %q", r.Lang, s)
		}
	}
}

func TestDedup(t *testing.T) {
	texts := []string{
		"Write a Python function that returns the n-th Fibonacci number, with a docstring.",
		"Explain why the sky is blue in two sentences.",
		"Write a Python function that returns the n-th Fibonacci number, with a docstring!",
		"Write a Go function that returns the n-th prime number.",
	}
	got := dedup(texts, 0.85, 1)
	want := []int{-1, -1, 0, -1}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("dedup = %v, want %v", got, want)
		}
	}
}

func TestTruncateTail(t *testing.T) {
	s := "héllo wörld"
	got := truncateTail(s, 6)
	if !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "wörld") {
		t.Fatalf("truncateTail = %q", got)
	}
	if truncateTail("short", 100) != "short" {
		t.Fatal("short text changed")
	}
}

func TestFixtureHarvest(t *testing.T) {
	cfg, err := config.Load("../../testdata/sluice.test.yaml")
	if err != nil {
		t.Fatal(err)
	}
	raw := "../../testdata/raw/"
	recs, st, err := Run(context.Background(), cfg, Options{
		ClaudeCode:   raw + "claude-code",
		ClaudeExport: raw + "claude-export/conversations.json",
		ChatGPT:      raw + "chatgpt-export/conversations.json",
		Repos:        ReposOptions{Root: raw + "repos", Exclude: cfg.Harvest.ExcludeRepos},
		Wildchat:     raw + "wildchat/sample.jsonl",
		WildchatN:    4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 20 {
		t.Fatalf("kept %d records, want 20", len(recs))
	}
	bySource := map[string]int{}
	for _, r := range recs {
		bySource[r.Source]++
		for _, banned := range []string{"Subagent prompt", "edited-away branch", "custom instructions", "Vendored file", "own docs", "SDK", "harness injected"} {
			if strings.Contains(r.Text, banned) {
				t.Errorf("record %s should have been skipped: %q", r.OriginRef, r.Text)
			}
		}
		if r.Split != "calibration" && r.Split != "test" {
			t.Errorf("bad split %q", r.Split)
		}
		want := corpus.AuthorHuman
		if r.Source == corpus.SourceRepos {
			want = corpus.AuthorAgent
		}
		if r.AuthorKind != want || r.AuthorRule == "" {
			t.Errorf("%s: author_kind %q rule %q, want %q", r.OriginRef, r.AuthorKind, r.AuthorRule, want)
		}
	}
	want := map[string]int{corpus.SourceClaudeCode: 7, corpus.SourceClaudeExport: 4, corpus.SourceChatGPT: 2, corpus.SourceRepos: 3, corpus.SourceWildchat: 4}
	for s, n := range want {
		if bySource[s] != n {
			t.Errorf("%s: %d records, want %d", s, bySource[s], n)
		}
	}
	// Context: the second human turn sees the first turn and the assistant's text blocks.
	for _, r := range recs {
		if strings.HasPrefix(r.Text, "now add tests") {
			if !strings.Contains(r.ContextText, "User: Write a Python function") || !strings.Contains(r.ContextText, "Done: fib(n)") {
				t.Errorf("context_text missing parts: %q", r.ContextText)
			}
			if strings.Contains(r.ContextText, "tool_result") {
				t.Errorf("context_text includes tool results")
			}
		}
		if r.TurnIndex == 0 && r.Source == corpus.SourceClaudeCode && strings.HasPrefix(r.Text, "Write a Python") && r.ContextText != "" {
			t.Errorf("first turn has context %q", r.ContextText)
		}
	}
	if st.rows[Key{corpus.SourceClaudeCode, "testhost"}].Dropped["near-duplicate"] != 1 {
		t.Errorf("near-duplicate not dropped")
	}
}

func TestPromptFileGlobs(t *testing.T) {
	yes := []string{"CC_PROMPT.md", "PROJECT.md", "feature.prompt.md", "ember kickoff prompt.md", "Sluice_Kickoff_Prompt.md",
		"X_CLAUDE_CODE_PROMPT.md", "AUDIT_PROMPT.md", "phase2_prompt.md", "prompts.md"}
	no := []string{"README.md", "README_PROMPT.md", "CHANGELOG.md", "LICENSE.md", "prompt.txt", "notes.md", "tag_prompt.txt", "project.md"}
	for _, n := range yes {
		if !isPromptFile(n) {
			t.Errorf("want prompt file: %q", n)
		}
	}
	for _, n := range no {
		if isPromptFile(n) {
			t.Errorf("want not a prompt file: %q", n)
		}
	}
}

func TestIssueAuthor(t *testing.T) {
	long := strings.Repeat("Some detail about the change. ", 15)
	cases := []struct {
		body, kind, rule string
	}{
		{"Fix it.\n\n🤖 Generated with [Claude Code](https://claude.com/claude-code)", corpus.AuthorAgent, corpus.RuleIssueFooter},
		{"## Summary\n" + long + "\n## Plan\n- a\n", corpus.AuthorAgent, corpus.RuleIssueStructure},
		{"## Summary\nshort\n## Plan\n- a\n", corpus.AuthorUnknown, corpus.RuleIssueNone},
		{"## Only one heading\n" + long, corpus.AuthorUnknown, corpus.RuleIssueNone},
		{"the parser panics on empty input", corpus.AuthorUnknown, corpus.RuleIssueNone},
	}
	for _, c := range cases {
		k, r := issueAuthor(c.body)
		if k != c.kind || r != c.rule {
			t.Errorf("issueAuthor(%.40q) = %s/%s, want %s/%s", c.body, k, r, c.kind, c.rule)
		}
	}
}
