// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

package harvest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadConversationsDir(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("json/b_2.json", `{"conversation_id": "b"}`)
	write("json/a_1.json", ` [{"conversation_id": "a"}, {"conversation_id": "a2"}]`)
	write("markdown/a_1.md", `# not json`)
	cs, err := ReadConversations[gptConv](dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 3 || cs[0].ConvID != "a" || cs[2].ConvID != "b" {
		t.Fatalf("got %+v", cs)
	}
	one, err := ReadConversations[gptConv](filepath.Join(dir, "json/b_2.json"))
	if err != nil || len(one) != 1 {
		t.Fatalf("single file: %v %v", one, err)
	}
}
