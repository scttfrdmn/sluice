// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

// Package harvest builds the request corpus (PROJECT.md §5): it reads each
// raw source, normalizes to corpus.Record, filters (credentials, length,
// language), near-deduplicates, and assigns the calibration/test split.
package harvest

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"unicode/utf8"

	"github.com/scttfrdmn/sluice/internal/config"
	"github.com/scttfrdmn/sluice/internal/corpus"
	"github.com/scttfrdmn/sluice/internal/jsonl"
)

// Options selects sources. An empty path disables that source.
type Options struct {
	ClaudeCode   string
	ClaudeExport string
	ChatGPT      string
	Repos        ReposOptions
	Wildchat     string // path to the helper's sample JSONL
	WildchatN    int
	ReviewDir    string // where drop samples are written (gitignored)
}

// Key identifies one row of the stats table.
type Key struct {
	Source string `json:"source"`
	Host   string `json:"host"`
}

// Row is one source × host row: candidates seen, kept, and counts by
// reason for turns skipped as non-requests and records dropped by filters.
type Row struct {
	Key
	Candidates int            `json:"candidates"`
	Kept       int            `json:"kept"`
	Skipped    map[string]int `json:"skipped,omitempty"`
	Dropped    map[string]int `json:"dropped,omitempty"`
}

// Stats is the aggregate harvest table. It contains counts only and is
// safe to publish in docs/CORPUS.md.
type Stats struct {
	rows        map[Key]*Row
	PatternHits map[string]int `json:"pattern_drops"`
	LangDetail  map[string]int `json:"lang_detail"`
	Rows        []*Row         `json:"rows"`
	Splits      map[string]int `json:"splits"`
}

func newStats() *Stats {
	return &Stats{rows: map[Key]*Row{}, PatternHits: map[string]int{}, LangDetail: map[string]int{}, Splits: map[string]int{}}
}

func (s *Stats) row(source, host string) *Row {
	k := Key{source, host}
	r := s.rows[k]
	if r == nil {
		r = &Row{Key: k, Skipped: map[string]int{}, Dropped: map[string]int{}}
		s.rows[k] = r
	}
	return r
}

func (s *Stats) skip(source, host, reason string, n int) { s.row(source, host).Skipped[reason] += n }

func (s *Stats) finish() {
	s.Rows = nil
	for _, r := range s.rows {
		s.Rows = append(s.Rows, r)
	}
	sort.Slice(s.Rows, func(i, j int) bool {
		if s.Rows[i].Source != s.Rows[j].Source {
			return s.Rows[i].Source < s.Rows[j].Source
		}
		return s.Rows[i].Host < s.Rows[j].Host
	})
}

// Drop is one dropped record written to the review sample. The text stays
// local (corpus/review is gitignored).
type Drop struct {
	Reason    string `json:"reason"`
	Source    string `json:"source"`
	OriginRef string `json:"origin_ref"`
	Offset    int    `json:"offset"`
	Text      string `json:"text"`
}

