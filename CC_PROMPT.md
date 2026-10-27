# sluice — handoff prompt for Claude Code

Read `PROJECT.md` first. Build only what this prompt scopes: the corpus harvester, the outcome-matrix runner, and the E0 report. E1–E3 get stubs and interfaces only.

## Repository

- Module `github.com/scttfrdmn/sluice`, Go 1.26. Apache License 2.0 (`LICENSE` file, standard text), `Copyright 2026 Scott Friedman` in `NOTICE` and in the SPDX header of every Go source file. Standard layout: `cmd/`, `internal/`, `config/`, `docs/`, `testdata/`.
- One binary per stage: `cmd/harvest`, `cmd/tag`, `cmd/matrix`, `cmd/label`, `cmd/report`. Each reads and writes files under a run directory (`runs/<run-id>/`), never a database. Parquet for the matrix (use `parquet-go`), JSONL for everything else.
- Config in `config/sluice.yaml`: model pool (Bedrock model IDs, provider, tier, price per M input/output tokens, price date), judge assignments, taxonomy, thresholds, seeds. No model IDs in code.
- Every command is idempotent and resumable: skip cells that already exist in the run directory. Cache tagger and judge outputs by SHA-256 of their input.
- `make e0` runs harvest → tag → matrix → label → report end to end on `testdata/` (20 synthetic requests, a fake-Bedrock backend) in under a minute. This is the CI test. The real run uses the same commands with the real backend.

## cmd/harvest

Inputs live under `corpus/raw/` (layout in PROJECT.md §5); add `corpus/` to `.gitignore` in the first commit. Sources, each behind a flag, all normalized to the record schema in PROJECT.md §5:

0. `make pull-sessions` — for each host in `config.hosts` (local, `juno.local`, `castor.local`), `rsync -az --include='*/' --include='*.jsonl' --exclude='*' <host>:~/.claude/projects/ corpus/raw/claude-code/<host>/projects/`. Local host copies rather than rsyncs over ssh. Print per-host file and byte counts. Fail loudly on an unreachable host; do not skip silently.

1. `--claude-code corpus/raw/claude-code` — walk every `<host>/projects/` subtree, parse each `*.jsonl` line as JSON, record `host` in `origin_ref`. For every kept user turn, build `context_text` from the preceding user turn and the preceding assistant turn's text blocks (no tool results), truncated to 2,000 tokens. The same rule applies to the two chat exports. First task: write a small probe that prints the distinct top-level keys and `type` values across the first 200 lines of 20 files and stops. Confirm the field names before writing the extractor; do not assume the structure in PROJECT.md is exact. Then keep user turns whose content is a string or a list containing `text` blocks; drop any turn containing a `tool_result` block, a `<command-name>` tag, or a `<system-reminder>` tag. Record `cwd`, session id, timestamp, turn index, and the character length of the preceding assistant turn.
2. `--claude-export corpus/raw/claude-export/conversations.json` — array of conversations; keep `chat_messages[]` entries with `sender == "human"`. Same probe-first rule.
2b. `--chatgpt-export corpus/raw/chatgpt-export/conversations.json` (or the directory, for per-conversation exports; see docs/SCHEMAS.md) — array of conversations with a `mapping` of nodes; reconstruct turn order by walking `parent` from `current_node`; keep nodes whose `message.author.role == "user"` and whose `content.parts` are strings (drop image/file parts). Source tag `chatgpt`. Probe first.
3. `--repos ~/src` — collect `PROJECT.md` and the case-insensitive prompt-file globs in PROJECT.md §5 (excluding `README*`, `CHANGELOG*`, `LICENSE*`, `node_modules`, `.git`; only from repos whose nearest git remote is owned by an owner in `harvest.prompt_file_owners` or that have no remote; matches under `vendor/` or `.hermit/` excluded), writing the matched list with outcomes to `runs/<id>/repo_prompt_files.txt`; and, if `gh` is on PATH and authenticated, `gh issue list --limit 500 --json number,body,author --state all` for each repo with a GitHub remote, keeping issues whose author login matches `--owner`.
4. `--wildchat N` — download WildChat-1M via the Hugging Face datasets API (Python helper in `tools/wildchat.py`, since there is no Go client), English, first user turn, uniform sample of N with the configured seed, written to JSONL that `harvest` ingests.

Post-processing: language filter (English only), length ≥ 20 chars, MinHash near-dedup at Jaccard 0.85, credential/identifier regex pass per PROJECT.md §5 (unconditional key/token patterns; 12-digit numbers only within 40 chars of an AWS context token) — drop the record. Print a source × host × kept/dropped table and per-pattern drop counts; write a 50-record drop sample to `corpus/review/drops.jsonl`. Exclude the sluice repo itself from the `repos` source. Assign `split` 50/50 by seeded hash of `id`. Set `author_kind` and `author_rule` on every record per PROJECT.md §5. `harvest subsample` draws the E0 subsample (PROJECT.md §5) into `runs/<id>/subsample.json` and the matching test draw into `subsample_test.json`; `tag` restricts to the calibration one unless `-all`, and reads the test one only with `-subsample test`. `estimate` projects E0 spend from the subsample: assumed leaf structure before tagging, measured after.

## cmd/tag

