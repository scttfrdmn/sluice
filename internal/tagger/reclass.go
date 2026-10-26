// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

package tagger

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/template"

	"github.com/scttfrdmn/sluice/internal/cache"
	"github.com/scttfrdmn/sluice/internal/config"
	"github.com/scttfrdmn/sluice/internal/corpus"
	"github.com/scttfrdmn/sluice/internal/llm"
)

// Reclassifier assigns new-taxonomy classes to existing leaves without
// re-segmenting, so a taxonomy change is a re-tag over the same matrix
// (PROJECT.md §7): leaf IDs and spans are unchanged.
type Reclassifier struct {
	cfg     *config.Config
	model   config.Model
	client  llm.Client
	cache   *cache.Cache
	tmpl    *template.Template
	tmplSrc string
}

// NewReclassifier loads the reclass prompt template.
func NewReclassifier(cfg *config.Config, client llm.Client, c *cache.Cache) (*Reclassifier, error) {
	m, _ := cfg.Model(cfg.Strong)
	src, err := os.ReadFile(cfg.Path(cfg.Prompts.Reclass))
	if err != nil {
		return nil, err
	}
	t, err := template.New("reclass").Funcs(template.FuncMap{"join": strings.Join}).Parse(string(src))
	if err != nil {
		return nil, err
	}
	return &Reclassifier{cfg: cfg, model: m, client: client, cache: c, tmpl: t, tmplSrc: string(src)}, nil
}

// Reclass returns l with operation, domain and hint from the configured
// taxonomy. A leaf that cannot be reclassified after one retry is returned
// with ok false.
func (rc *Reclassifier) Reclass(ctx context.Context, l corpus.Leaf) (corpus.Leaf, bool, error) {
	tx := rc.cfg.Taxonomy
	key := cache.Key("reclass", tx.Version, rc.tmplSrc, rc.model.ID, l.SpanText)
	var got struct {
		Operation, Domain, VerifierHint string
		OK                              bool
	}
	if ok, err := rc.cache.Get(key, &got); err != nil {
		return l, false, err
	} else if !ok {
		var sb strings.Builder
		if err := rc.tmpl.Execute(&sb, map[string]any{"Version": tx.Version, "Operations": tx.Operations, "Domains": tx.Domains, "Text": l.SpanText}); err != nil {
			return l, false, err
		}
		prompt := sb.String()
		for attempt := 0; attempt < 2 && !got.OK; attempt++ {
			resp, err := rc.client.Invoke(ctx, rc.model, llm.Request{Purpose: llm.PurposeReclass, Prompt: prompt, MaxTokens: 200, Meta: map[string]string{"text": l.SpanText}})
			if err != nil {
				return l, false, err
			}
			var o struct {
				Operation    string `json:"operation"`
				Domain       string `json:"domain"`
				VerifierHint string `json:"verifier_hint"`
			}
			verr := json.Unmarshal([]byte(ExtractJSON(resp.Text)), &o)
			switch {
			case verr != nil:
			case !tx.ValidOperation(o.Operation):
				verr = fmt.Errorf("operation %q not in taxonomy", o.Operation)
			case !tx.ValidDomain(o.Domain):
				verr = fmt.Errorf("domain %q not in taxonomy", o.Domain)
			case !tx.ValidHint(o.VerifierHint):
				verr = fmt.Errorf("verifier_hint %q not allowed", o.VerifierHint)
			}
			if verr == nil {
				got.Operation, got.Domain, got.VerifierHint, got.OK = o.Operation, o.Domain, o.VerifierHint, true
				break
			}
			prompt = sb.String() + "\n\nYour previous output was invalid: " + verr.Error() + "\nReturn the corrected JSON only."
		}
		if err := rc.cache.Put(key, got); err != nil {
			return l, false, err
		}
	}
	if !got.OK {
		return l, false, nil
	}
	l.Operation, l.Domain, l.VerifierHint = got.Operation, got.Domain, got.VerifierHint
	l.Class = corpus.ClassOf(got.Operation, got.Domain)
	l.TaxonomyVersion = tx.Version
	return l, true, nil
}
