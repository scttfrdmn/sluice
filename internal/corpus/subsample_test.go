// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

package corpus

import (
	"fmt"
	"slices"
	"testing"
)

func TestDrawSubsample(t *testing.T) {
	var recs []Record
	add := func(src, ref string, n int) {
		for i := range n {
			split := "calibration"
			if i%2 == 1 {
				split = "test"
			}
			recs = append(recs, Record{ID: fmt.Sprintf("%s-%d", ref, i), Source: src, OriginRef: fmt.Sprintf("%s%d", ref, i), Split: split})
		}
	}
	add(SourceClaudeCode, "cc:", 50)
	add(SourceRepos, "repos:issue:", 30)
	add(SourceRepos, "repos:file:", 6)
	add(SourceWildchat, "wc:", 8)
	s := DrawSubsample(recs, 10, "calibration", 7)
	want := map[string]int{SourceClaudeCode: 10, "repos:issue": 10, "repos:prompt-file": 3, SourceWildchat: 4}
	for k, n := range want {
		if s.Selected[k] != n {
			t.Errorf("%s: selected %d, want %d", k, s.Selected[k], n)
		}
	}
	if s.Available[SourceClaudeCode] != 25 {
		t.Errorf("available = %v", s.Available)
	}
	if len(s.IDs) != 27 {
		t.Errorf("%d ids", len(s.IDs))
	}
	if again := DrawSubsample(recs, 10, "calibration", 7); !slices.Equal(again.IDs, s.IDs) {
		t.Error("same seed drew a different subsample")
	}
	if other := DrawSubsample(recs, 10, "calibration", 8); slices.Equal(other.IDs, s.IDs) {
		t.Error("different seed drew the same subsample")
	}
	ids := map[string]bool{}
	for _, id := range s.IDs {
		ids[id] = true
	}
	for _, r := range FilterSubsample(recs, ids) {
		if r.Split != "calibration" {
			t.Errorf("%s from split %s", r.ID, r.Split)
		}
	}
}