// Run harvests every enabled source and returns kept records and stats.
func Run(ctx context.Context, cfg *config.Config, o Options) ([]corpus.Record, *Stats, error) {
	st := newStats()
	ctxChars := int(float64(cfg.Taxonomy.ContextTokens) * cfg.Taxonomy.CharsPerToken)
	var cands []corpus.Record
	add := func(recs []corpus.Record, err error) error {
		if err != nil {
			return err
		}
		cands = append(cands, recs...)
		return nil
	}
	if o.ClaudeCode != "" {
		if err := add(harvestClaudeCode(o.ClaudeCode, ctxChars, st)); err != nil {
			return nil, nil, fmt.Errorf("claude-code: %w", err)
		}
	}
	if o.ClaudeExport != "" {
		if err := add(harvestClaudeExport(o.ClaudeExport, ctxChars, st)); err != nil {
			return nil, nil, fmt.Errorf("claude-export: %w", err)
		}
	}
	if o.ChatGPT != "" {
		if err := add(harvestChatGPT(o.ChatGPT, ctxChars, st)); err != nil {
			return nil, nil, fmt.Errorf("chatgpt-export: %w", err)
		}
	}
	if o.Repos.Root != "" {
		if err := add(harvestRepos(ctx, o.Repos, st)); err != nil {
			return nil, nil, fmt.Errorf("repos: %w", err)
		}
	}
	if o.Wildchat != "" {
		if err := add(harvestWildchat(o.Wildchat, st)); err != nil {
			return nil, nil, fmt.Errorf("wildchat: %w", err)
		}
	}

	for i := range cands {
		r := &cands[i]
		r.ID = recordID(r.Source, r.OriginRef)
		st.row(r.Source, r.Host).Candidates++
	}

	// Filters, first failure wins: credentials, length, language.
	var credDrops, langDrops []Drop
	var pass []corpus.Record
	for _, r := range cands {
		row := st.row(r.Source, r.Host)
		if name, off := secretMatch(r.Text + "\n" + r.ContextText); name != "" {
			row.Dropped["credential"]++
			st.PatternHits[name]++
			credDrops = append(credDrops, Drop{Reason: name, Source: r.Source, OriginRef: r.OriginRef, Offset: off, Text: clip(r.Text, 2000)})
			continue
		}
		if utf8.RuneCountInString(strings.TrimSpace(r.Text)) < cfg.Harvest.MinChars {
			row.Dropped["short"]++
			continue
		}
		lr := detectEnglish(r.Text)
		st.LangDetail[lr.Lang]++
		if !lr.English {
			row.Dropped["non-english"]++
			langDrops = append(langDrops, Drop{Reason: "lang-" + lr.Lang, Source: r.Source, OriginRef: r.OriginRef, Text: clip(r.Text, 600)})
			continue
		}
		r.Lang = "en"
		pass = append(pass, r)
	}

	// Near-dedup in a fixed priority order: owner sources before the public
	// control, then by timestamp, then id; the earliest copy is kept.
	sort.SliceStable(pass, func(i, j int) bool {
		oi, oj := corpus.Owner(pass[i].Source), corpus.Owner(pass[j].Source)
		if oi != oj {
			return oi
		}
		if pass[i].Timestamp != pass[j].Timestamp {
			return pass[i].Timestamp < pass[j].Timestamp
		}
		return pass[i].ID < pass[j].ID
	})
	texts := make([]string, len(pass))
	for i, r := range pass {
		texts[i] = r.Text
	}
	dupOf := dedup(texts, cfg.Harvest.DedupJaccard, cfg.Seeds.Minhash)
	var kept []corpus.Record
	for i, r := range pass {
		if dupOf[i] >= 0 {
			st.row(r.Source, r.Host).Dropped["near-duplicate"]++
			continue
		}
		kept = append(kept, r)
	}

	// Public control: keep the first N by the helper's random rank.
	if o.WildchatN > 0 {
		sort.SliceStable(kept, func(i, j int) bool {
			if kept[i].Source == corpus.SourceWildchat && kept[j].Source == corpus.SourceWildchat {
				return kept[i].Rank < kept[j].Rank
			}
			return false
		})
		n := 0
		var trimmed []corpus.Record
		for _, r := range kept {
			if r.Source == corpus.SourceWildchat {
				n++
				if n > o.WildchatN {
					st.row(r.Source, r.Host).Skipped["beyond-sample-size"]++
					continue
				}
			}
			trimmed = append(trimmed, r)
		}
		kept = trimmed
	}

	for i := range kept {
		kept[i].Split = split(cfg.Seeds.Split, kept[i].ID)
		st.row(kept[i].Source, kept[i].Host).Kept++
		st.Splits[kept[i].Split]++
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].ID < kept[j].ID })
	st.finish()

	if o.ReviewDir != "" {
		n := cfg.Harvest.DropSampleSize
		if err := jsonl.Write(filepath.Join(o.ReviewDir, "drops.jsonl"), sample(credDrops, n, cfg.Seeds.Split)); err != nil {
			return nil, nil, err
		}
		if err := jsonl.Write(filepath.Join(o.ReviewDir, "drops_lang.jsonl"), sample(langDrops, n, cfg.Seeds.Split)); err != nil {
			return nil, nil, err
		}
	}
	return kept, st, nil
}

func recordID(source, origin string) string {
	h := sha256.Sum256([]byte(source + "\x00" + origin))
	return hex.EncodeToString(h[:8])
}

// split assigns calibration or test 50/50 by a seeded hash of the id.
func split(seed uint64, id string) string {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], seed)
	h := sha256.Sum256(append(b[:], id...))
	if h[0]&1 == 0 {
		return "calibration"
	}
	return "test"
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && (s[n]&0xC0) == 0x80 {
		n--
	}
	return s[:n] + "…"
}

func sample(ds []Drop, n int, seed uint64) []Drop {
	if len(ds) <= n {
		return ds
	}
	r := rand.New(rand.NewPCG(seed, 1))
	out := make([]Drop, len(ds))
	copy(out, ds)
	r.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out[:n]
}

// PrintTable writes the source × host × kept/dropped table and per-pattern
// drop counts.
func (s *Stats) PrintTable(w io.Writer) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, "source\thost\tcandidates\tkept\tcredential\tshort\tnon-english\tnear-dup\tskipped(non-request)\t")
	var tot Row
	for _, r := range s.Rows {
		sk := 0
		for _, v := range r.Skipped {
			sk += v
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t\n", r.Source, dash(r.Host), r.Candidates, r.Kept,
			r.Dropped["credential"], r.Dropped["short"], r.Dropped["non-english"], r.Dropped["near-duplicate"], sk)
		tot.Candidates += r.Candidates
		tot.Kept += r.Kept
	}
	fmt.Fprintf(tw, "total\t\t%d\t%d\t\t\t\t\t\t\n", tot.Candidates, tot.Kept)
	tw.Flush()
	fmt.Fprintln(w)
	fmt.Fprintln(w, "credential drops by pattern:")
	for _, k := range sortedKeys(s.PatternHits) {
		fmt.Fprintf(w, "  %-28s %d\n", k, s.PatternHits[k])
	}
	fmt.Fprintln(w, "skipped (non-request turns) by reason:")
	agg := map[string]int{}
	for _, r := range s.Rows {
		for k, v := range r.Skipped {
			agg[r.Source+" "+k] += v
		}
	}
	for _, k := range sortedKeys(agg) {
		fmt.Fprintf(w, "  %-40s %d\n", k, agg[k])
	}
	fmt.Fprintf(w, "split: calibration %d, test %d\n", s.Splits["calibration"], s.Splits["test"])
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func sortedKeys(m map[string]int) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// WriteStats writes stats as JSON-lines-free JSON to path.
func WriteStats(path string, s *Stats) error {
	return writeJSON(path, s)
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return jsonl.Write(path, []any{v})
}
