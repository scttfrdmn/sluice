// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

package corpus

import (
	"crypto/sha256"
	"encoding/binary"
	"sort"
)

// LeafStratum is the pilot stratum of a leaf: its source, with repos split
// by author_kind, since agent and other issues differ in how they are
// verified.
func LeafStratum(l Leaf) string {
	if l.Source == SourceRepos {
		return SourceRepos + ":" + l.AuthorKind
	}
	return l.Source
}

// DrawPilot draws the matrix+label pilot (runs/<id>/pilot.json): up to
// perStratum leaves per LeafStratum from split, ordered by a seeded hash of
// the leaf id. It uses the Subsample shape; IDs are leaf ids. split "all"
// draws from both splits.
func DrawPilot(leaves []Leaf, perStratum int, split string, seed uint64) *Subsample {
	s := &Subsample{Seed: seed, PerStratum: perStratum, Split: split, Available: map[string]int{}, Selected: map[string]int{}}
	by := map[string][]Leaf{}
	for _, l := range leaves {
		if split != "all" && l.Split != split {
			continue
		}
		st := LeafStratum(l)
		by[st] = append(by[st], l)
		s.Available[st]++
	}
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], seed)
	key := func(id string) string {
		h := sha256.Sum256(append(b[:], id...))
		return string(h[:])
	}
	for st, ls := range by {
		sort.Slice(ls, func(i, j int) bool { return key(ls[i].LeafID) < key(ls[j].LeafID) })
		n := min(perStratum, len(ls))
		for _, l := range ls[:n] {
			s.IDs = append(s.IDs, l.LeafID)
		}
		s.Selected[st] = n
	}
	sort.Strings(s.IDs)
	return s
}

// FilterLeaves keeps the leaves in ids; nil ids keeps all.
func FilterLeaves(leaves []Leaf, ids map[string]bool) []Leaf {
	if ids == nil {
		return leaves
	}
	var out []Leaf
	for _, l := range leaves {
		if ids[l.LeafID] {
			out = append(out, l)
		}
	}
	return out
}
