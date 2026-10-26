// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

// Package label labels every matrix cell correct or incorrect with the best
// verifier rung available for its class (PROJECT.md §6):
//
//	rung 1  code: run the strong model's test; numeric reason: exact match
//	rung 2  code: compile only; format/extract: parse check + judges
//	rung 3  two judges, one per provider, neither the judged model
package label

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
	"github.com/scttfrdmn/sluice/internal/matrix"
	"github.com/scttfrdmn/sluice/internal/tagger"
)

// Methods recorded on each label.
const (
	MethodExecTest     = "exec-test"
	MethodCompileOnly  = "compile-only"
	MethodNoCode       = "no-code-block"
	MethodNumericExact = "numeric-exact"
	MethodFormatFail   = "format-parse-fail"
	MethodFormatJudge  = "format+judge2"
	MethodJudge        = "judge2"
	MethodJudgeFailed  = "judge-failed"
)

// Label is one row of labels.jsonl.
type Label struct {
	LeafID string `json:"leaf_id"`
	Model  string `json:"model"`
	Draw   int    `json:"draw"`
	// Correct is nil when no label could be produced (judge failure).
	Correct        *bool    `json:"correct"`
	Rung           int      `json:"rung"`
	Method         string   `json:"method"`
	JudgeAgreement *bool    `json:"judge_agreement,omitempty"`
	Judges         []string `json:"judges,omitempty"`
	JudgeVerdicts  []*bool  `json:"judge_verdicts,omitempty"`
	JudgeReasons   []string `json:"judge_reasons,omitempty"`
	Detail         string   `json:"detail,omitempty"`
	CostUSD        float64  `json:"cost_usd"`
}

// Key matches matrix.CellKey.
func (l Label) Key() string { return matrix.CellKey(l.LeafID, l.Model, l.Draw) }

// Ref is one row of refs.jsonl: the per-leaf reference generated once by
// the strong model and held fixed across models.
type Ref struct {
	LeafID   string  `json:"leaf_id"`
	Kind     string  `json:"kind"`   // test | numeric
	Status   string  `json:"status"` // ok | untestable | no_reference | unsupported_language | failed
	Language string  `json:"language,omitempty"`
	Test     string  `json:"test,omitempty"`
	Answer   float64 `json:"answer,omitempty"`
	Reason   string  `json:"reason,omitempty"`
	CostUSD  float64 `json:"cost_usd"`
}

// Labeler holds everything needed to label cells.
type Labeler struct {
	cfg     *config.Config
	client  llm.Client
	cache   *cache.Cache
	sandbox Sandbox
	strong  config.Model
	judgeT  *template.Template
	testT   *template.Template
	numT    *template.Template
	srcs    string // concatenated template sources, part of every cache key
}

// New builds a labeler.
func New(cfg *config.Config, client llm.Client, c *cache.Cache, sb Sandbox) (*Labeler, error) {
	l := &Labeler{cfg: cfg, client: client, cache: c, sandbox: sb}
	l.strong, _ = cfg.Model(cfg.Strong)
	for _, t := range []struct {
		dst  **template.Template
		file string
	}{{&l.judgeT, cfg.Prompts.Judge}, {&l.testT, cfg.Prompts.Test}, {&l.numT, cfg.Prompts.Numeric}} {
		b, err := os.ReadFile(cfg.Path(t.file))
		if err != nil {
			return nil, err
		}
		tt, err := template.New(t.file).Parse(string(b))
		if err != nil {
			return nil, err
		}
		*t.dst = tt
		l.srcs += string(b)
	}
	return l, nil
}

func render(t *template.Template, v any) string {
	var sb strings.Builder
	if err := t.Execute(&sb, v); err != nil {
		panic(err) // templates are validated at load; data is plain strings
	}
	return sb.String()
}

// Kind returns which reference a leaf needs, if any, and the rung family.
func Kind(l corpus.Leaf) string {
	switch {
	case strings.HasPrefix(l.VerifierHint, "code:") && (l.Operation == "gen-code" || l.Operation == "transform"):
		return "code"
	case l.Operation == "reason" && l.VerifierHint == "numeric":
		return "numeric"
	case (l.Operation == "transform" && strings.HasPrefix(l.VerifierHint, "format:")) || l.Operation == "extract":
		return "format"
	}
	return "judge"
}

