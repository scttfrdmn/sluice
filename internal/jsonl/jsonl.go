// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

// Package jsonl reads and appends newline-delimited JSON files, the format
// of every run artifact except the matrix itself.
package jsonl

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// Read decodes every line of path into a T. A missing file yields no rows.
func Read[T any](path string) ([]T, error) {
	var out []T
	err := Each(path, func(v T) error {
		out = append(out, v)
		return nil
	})
	return out, err
}

// Each calls fn for every line of path. A missing file is not an error. A
// truncated final line (from an interrupted append) is skipped.
func Each[T any](path string, fn func(T) error) error {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 256<<20)
	line := 0
	var pending error
	for sc.Scan() {
		line++
		if pending != nil {
			return pending // a bad line that was not the last one
		}
		if len(sc.Bytes()) == 0 {
			continue
		}
		var v T
		if err := json.Unmarshal(sc.Bytes(), &v); err != nil {
			pending = fmt.Errorf("%s:%d: %w", path, line, err)
			continue
		}
		if err := fn(v); err != nil {
			return err
		}
	}
	return sc.Err()
}

// Write replaces path with rows, atomically.
func Write[T any](path string, rows []T) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			f.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Appender appends rows to a file, one fsync-free line per row, safe for
// concurrent use. Resumable commands use it so completed work survives an
// interruption.
type Appender struct {
	mu  sync.Mutex
	f   *os.File
	enc *json.Encoder
}

// OpenAppender opens path for appending, creating it if needed.
func OpenAppender(path string) (*Appender, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	return &Appender{f: f, enc: enc}, nil
}

// Append writes one row.
func (a *Appender) Append(v any) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.enc.Encode(v)
}

// Close closes the file.
func (a *Appender) Close() error { return a.f.Close() }
