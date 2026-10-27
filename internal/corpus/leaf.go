// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

package corpus

import "strings"

// Tag statuses.
const (
	TagOK     = "ok"
	TagFailed = "tag_failed"
)

// TagLeaf is one leaf as returned by the tagger, after span location.
type TagLeaf struct {
	SpanText     string `json:"span_text"`
	SpanStart    int    `json:"span_start"` // byte offsets located by the harness, not the model
	SpanEnd      int    `json:"span_end"`
	Operation    string `json:"operation"`
	Domain       string `json:"domain"`
	Needs        []int  `json:"needs"`
	VerifierHint string `json:"verifier_hint"`
}

// TagResult is one row of tags.jsonl.
type TagResult struct {
	RequestID       string    `json:"request_id"`
	Status          string    `json:"status"`
	Error           string    `json:"error,omitempty"`
	Attempts        int       `json:"attempts"`
	Standalone      bool      `json:"standalone"`
	Leaves          []TagLeaf `json:"leaves,omitempty"`
	TaxonomyVersion string    `json:"taxonomy_version"`
	Model           string    `json:"model"`
	TokensIn        int       `json:"tokens_in"`
	TokensOut       int       `json:"tokens_out"`
	CostUSD         float64   `json:"cost_usd"`
}

// Leaf is one row of leaves.jsonl: the unit the matrix is built on.
type Leaf struct {
	LeafID       string `json:"leaf_id"`
	RequestID    string `json:"request_id"`
	LeafIdx      int    `json:"leaf_idx"`
	NLeaves      int    `json:"n_leaves"`
	Operation    string `json:"operation"`
	Domain       string `json:"domain"`
	Class        string `json:"class"`
	SpanText     string `json:"span_text"`
	Needs        []int  `json:"needs,omitempty"`
	VerifierHint string `json:"verifier_hint"`
	// ModelText is the span plus the text of any leaves it needs.
	ModelText       string `json:"model_text"`
	ContextText     string `json:"context_text,omitempty"`
	Standalone      bool   `json:"standalone"`
	Source          string `json:"source"`
	AuthorKind      string `json:"author_kind"`
	Host            string `json:"host,omitempty"`
	Split           string `json:"split"`
	TaxonomyVersion string `json:"taxonomy_version"`
}

// ClassOf joins operation and domain into a class name.
func ClassOf(op, domain string) string { return op + "/" + domain }

// Prompt is exactly what every pool model receives for this leaf: the
// prior context as a preamble when present (the deployment condition,
// PROJECT.md §5), then the leaf text.
func (l Leaf) Prompt() string {
	if strings.TrimSpace(l.ContextText) == "" {
		return l.ModelText
	}
	return "Prior context:\n" + l.ContextText + "\n\n---\n\n" + l.ModelText
}
