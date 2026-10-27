# Corpus

Harvest run `r1b`, 2026-10-27.

Aggregate counts only. Raw inputs and harvested records stay on the local machine (`corpus/`, `runs/`, both gitignored).

## Source × host

| source | host | candidates | kept | human | agent | unknown | with context | with attachment | issue bodies | credential | short | non-English | near-dup |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| claude-code | castor.local | 190 | 167 | 167 | 0 | 0 | 163 | 0 | 0 | 0 | 19 | 0 | 4 |
| claude-code | juno.local | 990 | 497 | 497 | 0 | 0 | 478 | 0 | 0 | 84 | 313 | 1 | 95 |
| claude-code | terror | 7672 | 5650 | 5650 | 0 | 0 | 5547 | 21 | 0 | 223 | 1452 | 4 | 343 |
| repos | - | 6632 | 6455 | 0 | 3802 | 2653 | 0 | 0 | 6438 | 131 | 2 | 0 | 44 |
| wildchat | - | 4800 | 3000 | 3000 | 0 | 0 | 0 | 0 | 0 | 3 | 194 | 28 | 940 |
| **total** | | 20284 | 15769 | 9314 | 3802 | 2653 | | | | | | | |

Split: calibration 7757, test 8012.

## Author kind by rule (kept records)

`author_kind` identifies agent-written requests; they are kept at full weight (PROJECT.md §5). Issue rule: `issue-footer` = generated-with footer, `issue-structure` = ≥2 `## ` headings and ≥400 chars (both → agent); `issue-no-rule` → unknown. Repo prompt files → agent; Claude Code typed/queued turns, chat-export turns and WildChat → human.

| source | rule | records |
|---|---|---:|
| claude-code | claude-code-turn | 6314 |
| repos | issue-footer | 4 |
| repos | issue-no-rule | 2653 |
| repos | issue-structure | 3781 |
| repos | repo-prompt-file | 17 |
| wildchat | wildchat-user-turn | 3000 |

## Credential drops by pattern

Hard drop of the whole record. "context-only" means the match was in `context_text` (the preceding turns), which is also sent to models.

| pattern | records |
|---|---:|
| aws-access-key-id (context-only) | 2 |
| aws-access-key-id (request) | 1 |
| aws-account-id-in-context (context-only) | 147 |
| aws-account-id-in-context (request) | 24 |
| aws-secret-key-shape (context-only) | 7 |
| aws-secret-key-shape (request) | 11 |
| google-api-key (request) | 1 |
| private-key (request) | 1 |
| s3-bucket-uri (context-only) | 127 |
| s3-bucket-uri (request) | 120 |

## Turns skipped as not human-typed requests

| source | reason | turns |
|---|---|---:|
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
| repos | excluded-repo | 1 |
| repos | gh-issue-list-failed | 5 |
| repos | issue-empty | 9 |
| repos | issue-not-owner | 1826 |
| wildchat | beyond-sample-size | 635 |

## Language detection outcomes (records reaching the language filter)

| outcome | records |
|---|---:|
| ara | 2 |
| dan | 1 |
| deu | 4 |
| eng | 17696 |
| eng-low-ces | 1 |
| eng-low-dan | 4 |
| eng-low-deu | 24 |
| eng-low-fas | 1 |
| eng-low-fra | 4 |
| eng-low-hun | 1 |
| eng-low-ind | 4 |
| eng-low-ita | 7 |
| eng-low-nld | 7 |
| eng-low-nob | 2 |
| eng-low-por | 2 |
| eng-low-ron | 5 |
| eng-low-spa | 4 |
| eng-low-swe | 5 |
| eng-low-tur | 3 |
| fra | 3 |
| hin | 1 |
| ind | 1 |
| ita | 2 |
| jpn | 3 |
| nld | 2 |
| no-prose | 60 |
| nob | 1 |
| pol | 1 |
| rus | 5 |
| swe | 1 |
| zho | 6 |

## E0 subsample

Seeded (seed 1000), stratified draw from the calibration split, up to 1000 requests per stratum (PROJECT.md §5). E0 runs on these requests only.

| stratum | selected | available |
|---|---:|---:|
| claude-code | 1000 | 3165 |
| repos:issue | 1000 | 3153 |
| repos:prompt-file | 6 | 6 |
| wildchat | 1000 | 1433 |
| **total** | 3006 | |
