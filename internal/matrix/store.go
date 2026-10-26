// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

// Package matrix builds the outcome matrix (PROJECT.md §6): every leaf ×
// model × draw, with output, tokens, latency and cost.
package matrix

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/parquet-go/parquet-go"
)

// Modes.
const (
	ModeOnDemand = "on-demand"
	ModeBatch    = "batch"
)

// Cell is one row of the matrix. LatencyMs is set only for on-demand calls
// (including service-side queueing); BatchWallMs only for batch cells.
// The two are never mixed (PROJECT.md §6 "Latency").
type Cell struct {
	LeafID      string  `parquet:"leaf_id" json:"leaf_id"`
	RequestID   string  `parquet:"request_id" json:"request_id"`
	Class       string  `parquet:"class" json:"class"`
	Model       string  `parquet:"model" json:"model"`
	ModelID     string  `parquet:"model_id" json:"model_id"`
	Draw        int32   `parquet:"draw" json:"draw"`
	Output      string  `parquet:"output" json:"output"`
	TokensIn    int64   `parquet:"tokens_in" json:"tokens_in"`
	TokensOut   int64   `parquet:"tokens_out" json:"tokens_out"`
	LatencyMs   *int64  `parquet:"latency_ms,optional" json:"latency_ms,omitempty"`
	BatchWallMs *int64  `parquet:"batch_wall_ms,optional" json:"batch_wall_ms,omitempty"`
	Mode        string  `parquet:"mode" json:"mode"`
	Subsample   bool    `parquet:"latency_subsample" json:"latency_subsample"`
	CostUSD     float64 `parquet:"cost_usd" json:"cost_usd"`
	PriceBasis  string  `parquet:"price_basis" json:"price_basis"`
	PriceDate   string  `parquet:"price_date" json:"price_date"`
	StopReason  string  `parquet:"stop_reason" json:"stop_reason"`
	CreatedAt   string  `parquet:"created_at" json:"created_at"`
}

// Key identifies a cell.
func (c Cell) Key() string { return CellKey(c.LeafID, c.Model, int(c.Draw)) }

// CellKey identifies a (leaf, model, draw).
func CellKey(leaf, model string, draw int) string { return fmt.Sprintf("%s|%s|%d", leaf, model, draw) }

// ReadAll reads every part file in dir. Later parts win on duplicate keys.
func ReadAll(dir string) ([]Cell, error) {
	parts, err := filepath.Glob(filepath.Join(dir, "part-*.parquet"))
	if err != nil {
		return nil, err
	}
	sort.Strings(parts)
	byKey := map[string]int{}
	var out []Cell
	for _, p := range parts {
		rows, err := parquet.ReadFile[Cell](p)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		for _, r := range rows {
			if i, ok := byKey[r.Key()]; ok {
				out[i] = r
				continue
			}
			byKey[r.Key()] = len(out)
			out = append(out, r)
		}
	}
	return out, nil
}

// Writer buffers cells and flushes them as new part files, atomically, every
// flushRows rows or flushEvery, so an interruption loses at most one buffer.
type Writer struct {
	dir       string
	flushRows int
	mu        sync.Mutex
	buf       []Cell
	seq       int
	stop      chan struct{}
	done      chan struct{}
	err       error
	written   int
}

// NewWriter starts a writer.
func NewWriter(dir string, flushRows int, flushEvery time.Duration) (*Writer, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	w := &Writer{dir: dir, flushRows: max(1, flushRows), stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(w.done)
		t := time.NewTicker(flushEvery)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				w.mu.Lock()
				w.flushLocked()
				w.mu.Unlock()
			case <-w.stop:
				return
			}
		}
	}()
	return w, nil
}

// Add buffers one cell.
func (w *Writer) Add(c Cell) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, c)
	if len(w.buf) >= w.flushRows {
		w.flushLocked()
	}
	return w.err
}

func (w *Writer) flushLocked() {
	if len(w.buf) == 0 || w.err != nil {
		return
	}
	w.seq++
	name := fmt.Sprintf("part-%d-%04d.parquet", time.Now().UnixNano(), w.seq)
	tmp := filepath.Join(w.dir, "."+name+".tmp")
	if err := parquet.WriteFile(tmp, w.buf); err != nil {
		w.err = err
		return
	}
	if err := os.Rename(tmp, filepath.Join(w.dir, name)); err != nil {
		w.err = err
		return
	}
	w.written += len(w.buf)
	w.buf = w.buf[:0]
}

// Close flushes and stops the writer.
func (w *Writer) Close() (int, error) {
	close(w.stop)
	<-w.done
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushLocked()
	return w.written, w.err
}

// Compact rewrites all parts as one file (optional housekeeping).
func Compact(dir string) error {
	cells, err := ReadAll(dir)
	if err != nil || len(cells) == 0 {
		return err
	}
	parts, _ := filepath.Glob(filepath.Join(dir, "part-*.parquet"))
	name := fmt.Sprintf("part-%d-0000.parquet", time.Now().UnixNano())
	tmp := filepath.Join(dir, "."+name+".tmp")
	if err := parquet.WriteFile(tmp, cells); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
		return err
	}
	for _, p := range parts {
		if !strings.HasSuffix(p, name) {
			os.Remove(p)
		}
	}
	return nil
}
