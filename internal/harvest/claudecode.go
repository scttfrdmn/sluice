// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

package harvest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/scttfrdmn/sluice/internal/corpus"
)

// Claude Code session logs: corpus/raw/claude-code/<host>/projects/<encoded-cwd>/<session>.jsonl.
// Field names were confirmed by cmd/probe; see docs/SCHEMAS.md.

// injectedTags mark turns that were written by the harness, not typed by a
// person. A turn containing any of them is skipped (CC_PROMPT.md lists
// command-name and system-reminder; the rest were found by the probe).
var injectedTags = []string{
	"<command-name>", "<command-message>", "<command-args>",
	"<system-reminder>",
	"<local-command-stdout>", "<local-command-stderr>", "<local-command-caveat>",
	"<bash-input>", "<bash-stdout>", "<bash-stderr>",
	"<task-notification>", "<user-prompt-submit-hook>",
}

type ccLine struct {
	Type             string `json:"type"`
	UUID             string `json:"uuid"`
	SessionID        string `json:"sessionId"`
	Timestamp        string `json:"timestamp"`
	Cwd              string `json:"cwd"`
	Entrypoint       string `json:"entrypoint"`
	IsSidechain      bool   `json:"isSidechain"`
	IsMeta           bool   `json:"isMeta"`
	IsCompactSummary bool   `json:"isCompactSummary"`
	PromptSource     string `json:"promptSource"`
	Origin           *struct {
		Kind string `json:"kind"`
	} `json:"origin"`
	Message *struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type ccBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// harvestClaudeCode walks root/<host>/projects and returns candidate
// records. Skips (turns that are not human-typed requests) are counted in st.
func harvestClaudeCode(root string, contextChars int, st *Stats) ([]corpus.Record, error) {
	hosts, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []corpus.Record
	for _, h := range hosts {
		if !h.IsDir() {
			continue
		}
		host := h.Name()
		proj := filepath.Join(root, host, "projects")
		var files []string
		err := filepath.WalkDir(proj, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && d.Name() == "subagents" {
				st.skip(corpus.SourceClaudeCode, host, "subagent-file-dir", 1)
				return filepath.SkipDir
			}
			if !d.IsDir() && strings.HasSuffix(p, ".jsonl") {
				files = append(files, p)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk %s: %w", proj, err)
		}
		sort.Strings(files)
		for _, f := range files {
			recs, err := claudeCodeFile(f, host, contextChars, st)
			if err != nil {
				return nil, err
			}
			out = append(out, recs...)
		}
	}
	return out, nil
}

func claudeCodeFile(path, host string, contextChars int, st *Stats) ([]corpus.Record, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	src := corpus.SourceClaudeCode
	var (
		out       []corpus.Record
		prevHuman string
		asst      strings.Builder
		turn      int
		lineNo    int
	)
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 1<<20), 256<<20)
	for sc.Scan() {
		lineNo++
		var l ccLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			st.skip(src, host, "unparseable-line", 1)
			continue
		}
		if l.Message == nil {
			continue
		}
		if l.Type == "assistant" && !l.IsSidechain {
			var blocks []ccBlock
			if json.Unmarshal(l.Message.Content, &blocks) == nil {
				for _, b := range blocks {
					if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
						if asst.Len() > 0 {
							asst.WriteString("\n\n")
						}
						asst.WriteString(b.Text)
					}
				}
			}
			continue
		}
		if l.Type != "user" {
			continue
		}
		text, hasToolResult, hasAttachment := ccContent(l.Message.Content)
		reason := ""
		switch {
		case hasToolResult:
			reason = "tool-result"
		case l.IsSidechain:
			reason = "sidechain"
		case l.IsMeta:
			reason = "meta"
		case l.IsCompactSummary:
			reason = "compact-summary"
		case strings.HasPrefix(l.Entrypoint, "sdk"):
			reason = "entrypoint-" + l.Entrypoint
		case l.PromptSource != "" && l.PromptSource != "typed" && l.PromptSource != "queued":
			// queued = typed by the person while the agent was busy.
			reason = "prompt-source-" + l.PromptSource
		case l.Origin != nil && l.Origin.Kind != "" && l.Origin.Kind != "human":
			reason = "origin-" + l.Origin.Kind
		case strings.TrimSpace(text) == "" && hasAttachment:
			reason = "attachment-only"
		case strings.TrimSpace(text) == "":
			reason = "empty"
		case strings.HasPrefix(strings.TrimSpace(text), "[Request interrupted"):
			reason = "interrupt-marker"
		case strings.HasPrefix(strings.TrimSpace(text), "This session is being continued from a previous conversation"):
			// Compaction summary from client versions that predate isCompactSummary.
			reason = "compact-summary-unflagged"
		default:
			for _, tag := range injectedTags {
				if strings.Contains(text, tag) {
					reason = "tag-" + strings.Trim(tag, "<>")
					break
				}
			}
		}
		if reason != "" {
			if reason != "tool-result" {
				st.skip(src, host, reason, 1)
			}
			continue
		}
		ctx := ""
		if prevHuman != "" || asst.Len() > 0 {
			ctx = buildContext(prevHuman, asst.String(), contextChars)
		}
		out = append(out, corpus.Record{
			Source:        src,
			AuthorKind:    corpus.AuthorHuman,
			AuthorRule:    corpus.RuleClaudeCodeTurn,
			Host:          host,
			OriginRef:     fmt.Sprintf("claude-code:%s:%s:%s", host, l.SessionID, l.UUID),
			Timestamp:     l.Timestamp,
			Text:          text,
			ContextText:   ctx,
			ContextChars:  asst.Len(),
			HasAttachment: hasAttachment,
			TurnIndex:     turn,
			Cwd:           l.Cwd,
			SessionID:     l.SessionID,
		})
		turn++
		prevHuman = text
		asst.Reset()
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s:%d: %w", path, lineNo, err)
	}
	return out, nil
}

// ccContent returns the human text of a user message, whether it carries a
// tool_result block, and whether it carries a non-text block (image, document).
func ccContent(raw json.RawMessage) (text string, toolResult, attachment bool) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, false, false
	}
	var blocks []ccBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return "", false, false
	}
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			parts = append(parts, b.Text)
		case "tool_result":
			toolResult = true
		default:
			attachment = true
		}
	}
	return strings.Join(parts, "\n\n"), toolResult, attachment
}

// buildContext is the preceding user turn plus the preceding assistant
// turn's text, oldest first, truncated to maxChars keeping the most recent
// text (PROJECT.md §5).
func buildContext(prevUser, prevAssistant string, maxChars int) string {
	var b strings.Builder
	if prevUser != "" {
		b.WriteString("User: ")
		b.WriteString(prevUser)
	}
	if prevAssistant != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("Assistant: ")
		b.WriteString(prevAssistant)
	}
	return truncateTail(b.String(), maxChars)
}

// truncateTail keeps the last maxChars bytes of s, cut at a rune boundary
// and marked with a leading ellipsis.
func truncateTail(s string, maxChars int) string {
	if maxChars <= 0 || len(s) <= maxChars {
		return s
	}
	cut := len(s) - maxChars
	for cut < len(s) && (s[cut]&0xC0) == 0x80 {
		cut++
	}
	return "…" + s[cut:]
}
