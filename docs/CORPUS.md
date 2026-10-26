# Corpus

Harvest run `r1`, 2026-10-26.

Aggregate counts only. Raw inputs and harvested records stay on the local machine (`corpus/`, `runs/`, both gitignored).

## Source × host

| source | host | candidates | kept | with context | with attachment | issue bodies | credential | short | non-English | near-dup |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| claude-code | castor.local | 190 | 167 | 163 | 0 | 0 | 0 | 19 | 0 | 4 |
| claude-code | juno.local | 990 | 497 | 478 | 0 | 0 | 84 | 313 | 1 | 95 |
| claude-code | terror | 7672 | 5650 | 5547 | 21 | 0 | 223 | 1452 | 4 | 343 |
| repos | - | 6591 | 6421 | 0 | 0 | 6421 | 127 | 2 | 0 | 41 |
| wildchat | - | 4800 | 3000 | 0 | 0 | 0 | 3 | 194 | 28 | 940 |
| **total** | | 20243 | 15735 | | | | | | | |

Split: calibration 7740, test 7995.

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
| s3-bucket-uri (request) | 116 |

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
| repos | issue-agent-generated | 4 |
| repos | issue-empty | 9 |
| repos | issue-not-owner | 1826 |
| wildchat | beyond-sample-size | 635 |

## Language detection outcomes (records reaching the language filter)

| outcome | records |
|---|---:|
| ara | 2 |
| dan | 1 |
| deu | 4 |
| eng | 17659 |
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
