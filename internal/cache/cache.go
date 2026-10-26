// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

// Package cache stores model outputs keyed by SHA-256 of their full input,
// so tagger, judge and reference calls are never repeated (CC_PROMPT.md).
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Cache is a directory of JSON files named by key, sharded by prefix.
type Cache struct{ dir string }

// New returns a cache rooted at dir.
func New(dir string) *Cache { return &Cache{dir: dir} }

// Key hashes the parts of an input into a cache key.
func Key(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TextHash is the SHA-256 of a single string.
func TextHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func (c *Cache) path(key string) string {
	return filepath.Join(c.dir, key[:2], key+".json")
}

// Get decodes the entry for key into v and reports whether it existed.
func (c *Cache) Get(key string, v any) (bool, error) {
	b, err := os.ReadFile(c.path(key))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(b, v)
}

// Put stores v under key atomically.
func (c *Cache) Put(key string, v any) error {
	p := c.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p), "put-*")
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), p)
}
