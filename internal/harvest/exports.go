// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

package harvest

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/scttfrdmn/sluice/internal/corpus"
)

// claude.ai account export: conversations.json, an array of conversations
// each carrying chat_messages[]. Field names confirmed by cmd/probe; see
// docs/SCHEMAS.md.

type claudeConv struct {
	UUID         string `json:"uuid"`
	ChatMessages []struct {
		UUID      string `json:"uuid"`
		Sender    string `json:"sender"`
		Text      string `json:"text"`
		CreatedAt string `json:"created_at"`
		Content   []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Attachments []json.RawMessage `json:"attachments"`
		Files       []json.RawMessage `json:"files"`
	} `json:"chat_messages"`
}

func harvestClaudeExport(path string, contextChars int, st *Stats) ([]corpus.Record, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var convs []claudeConv
	if err := json.Unmarshal(b, &convs); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	src := corpus.SourceClaudeExport
	var out []corpus.Record
	for _, c := range convs {
		var prevHuman, prevAsst string
		turn := 0
		for _, m := range c.ChatMessages {
			text := m.Text
			if strings.TrimSpace(text) == "" {
				var parts []string
				for _, blk := range m.Content {
					if blk.Type == "text" {
						parts = append(parts, blk.Text)
					}
				}
				text = strings.Join(parts, "\n\n")
			}
			switch m.Sender {
			case "assistant":
				prevAsst = text
				continue
			case "human":
			default:
				st.skip(src, "", "sender-"+m.Sender, 1)
				continue
			}
			attach := len(m.Attachments) > 0 || len(m.Files) > 0
			if strings.TrimSpace(text) == "" {
				st.skip(src, "", "empty", 1)
				turn++
				continue
			}
			ctx := ""
			if prevHuman != "" || prevAsst != "" {
				ctx = buildContext(prevHuman, prevAsst, contextChars)
			}
			out = append(out, corpus.Record{
				Source:        src,
				AuthorKind:    corpus.AuthorHuman,
				AuthorRule:    corpus.RuleChatExport,
				OriginRef:     fmt.Sprintf("claude-export:%s:%s", c.UUID, m.UUID),
				Timestamp:     m.CreatedAt,
				Text:          text,
				ContextText:   ctx,
				ContextChars:  len(prevAsst),
				HasAttachment: attach,
				TurnIndex:     turn,
				SessionID:     c.UUID,
			})
			turn++
			prevHuman, prevAsst = text, ""
		}
	}
	return out, nil
}

// ChatGPT export: conversations.json, an array of conversations with a
// mapping of nodes forming a tree; the shown thread is recovered by walking
// parent links from current_node. Field names confirmed by cmd/probe.

type gptConv struct {
	ID          string             `json:"id"`
	ConvID      string             `json:"conversation_id"`
	CurrentNode string             `json:"current_node"`
	Mapping     map[string]gptNode `json:"mapping"`
}

type gptNode struct {
	ID      string `json:"id"`
	Parent  string `json:"parent"`
	Message *struct {
		ID     string `json:"id"`
		Author struct {
			Role string `json:"role"`
		} `json:"author"`
		CreateTime *float64 `json:"create_time"`
		Content    struct {
			ContentType string            `json:"content_type"`
			Parts       []json.RawMessage `json:"parts"`
		} `json:"content"`
		Metadata map[string]any `json:"metadata"`
	} `json:"message"`
}

func harvestChatGPT(path string, contextChars int, st *Stats) ([]corpus.Record, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var convs []gptConv
	if err := json.Unmarshal(b, &convs); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	src := corpus.SourceChatGPT
	var out []corpus.Record
	for _, c := range convs {
		id := c.ConvID
		if id == "" {
			id = c.ID
		}
		// Walk current_node -> root, then reverse into turn order.
		var thread []gptNode
		seen := map[string]bool{}
		for n := c.CurrentNode; n != "" && !seen[n]; {
			seen[n] = true
			node, ok := c.Mapping[n]
			if !ok {
				break
			}
			thread = append(thread, node)
			n = node.Parent
		}
		for i, j := 0, len(thread)-1; i < j; i, j = i+1, j-1 {
			thread[i], thread[j] = thread[j], thread[i]
		}
		var prevHuman, prevAsst string
		turn := 0
		for _, node := range thread {
			m := node.Message
			if m == nil {
				continue
			}
			text, nonString := gptParts(m.Content.Parts)
			switch m.Author.Role {
			case "assistant":
				if m.Content.ContentType == "text" && strings.TrimSpace(text) != "" {
					prevAsst = text
				}
				continue
			case "user":
			default:
				continue // system, tool
			}
			if hidden, _ := m.Metadata["is_visually_hidden_from_conversation"].(bool); hidden {
				st.skip(src, "", "hidden-user-message", 1)
				continue
			}
			if ct := m.Content.ContentType; ct != "text" && ct != "multimodal_text" {
				st.skip(src, "", "content-type-"+ct, 1)
				continue
			}
			if strings.TrimSpace(text) == "" {
				if nonString {
					st.skip(src, "", "attachment-only", 1)
				} else {
					st.skip(src, "", "empty", 1)
				}
				turn++
				continue
			}
			ts := ""
			if m.CreateTime != nil {
				sec := int64(*m.CreateTime)
				ts = time.Unix(sec, int64((*m.CreateTime-float64(sec))*1e9)).UTC().Format(time.RFC3339)
			}
			ctx := ""
			if prevHuman != "" || prevAsst != "" {
				ctx = buildContext(prevHuman, prevAsst, contextChars)
			}
			out = append(out, corpus.Record{
				Source:        src,
				AuthorKind:    corpus.AuthorHuman,
				AuthorRule:    corpus.RuleChatExport,
				OriginRef:     fmt.Sprintf("chatgpt:%s:%s", id, m.ID),
				Timestamp:     ts,
				Text:          text,
				ContextText:   ctx,
				ContextChars:  len(prevAsst),
				HasAttachment: nonString,
				TurnIndex:     turn,
				SessionID:     id,
			})
			turn++
			prevHuman, prevAsst = text, ""
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].OriginRef < out[j].OriginRef })
	return out, nil
}

// gptParts joins string parts and reports whether any part was not a
// string (image or file references).
func gptParts(parts []json.RawMessage) (string, bool) {
	var ss []string
	nonString := false
	for _, p := range parts {
		var s string
		if json.Unmarshal(p, &s) == nil {
			if s != "" {
				ss = append(ss, s)
			}
			continue
		}
		nonString = true
	}
	return strings.Join(ss, "\n\n"), nonString
}
