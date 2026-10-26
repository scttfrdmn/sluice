// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"regexp"
	"strings"

	"github.com/scttfrdmn/sluice/internal/config"
)

// Fake is a deterministic offline backend for the testdata pipeline
// (`make e0`). It never calls a network. Each model has a latent accuracy
// per class, so the downstream statistics are non-trivial; outputs carry
// markers ([[fake:correct]] etc.) that the fake judge and fake sandbox read.
// Nothing it produces is a measurement.
type Fake struct{ cfg *config.Config }

// NewFake returns the fake backend.
func NewFake(cfg *config.Config) *Fake { return &Fake{cfg: cfg} }

func unit(parts ...string) float64 {
	h := fnv.New64a()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return float64(h.Sum64()>>11) / float64(1<<53)
}

var tierBase = map[string]float64{"S": 0.55, "M": 0.72, "L": 0.84}

// accuracy is the fake model's latent accuracy on a class.
func accuracy(m config.Model, class string) float64 {
	p := tierBase[m.Tier] + 0.36*(unit("skill", m.Provider, class)-0.5) + 0.1*(unit("skill", m.Name, class)-0.5)
	return min(0.97, max(0.05, p))
}

// Invoke answers by purpose.
func (f *Fake) Invoke(_ context.Context, m config.Model, req Request) (Response, error) {
	var text string
	switch req.Purpose {
	case PurposeTag:
		text = fakeTag(req.Meta["text"], req.Meta["context"], req.Meta["attempt"])
	case PurposeReclass:
		op, dom, hint := classify(req.Meta["text"])
		b, _ := json.Marshal(map[string]string{"operation": op, "domain": dom, "verifier_hint": hint})
		text = string(b)
	case PurposeNumeric:
		leaf := req.Meta["leaf_id"]
		n := fakeNumber(leaf)
		if req.Draw == 1 && unit("noref", leaf) < 0.2 {
			n++ // the two reference draws disagree for some leaves
		}
		text = fmt.Sprintf("Working.\nANSWER: %d", n)
	case PurposeTest:
		if unit("untestable", req.Meta["leaf_id"]) < 0.25 {
			text = `{"testable": false, "test_code": "", "reason": "fake: depends on external context"}`
		} else {
			text = `{"testable": true, "test_code": "# fake test", "reason": "fake"}`
		}
	case PurposeJudge:
		correct := strings.Contains(req.Prompt, "[[fake:correct]]") || strings.Contains(req.Prompt, "[[fake:pass]]")
		if unit("judge-flip", m.Name, req.Prompt) < 0.08 {
			correct = !correct
		}
		text = fmt.Sprintf(`{"correct": %v, "reason": "fake judge"}`, correct)
	default:
		text = fakeAnswer(m, req)
	}
	in, out := len(req.Prompt)/4+1, len(text)/4+1
	return Response{Text: text, TokensIn: in, TokensOut: out, LatencyMs: int64(150 + 8*out + int(400*unit("lat", m.Name, req.Prompt))), StopReason: "end_turn"}, nil
}

// RunBatch simulates a batch job by invoking each item.
func (f *Fake) RunBatch(ctx context.Context, m config.Model, items []BatchItem) (map[string]BatchResult, int64, error) {
	out := make(map[string]BatchResult, len(items))
	var wall int64 = 60_000
	for _, it := range items {
		r, err := f.Invoke(ctx, m, it.Request)
		if err != nil {
			out[it.RecordID] = BatchResult{Err: err.Error()}
			continue
		}
		r.LatencyMs = 0
		out[it.RecordID] = BatchResult{Response: r}
		wall += 50
	}
	return out, wall, nil
}

func fakeNumber(leaf string) int { return 10 + int(unit("num", leaf)*990) }

func fakeAnswer(m config.Model, req Request) string {
	class, leaf, hint := req.Meta["class"], req.Meta["leaf_id"], req.Meta["hint"]
	p := accuracy(m, class)
	ok := unit("ans", m.Name, leaf) < p
	if req.Draw == 1 && unit("flip", m.Name, leaf) < 0.06 {
		ok = !ok
	}
	marker := "[[fake:wrong]]"
	if ok {
		marker = "[[fake:correct]]"
	}
	switch {
	case hint == "numeric":
		n := fakeNumber(leaf)
		if !ok {
			n += 1 + int(unit("off", m.Name, leaf)*5)
		}
		return fmt.Sprintf("Reasoning %s.\nThe answer is %d.", marker, n)
	case strings.HasPrefix(hint, "code:"):
		lang := strings.TrimPrefix(hint, "code:")
		state := "[[fake:pass]]"
		if !ok {
			state = "[[fake:fail]]"
			if unit("nocompile", m.Name, leaf) < 0.3 {
				state = "[[fake:nocompile]]"
			}
		}
		return fmt.Sprintf("Here is the code.\n```%s\n# %s\n```\n", lang, state)
	case strings.HasPrefix(hint, "format:json"):
		if !ok && unit("badjson", m.Name, leaf) < 0.5 {
			return "```json\n{\"result\": \"" + marker + "\",\n```"
		}
		return "```json\n{\"result\": \"" + marker + "\"}\n```"
	}
	return "Response " + marker + "."
}