Strong-model tagger for E0. Prompt template in `config/tag_prompt.txt`; the model sees `text` and `context_text` and returns strict JSON: `{standalone: bool, leaves: [{span_text, operation, domain, needs: [leaf_idx], verifier_hint}]}` against the taxonomy in config. Locate each `span_text` in the request verbatim (whitespace-normalized); a span that is not found fails the tag. Validate against the taxonomy; on invalid output retry once with the error appended, then record the request as `tag_failed`. Cache by text hash. Emit `leaves.jsonl` with one row per leaf carrying `request_id`, `leaf_idx`, class, span text, and the text to send to models (span plus any `needs` context the tagger marked).

## cmd/matrix

For each leaf × model × draw ∈ {0,1}: send `context_text` (if any) as a "Prior context:" preamble followed by the leaf text. Call the model via Bedrock — batch inference when the cell count exceeds 1,000, on-demand otherwise, plus a seeded 5% on-demand subsample per model regardless. Record output, token counts, cost from the price table, and latency: `latency_ms` for on-demand calls (including queueing), `batch_wall_ms` in a separate column for batch cells, never mixed. Batch S3 bucket and IAM role from env vars or a gitignored `config/local.yaml`. Concurrency and rate limits per model in config. Write rows as they complete; resume skips existing rows.

Local lane: stub the OpenAI-compatible client behind the same interface; do not enable it for the first run.

## cmd/label

Per PROJECT.md §6. Implement rungs in this order and record which rung labeled each cell:

- Code classes: extract the first fenced code block; language from the fence; compile/run in a scratch container (Docker, `--network none`, 30 s timeout) against a test generated once per leaf by the strong model and stored with the leaf. Record compile-only as rung 2.
- Numeric `reason`: regex the final number, apply GSM8K-style normalization, exact match against a reference produced once per leaf by the strong model with two draws that must agree (else leaf is `no_reference` and falls to judge).
- Format/extract classes: parse/schema check from the tagger's `verifier_hint` plus judge.
- Everything else: two judges, one per provider, neither the judged model itself, M-tier preferred (assignments in config). Judge prompt in `config/judge_prompt.txt`, returns `{correct: bool, reason}`. Label correct iff both agree; record disagreement and both judge identities per cell.

Correctness for (leaf, model) = correct on both draws, for every quantity in the report (best-single, best-per-class, oracle). Store per-draw labels too.

## cmd/report

Produce `runs/<id>/report.md` and `report.json`:

1. Corpus table: source, count, mean leaves per request, fraction compound (≥2 leaves), class histogram.
2. Noise table: per class, fraction of (leaf, model) cells whose two draws disagree; judge disagreement rate.
3. Per class: n, per-model accuracy with Clopper–Pearson 95% CI, cheapest model clearing 5% and 10% upper-bound thresholds, best-per-class model, oracle accuracy.
4. Headline: best single model, best-per-class, per-instance oracle, with selection-valid intervals (oracle at δ/2, each of K models at δ/(2K), gap interval = [max(0, L_q − max_m U_m), min(1, U_q − max_m L_m)]). Class-oracle fraction ρ with a paired bootstrap interval recomputing numerator and denominator jointly. Report pooled, owner-only, public-only, and standalone vs context-dependent.
5. Cost and latency per model, measured, and the cost of the best-per-class policy vs always-strong.
6. Decision line per PROJECT.md §4 E0, printed verbatim with the numbers that triggered it.

Plots (PNG via `gonum/plot`): accuracy–cost scatter per model with the best-per-class and oracle points; per-class ρ bar chart.

## Stubs

`internal/tagger` (encoder interface + the strong-model implementation), `internal/router` (class table lookup, cascade decision, interfaces only), `internal/dag` (leaf graph, critical path; wire to quarry's interfaces if that is simpler than re-implementing), `internal/certify` (copy the LTT/Clopper–Pearson code from go-cascade rather than re-deriving it). Each stub compiles and has a doc comment pointing at the PROJECT.md section.

## Rules

- No account IDs, bucket names, or credentials in the repo, in test data, or in the corpus. The harvester's regex pass is a hard drop, not a redaction.
- Prices and model IDs live in config with a date. The report prints them.
- Do not add a third draw, a new class, or a new model without recording the change and the reason as a comment on the relevant GitHub issue.
- Do not declare E0 complete, passed, or failed. When the pipeline runs end to end on the real corpus, print the decision line with its numbers and stop. If ρ < 0.50, run the one taxonomy iteration, print again, and stop. Scott decides.
- `tag`, `matrix` and `label` require `-max-spend <usd>` (PROJECT.md §7, spend control). Caps are set per stage on #7. After the tag pass, re-run `estimate`, post the revised projection on #7, and stop until it is approved.
- When something in the environment blocks a step (a Bedrock quota, a parse error in the session logs, a judge that will not return JSON), fix it and record what was done; do not substitute a weaker measurement silently.

## First session

0. Rename the two files to `PROJECT.md` and `CC_PROMPT.md`, `git init`, create public `scttfrdmn/sluice` with milestones E0–E3, labels, a project board, and one issue per step below; first commit at 0.1.0.
1. Scaffold the repo (with `corpus/` gitignored) and the fake-Bedrock backend; `make e0` green on `testdata/`.
2. `make pull-sessions` against all configured hosts; report per-host counts.
3. Write the three probes (Claude Code JSONL, claude.ai export, ChatGPT export); run them on real files; adjust the extractors; commit the observed schemas to `docs/SCHEMAS.md`.
4. Run `harvest` on all sources; commit the source × host × kept/dropped table to `docs/CORPUS.md`.
5. Stop and report counts before any model calls are made.
