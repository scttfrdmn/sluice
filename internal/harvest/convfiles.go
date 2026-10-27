// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

package harvest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ReadConversations loads chat-export conversations from path: either one
// JSON file holding an array of conversations (the official
// conversations.json), or a directory walked for *.json files, each holding
// one conversation object or an array (per-conversation exporters).
func ReadConversations[T any](path string) ([]T, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return readConvFile[T](path)
	}
	var files []string
	err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.EqualFold(filepath.Ext(p), ".json") {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%s: no .json files", path)
	}
	sort.Strings(files)
	var out []T
	for _, f := range files {
		cs, err := readConvFile[T](f)
		if err != nil {
			return nil, err
		}
		out = append(out, cs...)
	}
	return out, nil
}

func readConvFile[T any](path string) ([]T, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] == '{' {
		var one T
		if err := json.Unmarshal(b, &one); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		return []T{one}, nil
	}
	var arr []T
	if err := json.Unmarshal(b, &arr); err != nil {
		return nil, fmt.Errorf("%s: top level is neither an object nor an array of objects: %w", path, err)
	}
	return arr, nil
}