// call invokes a model through the cache.
func (lb *Labeler) call(ctx context.Context, m config.Model, req llm.Request) (llm.Response, float64, error) {
	key := cache.Key(string(req.Purpose), m.ID, req.Prompt, fmt.Sprint(req.Draw))
	var r llm.Response
	if ok, err := lb.cache.Get(key, &r); err != nil {
		return r, 0, err
	} else if ok {
		return r, 0, nil // already paid for
	}
	r, err := lb.client.Invoke(ctx, m, req)
	if err != nil {
		return r, 0, err
	}
	cost, _ := m.Cost(r.TokensIn, r.TokensOut, false)
	return r, cost, lb.cache.Put(key, r)
}

// MakeRef generates the reference for a code or numeric leaf.
func (lb *Labeler) MakeRef(ctx context.Context, l corpus.Leaf) (Ref, error) {
	switch Kind(l) {
	case "code":
		lang := NormLang(strings.TrimPrefix(l.VerifierHint, "code:"))
		ref := Ref{LeafID: l.LeafID, Kind: "test", Language: lang}
		if !testLangs[lang] {
			ref.Status = "unsupported_language"
			if compileLangs[lang] {
				ref.Reason = "compile-only language"
			}
			return ref, nil
		}
		prompt := render(lb.testT, map[string]string{"Language": lang, "Prompt": l.Prompt()})
		for attempt := 0; attempt < 2; attempt++ {
			r, cost, err := lb.call(ctx, lb.strong, llm.Request{Purpose: llm.PurposeTest, Prompt: prompt, MaxTokens: lb.cfg.Label.RefMaxTokens, Draw: attempt, Meta: map[string]string{"leaf_id": l.LeafID}})
			ref.CostUSD += cost
			if err != nil {
				return ref, err
			}
			var o struct {
				Testable *bool  `json:"testable"`
				TestCode string `json:"test_code"`
				Reason   string `json:"reason"`
			}
			if json.Unmarshal([]byte(tagger.ExtractJSON(r.Text)), &o) != nil || o.Testable == nil {
				ref.Status, ref.Reason = "failed", "test generator did not return valid JSON"
				continue
			}
			ref.Reason = o.Reason
			if !*o.Testable || strings.TrimSpace(o.TestCode) == "" {
				ref.Status = "untestable"
			} else {
				ref.Status, ref.Test = "ok", o.TestCode
			}
			break
		}
		return ref, nil
	case "numeric":
		ref := Ref{LeafID: l.LeafID, Kind: "numeric"}
		prompt := render(lb.numT, map[string]string{"Prompt": l.Prompt()})
		var vals [2]float64
		for d := 0; d < 2; d++ {
			r, cost, err := lb.call(ctx, lb.strong, llm.Request{Purpose: llm.PurposeNumeric, Prompt: prompt, MaxTokens: lb.cfg.Label.RefMaxTokens, Draw: d, Meta: map[string]string{"leaf_id": l.LeafID}})
			ref.CostUSD += cost
			if err != nil {
				return ref, err
			}
			v, ok := FinalNumber(r.Text)
			if !ok {
				ref.Status, ref.Reason = "no_reference", fmt.Sprintf("draw %d has no numeric answer", d)
				return ref, nil
			}
			vals[d] = v
		}
		if !NumEqual(vals[0], vals[1]) {
			ref.Status, ref.Reason = "no_reference", fmt.Sprintf("reference draws disagree: %v vs %v", vals[0], vals[1])
			return ref, nil
		}
		ref.Status, ref.Answer = "ok", vals[0]
		return ref, nil
	}
	return Ref{}, nil
}

