# Corpus

Harvest run `r2`, 2026-10-27.

Aggregate counts only. Raw inputs and harvested records stay on the local machine (`corpus/`, `runs/`, both gitignored).

## Source × host

| source | host | candidates | kept | human | agent | unknown | with context | with attachment | issue bodies | credential | short | non-English | near-dup |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| chatgpt | - | 1668 | 1376 | 1376 | 0 | 0 | 1231 | 75 | 0 | 2 | 264 | 1 | 25 |
| claude-code | castor.local | 190 | 167 | 167 | 0 | 0 | 163 | 0 | 0 | 0 | 19 | 0 | 4 |
| claude-code | juno.local | 990 | 496 | 496 | 0 | 0 | 477 | 0 | 0 | 84 | 313 | 1 | 96 |
| claude-code | terror | 7672 | 5647 | 5647 | 0 | 0 | 5544 | 21 | 0 | 223 | 1452 | 4 | 346 |
| claude-export | - | 15149 | 13735 | 13735 | 0 | 0 | 12549 | 338 | 0 | 162 | 967 | 19 | 266 |
| repos | - | 6649 | 6473 | 0 | 3795 | 2678 | 0 | 0 | 6461 | 131 | 2 | 0 | 43 |
| wildchat | - | 4800 | 3000 | 3000 | 0 | 0 | 0 | 0 | 0 | 3 | 194 | 28 | 940 |
| **total** | | 37118 | 30894 | 24421 | 3795 | 2678 | | | | | | | |

Split: calibration 15292, test 15602.

## Author kind by rule (kept records)

`author_kind` identifies agent-written requests; they are kept at full weight (PROJECT.md §5). Issue rule: `issue-footer` = generated-with footer, `issue-structure` = ≥2 `## ` headings and ≥400 chars (both → agent); `issue-no-rule` → unknown. Repo prompt files → agent; Claude Code typed/queued turns, chat-export turns and WildChat → human.

| source | rule | records |
|---|---|---:|
| chatgpt | chat-export-turn | 1376 |
| claude-code | claude-code-turn | 6310 |
| claude-export | chat-export-turn | 13735 |
| repos | issue-footer | 4 |
| repos | issue-no-rule | 2678 |
| repos | issue-structure | 3779 |
| repos | repo-prompt-file | 12 |
| wildchat | wildchat-user-turn | 3000 |

## Credential drops by pattern

Hard drop of the whole record. "context-only" means the match was in `context_text` (the preceding turns), which is also sent to models.

| pattern | records |
|---|---:|
| aws-access-key-id (context-only) | 7 |
| aws-access-key-id (request) | 3 |
| aws-account-id-in-context (context-only) | 165 |
| aws-account-id-in-context (request) | 35 |
| aws-secret-key-shape (context-only) | 13 |
| aws-secret-key-shape (request) | 15 |
| google-api-key (request) | 1 |
| private-key (request) | 1 |
| s3-bucket-uri (context-only) | 224 |
| s3-bucket-uri (request) | 141 |

## Turns skipped as not human-typed requests

| source | reason | turns |
|---|---|---:|
| chatgpt | attachment-only | 2 |
| chatgpt | empty | 38 |
| claude-code | compact-summary | 3469 |
| claude-code | compact-summary-unflagged | 6 |
| claude-code | entrypoint-sdk-cli | 164 |
| claude-code | interrupt-marker | 90 |
| claude-code | meta | 1647 |
| claude-code | origin-task-notification | 107 |
| claude-code | prompt-source-system | 6216 |
| claude-code | subagent-file-dir | 68 |
| claude-code | tag-bash-input | 32 |
| claude-code | tag-bash-stdout | 32 |
| claude-code | tag-command-name | 86 |
| claude-code | tag-local-command-stdout | 59 |
| claude-export | empty | 1828 |
| repos | excluded-repo | 1 |
| repos | gh-issue-list-failed | 5 |
| repos | issue-empty | 9 |
| repos | issue-not-owner | 1826 |
| repos | prompt-file-excluded:remote-owner | 4 |
| repos | prompt-file-excluded:vendored-dir | 2 |
| wildchat | beyond-sample-size | 635 |

## Language detection outcomes (records reaching the language filter)

| outcome | records |
|---|---:|
| ara | 2 |
| ces | 4 |
| dan | 1 |
| deu | 5 |
| eng | 32983 |
| eng-low-ces | 2 |
| eng-low-dan | 10 |
| eng-low-deu | 47 |
| eng-low-fas | 1 |
| eng-low-fra | 15 |
| eng-low-hun | 2 |
| eng-low-ind | 8 |
| eng-low-ita | 12 |
| eng-low-nld | 20 |
| eng-low-nob | 9 |
| eng-low-por | 5 |
| eng-low-ron | 5 |
| eng-low-spa | 5 |
| eng-low-swe | 12 |
| eng-low-tur | 5 |
| fra | 7 |
| hin | 1 |
| ind | 1 |
| ita | 2 |
| jpn | 3 |
| nld | 2 |
| no-prose | 108 |
| nob | 3 |
| pol | 2 |
| por | 2 |
| ron | 1 |
| rus | 5 |
| spa | 3 |
| swe | 3 |
| zho | 6 |

## E0 subsample

Seeded (seed 1000), stratified draw from the calibration split, up to 1000 requests per stratum (PROJECT.md §5).

| stratum | selected | available |
|---|---:|---:|
| chatgpt | 677 | 677 |
| claude-code | 1000 | 3163 |
| claude-export | 1000 | 6853 |
| repos:issue | 1000 | 3162 |
| repos:prompt-file | 4 | 4 |
| wildchat | 1000 | 1433 |
| **total** | 4681 | |

## E0 test subsample (drawn, not tagged)

Seeded (seed 1000), stratified draw from the test split, up to 1000 requests per stratum (PROJECT.md §5).

| stratum | selected | available |
|---|---:|---:|
| chatgpt | 699 | 699 |
| claude-code | 1000 | 3147 |
| claude-export | 1000 | 6882 |
| repos:issue | 1000 | 3299 |
| repos:prompt-file | 8 | 8 |
| wildchat | 1000 | 1567 |
| **total** | 4707 | |
