// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

// Package tagger segments a request into leaves {span, class, needs}
// (PROJECT.md §3 "Tagger", §4 E1).
//
// For E0 the strong model is the tagger (StrongModel). E1 distils its labels
// into a small encoder behind the Encoder interface; that implementation does
// not exist yet.
package tagger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/template"
	"unicode"

	"github.com/scttfrdmn/sluice/internal/cache"
	"github.com/scttfrdmn/sluice/internal/config"
	"github.com/scttfrdmn/sluice/internal/corpus"
	"github.com/scttfrdmn/sluice/internal/llm"
	"github.com/scttfrdmn/sluice/internal/spend"
)

// Tagger is anything that tags a request.
type Tagger interface {
	Tag(ctx context.Context, r corpus.Record) (corpus.TagResult, error)
}

// Encoder is the E1 tagger: a ≤400M-parameter encoder with a
// token-classification head for class and a pairwise head for needs edges,
// trained on StrongModel labels (PROJECT.md §3, §4 E1). Not implemented.
type Encoder interface {
	Tagger
	// SpanF1 and EdgePR are the E1 evaluation metrics against strong-model labels.
	SpanF1(gold, pred []corpus.TagResult) float64
	EdgePR(gold, pred []corpus.TagResult) (precision, recall float64)
}

// StrongModel tags with the configured strong model and a prompt template.
type StrongModel struct {
	cfg     *config.Config
	model   config.Model
	client  llm.Client
	cache   *cache.Cache
	tmpl    *template.Template
	tmplSrc string
	// Spend, if set, receives the measured cost of every model call.
	Spend *spend.Budget
}

// NewStrongModel loads the tag prompt template.
func NewStrongModel(cfg *config.Config, client llm.Client, c *cache.Cache) (*StrongModel, error) {
	m, _ := cfg.Model(cfg.Strong)
	src, err := os.ReadFile(cfg.Path(cfg.Prompts.Tag))
	if err != nil {
		return nil, err
	}
	t, err := template.New("tag").Funcs(template.FuncMap{"join": strings.Join}).Parse(string(src))
	if err != nil {
		return nil, err
	}
	return &StrongModel{cfg: cfg, model: m, client: client, cache: c, tmpl: t, tmplSrc: string(src)}, nil
}

type tagOutput struct {
	Standalone *bool `json:"standalone"`
	Leaves     []struct {
		SpanText     string `json:"span_text"`
		Operation    string `json:"operation"`
		Domain       string `json:"domain"`
		Needs        []int  `json:"needs"`
		VerifierHint string `json:"verifier_hint"`
	} `json:"leaves"`
}

// Tag tags one request. Invalid output is retried once with the error
// appended; a second failure yields Status tag_failed. A transport error is
// returned as an error and nothing is cached, so a rerun retries it.
func (s *StrongModel) Tag(ctx context.Context, r corpus.Record) (corpus.TagResult, error) {
	tx := s.cfg.Taxonomy
	key := cache.Key("tag", tx.Version, s.tmplSrc, s.model.ID, r.Text, r.ContextText)
	var res corpus.TagResult
	if ok, err := s.cache.Get(key, &res); err != nil || ok {
		res.RequestID = r.ID
		return res, err
	}
	var sb strings.Builder
	err := s.tmpl.Execute(&sb, map[string]any{
		"Version": tx.Version, "Operations": tx.Operations, "Domains": tx.Domains,
		"MaxLeaves": tx.MaxLeaves, "Text": r.Text, "Context": r.ContextText,
	})
	if err != nil {
		return res, err
	}
	prompt := sb.String()
	res = corpus.TagResult{RequestID: r.ID, TaxonomyVersion: tx.Version, Model: s.model.Name}
	for attempt := 0; attempt < 2; attempt++ {
		res.Attempts = attempt + 1
		resp, err := s.client.Invoke(ctx, s.model, llm.Request{
			Purpose: llm.PurposeTag, Prompt: prompt, MaxTokens: tx.TagMaxTokens,
			Meta: map[string]string{"text": r.Text, "context": r.ContextText, "attempt": strconv.Itoa(attempt)},
		})
		if err != nil {
			return res, err
		}
		res.TokensIn += resp.TokensIn
		res.TokensOut += resp.TokensOut
		c, _ := s.model.Cost(resp.TokensIn, resp.TokensOut, false)
		res.CostUSD += c
		if s.Spend != nil {
			s.Spend.Add(c)
		}
		leaves, standalone, verr := s.validate(r.Text, resp.Text)
		if verr == nil {
			res.Status, res.Error, res.Standalone, res.Leaves = corpus.TagOK, "", standalone, leaves
			break
		}
		res.Status, res.Error = corpus.TagFailed, verr.Error()
		prompt = sb.String() + "\n\nYour previous output was invalid: " + verr.Error() +
			"\nPrevious output:\n" + resp.Text + "\n\nReturn the corrected JSON only."
	}
	return res, s.cache.Put(key, res)
}

