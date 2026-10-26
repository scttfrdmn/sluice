# sluice

Class-based request routing on a task graph, with per-class certified quality.

This repository is an experiment program first. It currently holds **E0**:
the measurement that decides whether the routing library is worth
building. E1–E3 exist as interfaces only. See [PROJECT.md](PROJECT.md) for
the claim, design, and decision rules, and [CC_PROMPT.md](CC_PROMPT.md) for
the build scope.

## E0 pipeline

One binary per stage. Each stage reads and writes files under `runs/<run-id>/`.
Every stage is resumable, and model outputs are cached by SHA-256 of their input.

| stage | reads | writes |
|---|---|---|
| `harvest` | `corpus/raw/**` | `records.jsonl`, `harvest_stats.json` |
| `tag` | `records.jsonl` | `tags.jsonl`, `leaves.jsonl` |
| `matrix` | `leaves.jsonl` | `matrix/part-*.parquet` |
| `label` | leaves, matrix | `refs.jsonl`, `labels.jsonl`, `matrix_labeled.parquet` |
| `report` | all of the above | `report.md`, `report.json`, `*.png` |

```sh
make test            # unit tests
make e0              # whole pipeline on testdata/ with the fake backend (the CI test)
make pull-sessions   # rsync Claude Code logs from config.hosts into corpus/raw/claude-code
make probes          # print the observed raw-input schemas (field names only)
```

A real run uses the same commands with `config/sluice.yaml`, which uses the
Bedrock backend:

```sh
bin/harvest -run r1 -claude-code corpus/raw/claude-code \
  -claude-export corpus/raw/claude-export/conversations.json \
  -chatgpt-export corpus/raw/chatgpt-export/conversations.json \
  -repos ~/src -wildchat 3000
bin/tag -run r1 && bin/matrix -run r1 && bin/label -run r1 && bin/report -run r1
```

`report` defaults to the calibration split. You can open the test split once per run with
`-split test`.

## Configuration

`config/sluice.yaml` holds the model pool (Bedrock IDs, tiers, dated
prices), judge rules, taxonomy, thresholds, and seeds. Model IDs and prices
are never in code. Batch inference needs an S3 location and an IAM role:
set them with `SLUICE_BATCH_S3_URI` / `SLUICE_BATCH_ROLE_ARN`, or put them in the
gitignored `config/local.yaml`.

## Data handling

`corpus/` and `runs/` are gitignored in full. Raw exports, harvested
records, and model outputs never leave the local machine. Only aggregate
counts go out, in `docs/CORPUS.md`. A record that contains a credential,
account identifier, or bucket name is dropped, not redacted.

## Status

Project status lives on GitHub: issues, milestones E0–E3, and the project
board.

## License

Apache License 2.0. Copyright 2026 Scott Friedman. See [LICENSE](LICENSE)
and [NOTICE](NOTICE).
