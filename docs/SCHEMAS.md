# Raw input schemas

Observed with `cmd/probe` (`make probes`) on the real inputs, 2026-10-26.
Field names and kinds only. The extractors in `internal/harvest` rely on
exactly what is listed here.

## Claude Code session logs

`corpus/raw/claude-code/<host>/projects/<encoded-cwd>/<session>.jsonl`, one
JSON object per line. Probed on all three hosts: terror (141 session files,
2,505 under `subagents/`), juno.local (19 / 626), castor.local (4 / 18).
Client versions 2.1.220–2.1.276. The schema is the same on every host.

`<session>/subagents/*.jsonl` files are agent-written transcripts. The
extractor skips the whole `subagents/` directory.

Top-level `type` values: `user`, `assistant`, `attachment`, `system`, `mode`,
`permission-mode`, `last-prompt`, `ai-title`, `atis-latch`, `api-request`,
`api-request-blob`, `api-request-shape`, `queue-operation`,
`file-history-snapshot`, `file-history-delta`, `pr-link`, `cost-state`. Only
`user` and `assistant` lines are read.

Fields used on `user` / `assistant` lines:

| field | kind | use |
|---|---|---|
| `type` | string | `user` / `assistant` |
| `uuid` | string | origin_ref |
| `sessionId` | string | origin_ref, session_id |
| `timestamp` | string (RFC 3339) | timestamp |
| `cwd` | string | cwd |
| `isSidechain` | bool | true = subagent turn: skipped |
| `isMeta` | bool | true = harness-injected (local-command caveats, image notes, scheduled prompts): skipped |
| `isCompactSummary` | bool | true = context-compaction summary: skipped |
| `entrypoint` | string | `cli` interactive; `sdk-cli` programmatic (`claude -p`): skipped |
| `promptSource` | string, newer clients only | `typed` and `queued` (typed while the agent was busy) kept; `system`, `sdk` skipped |
| `origin.kind` | string, newer clients only | `human` kept; `task-notification`, `peer` skipped |
| `message.role` | string | |
| `message.content` | string, or array of blocks | user: string, or blocks of `text` / `tool_result` / `image`; assistant: blocks of `text` / `thinking` / `tool_use` |

A `user` line whose content has a `tool_result` block is a tool response
inside an assistant turn, not a human turn. Human text can also contain
harness wrappers, and any turn containing one is skipped: `<command-name>`,
`<command-message>`, `<command-args>`, `<system-reminder>`,
`<local-command-stdout|stderr|caveat>`, `<bash-input|stdout|stderr>`,
`<task-notification>`, `<user-prompt-submit-hook>`. Two marker texts are
also skipped: `[Request interrupted…`, and `This session is being continued
from a previous conversation…` (a compaction summary on client versions
that predate `isCompactSummary`).

`context_text` for a kept turn is the previous kept human turn plus the
`text` blocks of every non-sidechain assistant line since then, oldest
first, truncated to the last 2,000 tokens (8,000 characters at 4
characters/token). `context_chars` is the untruncated length of the
assistant part.

## claude.ai export

`corpus/raw/claude-export/conversations.json`. **Not yet probed: no
export is present yet.** The extractor assumes the PROJECT.md §5 structure
(array of conversations with `uuid` and `chat_messages[]` carrying `uuid`,
`sender`, `text`, `created_at`, `content[]`, `attachments[]`, `files[]`).
It will be checked against `probe claude-export` before use.

## ChatGPT export

Probed 2026-10-27 (`probe chatgpt-export corpus/raw/chatgpt-export`) on 147
conversations. The export did not come as the official zip with one
`conversations.json`. It is a per-conversation exporter's zip with `json/`,
`markdown/`, `html/` and `files/` (attachments) folders, one
`<title>_<id>.json` per conversation. Only `json/` is extracted, to
`corpus/raw/chatgpt-export/json/`. Each file is one conversation object in
OpenAI's native shape. The harvester and the probe accept either form: a
`conversations.json` array, or a directory walked for `*.json`, where each
file holds an object or an array.

| field | type | observed | used as |
|---|---|---|---|
| `conversation_id` | string | all | origin_ref, session_id |
| `current_node` | string | all | start of the `parent` walk |
| `mapping` | object | all | nodes `{id, parent, children[], message}`; the root node has `message: null` |
| `message.id` | string | all | origin_ref |
| `message.author.role` | string | `assistant` 5,398, `tool` 4,164, `user` 1,728 | keep `user` |
| `message.content.content_type` | string | user: `text` 1,648, `multimodal_text` 80 | keep `text`, `multimodal_text` |
| `message.content.parts[]` | string or object | user: 1,728 strings, 97 objects (`image_asset_pointer`) | strings joined; objects set has_attachment |
| `message.create_time` | number or null | 11,008 / 282 null | timestamp |
| `message.metadata.is_visually_hidden_from_conversation` | bool | absent on all user messages | skip if true |

Assistant content types seen: `text` (used for context), `code`, `thoughts`,
`reasoning_recap` (not used). 20 of 1,728 user messages are on edited-away
branches, off the `current_node` path, and are not harvested. Two
conversations use a custom GPT (`gizmo_id` set) and are kept.

## WildChat (public control)

`corpus/raw/wildchat/sample.jsonl`, written by `tools/wildchat.py` from
`allenai/WildChat-1M` (837,989 rows; not gated). It keeps rows with
`language == "English"` and takes the first `user` turn of `conversation`.
The sample is a uniform seeded permutation, and each row has `id`
(`conversation_hash`), `text`, `timestamp`, `rank`, `dataset`.
