// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

// Command probe prints the observed structure of a raw corpus source before
// any extractor relies on it (CC_PROMPT.md, cmd/harvest: "probe first").
//
//	probe claude-code <dir>        distinct keys and type values, first 200 lines of 20 files
//	probe claude-export <file>     conversation and chat_message keys, sender values
//	probe chatgpt-export <file>    conversation, node, message keys, roles, part types
//
// Output is key names, value kinds and counts only; no message text is
// printed, so the output is safe to paste into docs/SCHEMAS.md.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: probe claude-code|claude-export|chatgpt-export <path>")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "claude-code":
		err = probeClaudeCode(os.Args[2])
	case "claude-export":
		err = probeClaudeExport(os.Args[2])
	case "chatgpt-export":
		err = probeChatGPT(os.Args[2])
	default:
		err = fmt.Errorf("unknown source %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "probe:", err)
		os.Exit(1)
	}
}

// counter counts distinct string keys.
type counter map[string]int

func (c counter) print(title string) {
	fmt.Printf("\n## %s\n", title)
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if c[keys[i]] != c[keys[j]] {
			return c[keys[i]] > c[keys[j]]
		}
		return keys[i] < keys[j]
	})
	for _, k := range keys {
		fmt.Printf("%8d  %s\n", c[k], k)
	}
}

func kind(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "bool"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "?"
}

// tags lists the leading XML-ish tags seen at the start of string content,
// e.g. "<command-name>", so the extractor's drop rules can be checked.
func leadingTag(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "<") {
		return "(text)"
	}
	end := strings.IndexAny(s, "> \n")
	if end < 0 {
		return "(text)"
	}
	return s[:end] + ">"
}

func probeClaudeCode(root string) error {
	var files []string
	nested := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".jsonl") {
			if strings.Contains(p, "/subagents/") {
				nested++
			} else {
				files = append(files, p)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(files)
	// Spread the 20 files across the tree rather than taking the first 20
	// lexically, which would all come from one project.
	picked := files
	if len(files) > 20 {
		picked = nil
		step := len(files) / 20
		for i := 0; i < 20; i++ {
			picked = append(picked, files[i*step])
		}
	}
	fmt.Printf("# claude-code probe: %d session jsonl files under %s (plus %d under subagents/, not sampled); sampled %d\n", len(files), root, nested, len(picked))

	top, types, msgKeys, contentKinds, blockTypes, userTags, flags := counter{}, counter{}, counter{}, counter{}, counter{}, counter{}, counter{}
	lines, bad := 0, 0
	for _, f := range picked {
		fh, err := os.Open(f)
		if err != nil {
			return err
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 1<<20), 64<<20)
		for n := 0; n < 200 && sc.Scan(); n++ {
			lines++
			var m map[string]any
			if json.Unmarshal(sc.Bytes(), &m) != nil {
				bad++
				continue
			}
			for k, v := range m {
				top[k+" : "+kind(v)]++
			}
			t, _ := m["type"].(string)
			types[t]++
			for _, fk := range []string{"isSidechain", "isMeta", "userType", "entrypoint", "isCompactSummary", "isVisibleInTranscriptOnly", "promptSource", "turnOrigin", "permissionMode"} {
				if v, ok := m[fk]; ok {
					flags[fmt.Sprintf("%s=%v (type=%s)", fk, v, t)]++
				}
			}
			if o, ok := m["origin"].(map[string]any); ok {
				for k, v := range o {
					flags[fmt.Sprintf("origin.%s=%v (type=%s)", k, v, t)]++
				}
			}
			msg, _ := m["message"].(map[string]any)
			if msg == nil {
				continue
			}
			for k, v := range msg {
				msgKeys[t+".message."+k+" : "+kind(v)]++
			}
			switch c := msg["content"].(type) {
			case string:
				contentKinds[t+" : string"]++
				if t == "user" {
					userTags[leadingTag(c)]++
				}
			case []any:
				contentKinds[t+" : array"]++
				for _, b := range c {
					bm, _ := b.(map[string]any)
					bt, _ := bm["type"].(string)
					blockTypes[t+" : "+bt]++
					if t == "user" && bt == "text" {
						s, _ := bm["text"].(string)
						userTags["[block] "+leadingTag(s)]++
					}
				}
			}
		}
		if err := sc.Err(); err != nil {
			fmt.Printf("scan error in one file: %v\n", err)
		}
		fh.Close()
	}
	fmt.Printf("lines read: %d, unparseable: %d\n", lines, bad)
	top.print("top-level keys : kind")
	types.print("type values")
	flags.print("flag fields")
	msgKeys.print("message keys by type")
	contentKinds.print("message.content kind by type")
	blockTypes.print("content block types by type")
	userTags.print("user content leading tag")
	return nil
}

func readJSONArray(path string) ([]map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var arr []map[string]any
	if err := json.Unmarshal(b, &arr); err != nil {
		return nil, fmt.Errorf("%s: top level is not an array of objects: %w", path, err)
	}
	return arr, nil
}

func probeClaudeExport(path string) error {
	convs, err := readJSONArray(path)
	if err != nil {
		return err
	}
	fmt.Printf("# claude-export probe: %d conversations\n", len(convs))
	convKeys, msgKeys, senders, contentTypes := counter{}, counter{}, counter{}, counter{}
	for _, c := range convs {
		for k, v := range c {
			convKeys[k+" : "+kind(v)]++
		}
		msgs, _ := c["chat_messages"].([]any)
		for _, m := range msgs {
			mm, _ := m.(map[string]any)
			for k, v := range mm {
				msgKeys[k+" : "+kind(v)]++
			}
			s, _ := mm["sender"].(string)
			senders[s]++
			if cs, ok := mm["content"].([]any); ok {
				for _, b := range cs {
					bm, _ := b.(map[string]any)
					bt, _ := bm["type"].(string)
					contentTypes[s+" : "+bt]++
				}
			}
		}
	}
	convKeys.print("conversation keys")
	msgKeys.print("chat_message keys")
	senders.print("sender values")
	contentTypes.print("content block types by sender")
	return nil
}

func probeChatGPT(path string) error {
	convs, err := readJSONArray(path)
	if err != nil {
		return err
	}
	fmt.Printf("# chatgpt-export probe: %d conversations\n", len(convs))
	convKeys, nodeKeys, msgKeys, roles, ctypes, partKinds := counter{}, counter{}, counter{}, counter{}, counter{}, counter{}
	for _, c := range convs {
		for k, v := range c {
			convKeys[k+" : "+kind(v)]++
		}
		mapping, _ := c["mapping"].(map[string]any)
		for _, n := range mapping {
			nm, _ := n.(map[string]any)
			for k, v := range nm {
				nodeKeys[k+" : "+kind(v)]++
			}
			msg, _ := nm["message"].(map[string]any)
			if msg == nil {
				continue
			}
			for k, v := range msg {
				msgKeys[k+" : "+kind(v)]++
			}
			author, _ := msg["author"].(map[string]any)
			role, _ := author["role"].(string)
			roles[role]++
			content, _ := msg["content"].(map[string]any)
			ct, _ := content["content_type"].(string)
			ctypes[role+" : "+ct]++
			parts, _ := content["parts"].([]any)
			for _, p := range parts {
				partKinds[role+" : "+kind(p)]++
			}
		}
	}
	convKeys.print("conversation keys")
	nodeKeys.print("mapping node keys")
	msgKeys.print("message keys")
	roles.print("author.role values")
	ctypes.print("content.content_type by role")
	partKinds.print("content.parts element kind by role")
	return nil
}
