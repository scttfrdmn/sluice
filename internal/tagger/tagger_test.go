// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

package tagger

import "testing"

func TestLocate(t *testing.T) {
	text := "First, summarize   the report.\n\nThen\twrite an email to the team."
	cases := []struct {
		span string
		ok   bool
		want string
	}{
		{"summarize the report.", true, "summarize   the report."},
		{"Then write an email", true, "Then\twrite an email"},
		{"write an  email to the team.", true, "write an email to the team."},
		{"rewrite the report", false, ""},
		{"   ", false, ""},
	}
	for _, c := range cases {
		s, e, ok := Locate(text, c.span)
		if ok != c.ok {
			t.Errorf("Locate(%q) ok = %v", c.span, ok)
			continue
		}
		if ok && text[s:e] != c.want {
			t.Errorf("Locate(%q) = %q, want %q", c.span, text[s:e], c.want)
		}
	}
	// Multibyte runes at the span end.
	s, e, ok := Locate("dit is één zin", "één")
	if !ok || "dit is één zin"[s:e] != "één" {
		t.Errorf("multibyte locate failed: %v %q", ok, "dit is één zin"[s:e])
	}
}
