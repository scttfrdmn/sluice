// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

// Package corpus defines the harvested request record (PROJECT.md §5).
package corpus

// Source tags.
const (
	SourceClaudeCode   = "claude-code"
	SourceClaudeExport = "claude-export"
	SourceChatGPT      = "chatgpt"
	SourceRepos        = "repos"
	SourceWildchat     = "wildchat"
)

// Owner reports whether a source is the owner's own traffic (as opposed to
// the public control).
func Owner(source string) bool { return source != SourceWildchat }

// Record is one harvested request.
type Record struct {
	ID        string `json:"id"`
	Source    string `json:"source"`
	Host      string `json:"host,omitempty"`
	OriginRef string `json:"origin_ref"`
	Timestamp string `json:"timestamp,omitempty"`
	Text      string `json:"text"`
	// ContextText is the preceding user turn plus the preceding assistant
	// turn's text blocks, truncated to the configured token budget (most
	// recent text kept). Empty for first turns.
	ContextText string `json:"context_text"`
	// ContextChars is the character length of the preceding assistant turn
	// before truncation.
	ContextChars int `json:"context_chars"`
	// Standalone is set by the tagger, not the harvester.
	Standalone    *bool  `json:"standalone,omitempty"`
	Lang          string `json:"lang"`
	HasAttachment bool   `json:"has_attachment"`
	Split         string `json:"split"`
	// TurnIndex is the position of the turn among kept-or-dropped human
	// turns in its session or conversation (0-based).
	TurnIndex int `json:"turn_index"`
	// Cwd and SessionID locate Claude Code turns (and conversation IDs for
	// the chat exports).
	Cwd       string `json:"cwd,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	// Rank orders public-control records for uniform sampling (WildChat).
	Rank int `json:"rank,omitempty"`
}