var (
	reCode   = regexp.MustCompile(`(?i)\b(write|implement|code|function|script)\b`)
	reConv   = regexp.MustCompile(`(?i)\b(convert|translate|rewrite|port)\b`)
	reSum    = regexp.MustCompile(`(?i)\bsummari[sz]e\b`)
	reReason = regexp.MustCompile(`(?i)\b(calculate|compute|how many|how much)\b`)
	reClass  = regexp.MustCompile(`(?i)\b(classify|categori[sz]e)\b`)
	reExtr   = regexp.MustCompile(`(?i)\b(extract|list all|pull out)\b`)
	rePlan   = regexp.MustCompile(`(?i)\b(plan|design|outline)\b`)
	reProse  = regexp.MustCompile(`(?i)\b(email|essay|story|poem|letter|blog)\b`)
	reFact   = regexp.MustCompile(`(?i)^(what|who|when|where|why|explain)\b`)
	reDep    = regexp.MustCompile(`(?i)^(fix that|now |also |and |ok |do that|same )`)
)

// classify is the fake tagger's keyword classifier.
func classify(s string) (op, domain, hint string) {
	t := strings.TrimSpace(s)
	switch {
	case reReason.MatchString(t):
		op, hint = "reason", "numeric"
	case reConv.MatchString(t):
		op, hint = "transform", "none"
		if strings.Contains(strings.ToLower(t), "json") {
			hint = "format:json"
		}
	case reSum.MatchString(t):
		op, hint = "summarize", "none"
	case reClass.MatchString(t):
		op, hint = "classify", "none"
	case reExtr.MatchString(t):
		op, hint = "extract", "format:json"
	case reProse.MatchString(t):
		op, hint = "gen-prose", "none"
	case reCode.MatchString(t):
		op, hint = "gen-code", "code:python"
		if strings.Contains(t, " Go ") || strings.Contains(t, " in Go") {
			hint = "code:go"
		}
	case rePlan.MatchString(t):
		op, hint = "plan", "none"
	case reFact.MatchString(t):
		op, hint = "answer-factual", "none"
	default:
		op, hint = "converse", "none"
	}
	low := strings.ToLower(t)
	switch {
	case op == "gen-code" || strings.Contains(low, "code") || strings.Contains(low, "function"):
		domain = "code"
	case strings.Contains(low, "contract") || strings.Contains(low, "tax"):
		domain = "legal-finance"
	case strings.Contains(low, "story") || strings.Contains(low, "poem"):
		domain = "creative"
	case strings.Contains(low, "physics") || strings.Contains(low, "protein") || strings.Contains(low, "orbit"):
		domain = "sci-tech"
	default:
		domain = "general"
	}
	return op, domain, hint
}

// fakeTag segments on blank lines (at most four leaves), each leaf after
// the first needing the one before it. On attempt 0 it deliberately returns
// an invalid span for one request so the retry path is exercised.
func fakeTag(text, context, attempt string) string {
	var paras []string
	for _, p := range strings.Split(text, "\n\n") {
		if strings.TrimSpace(p) != "" {
			paras = append(paras, strings.TrimSpace(p))
		}
	}
	if len(paras) > 4 {
		paras = append(paras[:3], strings.Join(paras[3:], "\n\n"))
	}
	type leaf struct {
		Span   string `json:"span_text"`
		Op     string `json:"operation"`
		Domain string `json:"domain"`
		Needs  []int  `json:"needs"`
		Hint   string `json:"verifier_hint"`
	}
	var leaves []leaf
	for i, p := range paras {
		op, dom, hint := classify(p)
		var needs []int
		if i > 0 {
			needs = []int{i - 1}
		}
		leaves = append(leaves, leaf{Span: p, Op: op, Domain: dom, Needs: needs, Hint: hint})
	}
	if attempt == "0" && len(leaves) > 0 && unit("badspan", text) < 0.1 {
		leaves[0].Span = "this text is not in the request"
	}
	standalone := context == "" || !reDep.MatchString(text)
	b, _ := json.Marshal(map[string]any{"standalone": standalone, "leaves": leaves})
	return string(b)
}