// LabelCell labels one cell given its leaf and (possibly empty) reference.
func (lb *Labeler) LabelCell(ctx context.Context, l corpus.Leaf, c matrix.Cell, ref Ref) (Label, error) {
	lab := Label{LeafID: c.LeafID, Model: c.Model, Draw: int(c.Draw)}
	set := func(v bool, rung int, method, detail string) {
		lab.Correct, lab.Rung, lab.Method, lab.Detail = &v, rung, method, detail
	}
	switch Kind(l) {
	case "code":
		lang := NormLang(strings.TrimPrefix(l.VerifierHint, "code:"))
		if !compileLangs[lang] {
			break // falls to judges below
		}
		_, code, found := FirstCodeBlock(c.Output)
		test := ""
		rung, method := 2, MethodCompileOnly
		if ref.Status == "ok" {
			test, rung, method = ref.Test, 1, MethodExecTest
		}
		if !found {
			set(false, rung, MethodNoCode, "no fenced code block in output")
			return lab, nil
		}
		res, err := lb.sandbox.Run(ctx, lang, code, test)
		if err != nil {
			return lab, err
		}
		ok := res.Compiled && !res.TimedOut
		if test != "" {
			ok = ok && res.Passed
		}
		set(ok, rung, method, fmt.Sprintf("compiled=%v passed=%v timeout=%v", res.Compiled, res.Passed, res.TimedOut))
		return lab, nil
	case "numeric":
		if ref.Status == "ok" {
			v, found := FinalNumber(c.Output)
			set(found && NumEqual(v, ref.Answer), 1, MethodNumericExact, fmt.Sprintf("got=%v found=%v", v, found))
			return lab, nil
		}
		// no_reference: judges, recorded as such below.
		lab.Detail = "numeric no_reference"
	case "format":
		if f, ok := strings.CutPrefix(l.VerifierHint, "format:"); ok {
			if parsed, known := FormatOK(f, c.Output); known {
				if !parsed {
					set(false, 2, MethodFormatFail, "does not parse as "+f)
					return lab, nil
				}
				if err := lb.judge(ctx, l, c, &lab); err != nil {
					return lab, err
				}
				if lab.Method == MethodJudge {
					lab.Rung, lab.Method = 2, MethodFormatJudge
				}
				return lab, nil
			}
		}
	}
	return lab, lb.judge(ctx, l, c, &lab)
}

// judge runs the two-judge rung and fills lab.
func (lb *Labeler) judge(ctx context.Context, l corpus.Leaf, c matrix.Cell, lab *Label) error {
	judges, err := lb.cfg.JudgesFor(c.Model)
	if err != nil {
		return err
	}
	prompt := render(lb.judgeT, map[string]string{"Prompt": l.Prompt(), "Output": c.Output, "Reference": ""})
	var verdicts []*bool
	for _, j := range judges {
		lab.Judges = append(lab.Judges, j.Name)
		var verdict *bool
		reason := ""
		p := prompt
		for attempt := 0; attempt < 2 && verdict == nil; attempt++ {
			r, cost, err := lb.call(ctx, j, llm.Request{Purpose: llm.PurposeJudge, Prompt: p, MaxTokens: lb.cfg.Judges.MaxTokens, Draw: attempt})
			lab.CostUSD += cost
			if err != nil {
				return err
			}
			var o struct {
				Correct *bool  `json:"correct"`
				Reason  string `json:"reason"`
			}
			if json.Unmarshal([]byte(tagger.ExtractJSON(r.Text)), &o) == nil && o.Correct != nil {
				verdict, reason = o.Correct, o.Reason
				break
			}
			p = prompt + "\n\nYour previous reply was not the required JSON. Reply with only {\"correct\": true|false, \"reason\": \"...\"}."
			reason = "invalid judge JSON: " + clip(r.Text, 200)
		}
		verdicts = append(verdicts, verdict)
		lab.JudgeReasons = append(lab.JudgeReasons, reason)
	}
	lab.JudgeVerdicts = verdicts
	lab.Rung = 3
	for _, v := range verdicts {
		if v == nil {
			lab.Method = MethodJudgeFailed
			return nil
		}
	}
	all, agree := true, true
	for _, v := range verdicts {
		all = all && *v
		agree = agree && *v == *verdicts[0]
	}
	lab.Correct, lab.JudgeAgreement, lab.Method = &all, &agree, MethodJudge
	return nil
}
