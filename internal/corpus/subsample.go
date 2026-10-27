// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

package corpus

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
)

// Subsample is the E0 subsample (runs/<id>/subsample.json): a seeded,
// stratified draw of PerStratum requests per stratum from one split. E0
// tags, runs and labels only these requests; the full corpus is reserved
// for E1 tagger training and E2.
type Subsample struct {
	Seed       uint64         `json:"seed"`
	PerStratum int            `json:"per_stratum"`
	Split      string         `json:"split"`
	Available  map[string]int `json:"available"`
	Selected   map[string]int `json:"selected"`
	IDs        []string       `json:"ids"`
}

// Stratum is the subsample stratum of a record: its source, with repos
// split into issue bodies and prompt files.
func Stratum(r Record) string {
	if r.Source == SourceRepos {
		if strings.HasPrefix(r.OriginRef, "repos:file:") {
			return "repos:prompt-file"
		}
		return "repos:issue"
	}
	return r.Source
}

// DrawSubsample orders each stratum by a seeded hash of the record id and
// keeps the first perStratum. split "all" draws from both splits.
func DrawSubsample(recs []Record, perStratum int, split string, seed uint64) *Subsample {
	s := &Subsample{Seed: seed, PerStratum: perStratum, Split: split, Available: map[string]int{}, Selected: map[string]int{}}
	by := map[string][]Record{}
	for _, r := range recs {
		if split != "all" && r.Split != split {
			continue
		}
		st := Stratum(r)
		by[st] = append(by[st], r)
		s.Available[st]++
	}
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], seed)
	key := func(id string) string {
		h := sha256.Sum256(append(b[:], id...))
		return string(h[:])
	}
	for st, rs := range by {
		sort.Slice(rs, func(i, j int) bool { return key(rs[i].ID) < key(rs[j].ID) })
		n := min(perStratum, len(rs))
		for _, r := range rs[:n] {
			s.IDs = append(s.IDs, r.ID)
		}
		s.Selected[st] = n
	}
	sort.Strings(s.IDs)
	return s
}

// WriteSubsample writes s as indented JSON.
func WriteSubsample(path string, s *Subsample) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// ReadSubsample returns the subsample id set, or nil if path does not exist.
func ReadSubsample(path string) (map[string]bool, *Subsample, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	} else if err != nil {
		return nil, nil, err
	}
	var s Subsample
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	ids := make(map[string]bool, len(s.IDs))
	for _, id := range s.IDs {
		ids[id] = true
	}
	return ids, &s, nil
}

// FilterSubsample keeps the records in ids; nil ids keeps all.
func FilterSubsample(recs []Record, ids map[string]bool) []Record {
	if ids == nil {
		return recs
	}
	var out []Record
	for _, r := range recs {
		if ids[r.ID] {
			out = append(out, r)
		}
	}
	return out
}
