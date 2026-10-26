// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

package harvest

import (
	"hash/fnv"
	"math/rand/v2"
	"strings"
)

// MinHash near-duplicate detection (PROJECT.md §5: Jaccard > 0.85).
// Shingles are character 5-grams of the lowercased, whitespace-collapsed
// text. LSH with 16 bands of 8 rows proposes candidates (detection
// probability ≈ 0.98 at J = 0.85); each candidate pair is then confirmed on
// exact shingle-set Jaccard, so the threshold is applied exactly.

const (
	mhBands = 16
	mhRows  = 8
	mhPerms = mhBands * mhRows
	shingle = 5
)

type minhasher struct{ a, b [mhPerms]uint64 }

func newMinhasher(seed uint64) *minhasher {
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	m := &minhasher{}
	for i := range mhPerms {
		m.a[i] = r.Uint64() | 1
		m.b[i] = r.Uint64()
	}
	return m
}

func shingles(text string) map[uint64]struct{} {
	norm := []rune(strings.Join(strings.Fields(strings.ToLower(text)), " "))
	set := map[uint64]struct{}{}
	if len(norm) < shingle {
		h := fnv.New64a()
		h.Write([]byte(string(norm)))
		set[h.Sum64()] = struct{}{}
		return set
	}
	for i := 0; i+shingle <= len(norm); i++ {
		h := fnv.New64a()
		h.Write([]byte(string(norm[i : i+shingle])))
		set[h.Sum64()] = struct{}{}
	}
	return set
}

func (m *minhasher) signature(set map[uint64]struct{}) [mhPerms]uint64 {
	var sig [mhPerms]uint64
	for i := range sig {
		sig[i] = ^uint64(0)
	}
	for x := range set {
		for i := range mhPerms {
			if v := m.a[i]*x + m.b[i]; v < sig[i] {
				sig[i] = v
			}
		}
	}
	return sig
}

func jaccard(a, b map[uint64]struct{}) float64 {
	if len(a) > len(b) {
		a, b = b, a
	}
	inter := 0
	for x := range a {
		if _, ok := b[x]; ok {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 1
	}
	return float64(inter) / float64(union)
}

// dedup returns, for texts in priority order, the index of the earlier
// text each one duplicates, or -1 if it is kept.
func dedup(texts []string, threshold float64, seed uint64) []int {
	m := newMinhasher(seed)
	sets := make([]map[uint64]struct{}, len(texts))
	dupOf := make([]int, len(texts))
	buckets := make([]map[uint64][]int, mhBands)
	for i := range buckets {
		buckets[i] = map[uint64][]int{}
	}
	for i, t := range texts {
		dupOf[i] = -1
		sets[i] = shingles(t)
		sig := m.signature(sets[i])
		keys := make([]uint64, mhBands)
		for band := range mhBands {
			h := fnv.New64a()
			for r := range mhRows {
				v := sig[band*mhRows+r]
				for k := range 8 {
					h.Write([]byte{byte(v >> (8 * k))})
				}
			}
			keys[band] = h.Sum64()
		}
		checked := map[int]bool{}
	search:
		for band, k := range keys {
			for _, j := range buckets[band][k] {
				if checked[j] {
					continue
				}
				checked[j] = true
				if jaccard(sets[i], sets[j]) > threshold {
					dupOf[i] = j
					break search
				}
			}
		}
		if dupOf[i] >= 0 {
			sets[i] = nil // never a canonical; free it
			continue
		}
		for band, k := range keys {
			buckets[band][k] = append(buckets[band][k], i)
		}
	}
	return dupOf
}