// ExtractJSON returns the outermost {...} object in s, tolerating a code
// fence or stray prose around it.
func ExtractJSON(s string) string {
	i, j := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if i < 0 || j < i {
		return s
	}
	return s[i : j+1]
}

func (s *StrongModel) validate(text, out string) ([]corpus.TagLeaf, bool, error) {
	tx := s.cfg.Taxonomy
	var o tagOutput
	if err := json.Unmarshal([]byte(ExtractJSON(out)), &o); err != nil {
		return nil, false, fmt.Errorf("not valid JSON: %v", err)
	}
	if o.Standalone == nil {
		return nil, false, errors.New("missing standalone")
	}
	if len(o.Leaves) == 0 {
		return nil, false, errors.New("no leaves")
	}
	if len(o.Leaves) > tx.MaxLeaves {
		return nil, false, fmt.Errorf("%d leaves exceeds max %d", len(o.Leaves), tx.MaxLeaves)
	}
	var leaves []corpus.TagLeaf
	for i, l := range o.Leaves {
		if !tx.ValidOperation(l.Operation) {
			return nil, false, fmt.Errorf("leaf %d: operation %q not in taxonomy", i, l.Operation)
		}
		if !tx.ValidDomain(l.Domain) {
			return nil, false, fmt.Errorf("leaf %d: domain %q not in taxonomy", i, l.Domain)
		}
		if !tx.ValidHint(l.VerifierHint) {
			return nil, false, fmt.Errorf("leaf %d: verifier_hint %q not allowed", i, l.VerifierHint)
		}
		for _, n := range l.Needs {
			if n < 0 || n >= i {
				return nil, false, fmt.Errorf("leaf %d: needs index %d must refer to an earlier leaf", i, n)
			}
		}
		start, end, ok := Locate(text, l.SpanText)
		if !ok {
			return nil, false, fmt.Errorf("leaf %d: span_text not found verbatim in the request", i)
		}
		leaves = append(leaves, corpus.TagLeaf{
			SpanText: text[start:end], SpanStart: start, SpanEnd: end,
			Operation: l.Operation, Domain: l.Domain, Needs: l.Needs, VerifierHint: l.VerifierHint,
		})
	}
	return leaves, *o.Standalone, nil
}

// Locate finds span in text with whitespace runs normalized on both sides
// and returns byte offsets into the original text.
func Locate(text, span string) (start, end int, ok bool) {
	normSpan := strings.Join(strings.Fields(span), " ")
	if normSpan == "" {
		return 0, 0, false
	}
	// Build the normalized text with a map back to original byte offsets.
	var norm strings.Builder
	var pos []int // pos[k] = original byte offset of normalized byte k
	inSpace := true
	for i, r := range text {
		if unicode.IsSpace(r) {
			if !inSpace {
				norm.WriteByte(' ')
				pos = append(pos, i)
				inSpace = true
			}
			continue
		}
		inSpace = false
		n := norm.Len()
		norm.WriteRune(r)
		for k := n; k < norm.Len(); k++ {
			pos = append(pos, i+(k-n))
		}
	}
	idx := strings.Index(norm.String(), normSpan)
	if idx < 0 {
		return 0, 0, false
	}
	last := idx + len(normSpan) - 1
	start = pos[idx]
	end = pos[last] + 1
	// Extend end to the end of the final rune.
	for end < len(text) && (text[end]&0xC0) == 0x80 {
		end++
	}
	return start, end, true
}

// Leaves expands tag results into matrix leaves.
func Leaves(records map[string]corpus.Record, tags []corpus.TagResult) []corpus.Leaf {
	var out []corpus.Leaf
	for _, t := range tags {
		if t.Status != corpus.TagOK {
			continue
		}
		r := records[t.RequestID]
		for i, l := range t.Leaves {
			out = append(out, corpus.Leaf{
				LeafID:          fmt.Sprintf("%s-%d", t.RequestID, i),
				RequestID:       t.RequestID,
				LeafIdx:         i,
				NLeaves:         len(t.Leaves),
				Operation:       l.Operation,
				Domain:          l.Domain,
				Class:           corpus.ClassOf(l.Operation, l.Domain),
				SpanText:        l.SpanText,
				Needs:           l.Needs,
				VerifierHint:    l.VerifierHint,
				ModelText:       modelText(t.Leaves, i),
				ContextText:     r.ContextText,
				Standalone:      t.Standalone,
				Source:          r.Source,
				AuthorKind:      r.AuthorKind,
				Host:            r.Host,
				Split:           r.Split,
				TaxonomyVersion: t.TaxonomyVersion,
			})
		}
	}
	return out
}

// modelText is the leaf span preceded by the spans of the leaves it needs.
func modelText(leaves []corpus.TagLeaf, i int) string {
	l := leaves[i]
	if len(l.Needs) == 0 {
		return l.SpanText
	}
	var b strings.Builder
	b.WriteString("Related earlier part of the same request:\n")
	for _, n := range l.Needs {
		b.WriteString(leaves[n].SpanText)
		b.WriteString("\n\n")
	}
	b.WriteString("Task:\n")
	b.WriteString(l.SpanText)
	return b.String()
}
