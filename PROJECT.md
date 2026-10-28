# sluice

Class-based request routing on a task graph, with per-class certified quality. Experiment program first; the routing library follows only if E0 clears its gate.

Module: `github.com/scttfrdmn/sluice`. Go 1.26. Apache License 2.0, Copyright 2026 Scott Friedman. Bedrock for hosted models; optional open-weight lane on local hardware.

A sluice separates material by class as it flows, without inspecting each grain. That is the design: tag what a request *is*, route by a table, never predict per-instance difficulty.

## 1. Claim

On a corpus of real requests, a pipeline of

1. span tagger (operation × domain classes, plus dependency edges),
2. per-class default model from a certified table, and
3. verifier-gated escalation only where a verifier exists

achieves verified quality within ε of the strong-model-on-everything baseline at a cost lower than that baseline by a margin that pays for the tagger and wasted escalation calls, and lower than the best plateau router at matched quality.

If the first cost condition fails, the project ends. If it holds and the second fails, the result is existing routing performance with per-class diagnostics, which is worth less but still reportable.

## 2. Background (what is settled)

- Prompt-only routers converge to a narrow accuracy band far below the per-instance oracle (21 methods, 5 benchmarks; kNN is competitive with everything). More data and larger encoders move the band by ~1 pp.
- The realizable share of oracle opportunity for prompt routers is 7.5–14.4% under selection-valid inference; the simultaneous CI for the best of eleven policies has lower limit zero.
- Per-task-type static assignment explains most of the *reproducible* routing gain (21 of 29 routable items on a de-noised matrix). On LLMRouterBench the per-dataset oracle captures ~22% of the gap. The spread is taxonomy and label noise.
- Single-draw oracles are upward-biased: 5.37% of model-question pairs flip between identical runs. Judge-based labels diverge from exact-match by 10–24 pp on knowledge tasks.
- Calibrated thresholds (isotonic on token margin) and ranking objectives produce the only cost gains that replicate (31% at fixed F1; ~17% at GPT-4-level).
- The generating model's verbal confidence is not a usable signal; the weakest models are the worst calibrated (ECE 0.27–0.30).

Consequences for design: quality is a per-class constraint, not a per-instance score. The only per-instance signals used are external (verifier, calibrated probe, judge). The oracle is multi-draw and exact-match anchored.

## 3. Pipeline (target system, built only after E0)

```
request ─► tagger ─► leaves {span, class, needs} ─► DAG ─► per-leaf route ─► execute+verify ─► join ─► response
                                                      │
                                                      └─ class table: {default model, certified err, threshold, verifier rung}
```

- **Tagger.** Small encoder (ModernBERT/DeBERTa class, ≤400M params) with a token-classification head for class and a pairwise head for `needs` edges. Trained on strong-model labels, evaluated by span-F1 and edge P/R. Until trained, the strong model is the tagger.
- **Class.** Defined by what can be done with it: (verifier rung, candidate models, threshold). Two axes, sparsely crossed:
  - operation: `extract`, `transform` (code↔code, format↔format), `summarize`, `classify`, `gen-prose`, `gen-code`, `reason` (math, multi-step), `answer-factual`, `plan`, `converse`
  - domain: `general`, `code`, `sci-tech`, `legal-finance`, `medical`, `creative`
- **Verifier rungs.** 1 executable (tests, exact answer, schema + run); 2 partial (compiles/parses/validates/citations resolve/format); 3 calibrated external estimator (probe, k-sample consistency, judge — isotonic-calibrated per class); 4 class certificate only.
- **Quality constraint.** Node passes if its best available rung meets the class threshold. Default model = cheapest whose certified error *upper bound* clears the threshold. Where the default does not clear but a rung-1/2 verifier exists, cascade: run default, verify, escalate with prompt + failed attempt + verifier output.
- **Scheduler.** Cost = Σ nodes; latency = critical path. Workload class sets which is bound and which is objective (interactive: latency bound; batch: cost bound; agentic: upstream thresholds tightened). Shared prefixes cached once. Joins are nodes with their own class and verifier.
- **Thresholds.** Supplied by the workload owner. Default: "no worse than the strong model's own certified error on this class."
- **Recalibration.** Per (class, model) on model change or traffic drift. Costs a labeled-set rerun, not a tagger retrain.

## 4. Experiments

### E0 — class-oracle fraction (gate)

Deliverable: for each class and overall, three numbers — best single model, best fixed model per class, per-instance oracle — with selection-valid 95% intervals (Clopper–Pearson, Bonferroni over K models; oracle interval at δ/2, each model at δ/(2K)).

Class-oracle fraction ρ_c = (best-per-class − best-single) / (oracle − best-single).

Decision: ρ ≥ 0.50 → proceed to E1. ρ < 0.50 → iterate the taxonomy once on the same matrix (re-tag only) and re-measure. After that one iteration, the report prints the number and the rule; Scott decides whether to proceed. Nothing in the pipeline declares the gate passed or failed on its own.

Also report per-class: n, certified error per model, cheapest model clearing a 5% and a 10% threshold, within-class residual (oracle − best-per-class), verifier rung available.

### E1 — tagger

Distill strong-model labels into the encoder. Metrics: span-F1 (class), edge precision/recall. Decision: P(misroute) × cost(misroute) < saving per request, both from E0.

### E2 — pipeline on compound requests

Held-out requests with ≥2 leaves. Arms: (a) strong model on whole prompt; (b) best fixed model per whole-prompt class; (c) kNN router and K-head encoder router fit on the E0 matrix; (d) sluice pipeline. Measure verified quality, measured $ (tokens × price table, including tagger and wasted calls), makespan with real Bedrock latency. Report Pareto per class and overall.

### E3 — cascade value by class

For each class with a rung-1/2 verifier: cost per verified-correct leaf, cascade vs default-only.

### Positive result

E0 ρ ≥ 0.50. E2: cost ≤ 0.4× arm (a) at verified quality within 2 pp; cost ≤ 0.8× best of arm (c) at matched quality; makespan ≤ 1.2× arm (a). E3: ≥3 classes where cascade beats default by ≥25% on cost per verified-correct.

## 5. Corpus

Four sources, harvested by `cmd/harvest`, normalized to one record schema, deduplicated, and labeled with source.

1. **Claude Code session logs.** `~/.claude/projects/<encoded-cwd>/<session>.jsonl` on each lab machine. One JSON object per line. Keep records where `type == "user"` and the message content is human-typed text (string content, or text blocks); drop `tool_result` blocks, slash-command meta turns (`<command-name>`), and system-injected reminders. Record `cwd`, `sessionId`, `timestamp`, position in session, and the preceding assistant turn's length as context size. Verify the field names against actual files before relying on them; the format is undocumented and has changed.
2. **claude.ai data export.** `conversations.json` from the account export zip: array of conversations, each with `chat_messages[]` carrying `sender` (`human`/`assistant`), `text`, `created_at`. Keep human turns. Record conversation id and position.
2b. **ChatGPT data export.** `conversations.json` from the OpenAI export zip: array of conversations, each with a `mapping` dict of nodes; each node's `message` has `author.role` (`user`/`assistant`/`system`) and `content.parts[]`. Walk from `current_node` up the `parent` chain to recover turn order. Keep `user` turns with string parts. Source tag `chatgpt`. Same probe-first rule as the other exports.

Raw inputs live under `corpus/raw/` in the repo, gitignored (personal traffic never enters git):

```
corpus/raw/claude-export/conversations.json
corpus/raw/chatgpt-export/conversations.json  (or json/<title>_<id>.json, one per conversation)
corpus/raw/claude-code/<host>/projects/...     one subtree per machine, rsynced
corpus/raw/wildchat/sample.jsonl
```

Claude Code logs from other machines are rsynced into `corpus/raw/claude-code/<host>/` by `make pull-sessions` (hosts in config: at least the local machine, `juno.local`, `castor.local`); the harvester records `host` in `origin_ref` and reports per-host counts.
3. **Repo prompts.** Walk `~/src/*`: `PROJECT.md`, and files matching (case-insensitive) `*kickoff*prompt*.md`, `*CLAUDE_CODE_PROMPT*.md`, `*PROMPT*.md`, `*_prompt.md`, `*.prompt.md` — excluding `README*`, `CHANGELOG*`, `LICENSE*` and anything under `node_modules` or `.git`. Prompt files are taken only from repos whose remote owner is `scttfrdmn`, `playgroundlogic`, `provabl` or `trnsci`, or that have no remote, judged by the nearest enclosing git repository, so a third-party clone nested in an owner directory is excluded; matches under `vendor/` or `.hermit/` are excluded too. Plus GitHub issue bodies authored by the owner (via `gh issue list --json body,author`). These are compound by construction. The harvest writes every matched file, with its outcome (kept, dropped by a filter, or excluded and why), to `runs/<id>/repo_prompt_files.txt`, so the exclusions are visible.
4. **Public control.** WildChat-1M, English, first user turn only, uniform sample of 3,000. Controls for the owner's traffic being unrepresentative.

Record schema:

```
id, source, origin_ref, timestamp, author_kind, author_rule, text, context_text, standalone, lang, has_attachment, split
```

**Author kind.** Agent-written requests are normal traffic in an agentic deployment and belong in the corpus: they are identified, never dropped or down-weighted. `author_kind` ∈ {`human`, `agent`, `unknown`}, assigned per source, and `author_rule` records which rule fired:
- Claude Code typed and queued turns → `human` (`claude-code-turn`). Chat-export human turns → `human` (`chat-export-turn`). WildChat → `human` (`wildchat-user-turn`).
- GitHub issues → `agent` if the "Generated with Claude Code" footer is present (`issue-footer`) or the body has ≥2 `## ` headings and ≥400 chars (`issue-structure`); otherwise `unknown` (`issue-no-rule`).
- Repo prompt files → `agent` (`repo-prompt-file`): drafted in chat and handed off.

Every E0 number is reported by `author_kind` and by source as well as pooled. Next to the natural-weight pooled ρ the report gives a source-balanced pooled ρ (equal total weight per source), so a dominant source's share is visible rather than hidden.

**E0 subsample.** E0 does not run the full corpus. `harvest subsample` draws a seeded, stratified subsample of the calibration split: 1,000 requests per stratum (Claude Code, GitHub issues, WildChat, each chat export once present; repo prompt files are their own stratum and are all taken, being few). The ids, seed and per-stratum counts are written to `runs/<id>/subsample.json`; `tag`, and through it `matrix`, `label` and `report`, restrict to it. The full corpus is reserved for E1 tagger training and E2. The matching test-split draw (same seed scheme, same 1,000 per stratum) is written at the same time to `runs/<id>/subsample_test.json` and is not tagged or run. The test split stays closed until the calibration result warrants opening it; opening it is one command (`make open-test`), with its spend budgeted separately at that point.

Per-class accuracy tables are pooled: at 1,000 per source, a class × source × model cell is too thin to certify. The report adds a class × source × `author_kind` count table (n only) so the sparsity is visible. Cost and latency are reported per model, pooled; they are properties of the model.

**Context-dependent requests.** Many turns in session logs assume prior context ("fix that", "now the tests"). Sent bare, they score as uniform failures and shrink ρ artificially. Handling:
- `context_text` = the preceding user turn plus the preceding assistant turn's text blocks only (no tool results), truncated to 2,000 tokens, oldest first. First turns have empty context.
- The tagger sets `standalone` (true if the request is interpretable without `context_text`).
- Models receive `context_text` as a "Prior context:" preamble when present. This is the deployment condition — a router sees the conversation, not a bare turn.
- Report ρ for standalone and dependent requests separately and pooled. The bare-text variant of dependent requests is not run; it measures nothing.

Exclusions: text < 20 chars, non-English (langdetect), near-duplicates (MinHash, Jaccard > 0.85), anything containing credentials or account identifiers — drop the record, do not redact. Patterns: `AKIA[0-9A-Z]{16}`, AWS secret-key shape, bearer/JWT tokens, private-key headers unconditionally; a 12-digit number only within 40 chars of an AWS context token (`arn:`, `account`, `aws`, `sts`, `assume`, `role/`, `::`). Report drop counts per pattern and write a 50-record sample of drops to `corpus/review/drops.jsonl` (gitignored) for manual check. The sluice repo's own files are excluded from the `repos` source.

Target: ≥3,000 owner-sourced + 3,000 public. Split 50/50 calibration/test by a fixed seed; test opened once.

## 6. Outcome matrix

For every leaf × model × draw (2 draws, temperature per model default):

```
leaf_id, model, draw, output, tokens_in, tokens_out, latency_ms, cost_usd, label, label_method, judge_agreement
```

Labeling by class:
- `gen-code`, `transform(code)`: extract code; compile; run the strong model's generated test (generated once per leaf, held fixed across models); rung 1. Compile-only fallback is rung 2 and recorded as such.
- `reason` with numeric answer: exact match on final number (official normalization); rung 1.
- `transform(format)`, `extract`: schema/parse check + judge; rung 2.
- everything else: two judges from different providers, each shown the prompt and output, exact-match anchored where a reference exists; label = correct only if both agree; disagreement recorded. Rung 3.

Correctness for a (leaf, model) = correct on *both* draws. This single definition is used for best-single, best-per-class, and the oracle; otherwise ρ is biased. Single-draw results are kept for the noise analysis only.

Judges: one from each provider in the pool, neither being the judged model itself, M-tier preferred. With two providers one judge necessarily shares a provider with the judged model; that is accepted and the judge identities are recorded per cell. A third-provider judge is added only if the open-weight lane is in.

Pool (config, not code): Anthropic S/M/L (Haiku/Sonnet/Opus 5.5) and the current Amazon Nova S/M/L tiers as listed in Bedrock on the run date — Claude Code checks the model list and records exact IDs in config. Strong model for tagging and reference generation: Opus 5.5. Open-weight lane: out for the first E0 run; interface stubbed; added as a second pass if E0 clears. Price table in config with date.

Latency: on-demand calls record per-call latency including queueing. Batch-mode cells record batch wall time in a separate flagged column and are not used for latency analysis. Each model gets a 5% on-demand subsample for real latency in E0; E2 is on-demand throughout.

Budget estimate: 6,000 requests × ~2.2 leaves × 6 models × 2 draws ≈ 160k calls; labeling pass ~13k strong-model calls; judge pass ~2 × 160k small-model calls. Low hundreds of dollars on Bedrock batch. Wall clock ≈ one weekend with batch inference.

## 7. Decisions (decided unless objected)

- Name `sluice`; module `github.com/scttfrdmn/sluice`; Go 1.26; Apache 2.0, Copyright 2026 Scott Friedman.
- E0 is the only experiment built in the first handoff. E1–E3 get directory stubs and interfaces, no implementation.
- Taxonomy v0 as in §3. Changing it is one re-tag pass over the same matrix; the matrix is never regenerated for a taxonomy change.
- Strong model is the tagger for E0 (no encoder yet). Tagger output is cached per request text hash.
- Two draws per cell, not more; the noise analysis reports the flip rate and that decides whether a third is needed.
- Judges are from a different provider than the model being judged where possible; never the same model.
- Owner traffic and public control are reported separately and pooled; the headline number is the pooled one. Every source is also reported separately (Claude Code, claude.ai, ChatGPT, repos, WildChat), since they have different compound fractions by construction, and every number is also broken out by `author_kind`, with a source-balanced pooled ρ beside the natural-weight one (§5).
- `corpus/` is gitignored in full. Raw exports and harvested records never leave the local machine except as aggregate counts in `docs/CORPUS.md`.
- Prices from the Bedrock price list on the run date, stored with the matrix. Latency measured per call, including queueing.
- Account IDs, bucket names, and credentials never enter the corpus or the repo. Batch-inference S3 bucket and IAM role come from environment variables or a gitignored local config.
- Decisions are recorded as GitHub issue comments on scttfrdmn/sluice, not in a local markdown file. `docs/SCHEMAS.md` and `docs/CORPUS.md` are reference documents and stay in the repo.
- Repo is public, like the rest of scttfrdmn/*; the corpus never enters it.
- The tagger returns quoted span text, not character offsets; the harness locates the text in the request and fails the tag if it is absent.
- **Spend control.** Every stage that calls a model (`tag`, `matrix`, `label`) requires `-max-spend <usd>` and will not start without it. Cost accumulates from measured token counts and the price table as calls complete, cumulative with the stage's earlier passes over the same leaves; the stage halts cleanly (resumable) when the running total crosses the cap and prints the total. Batch jobs reserve their worst case (prompt plus `max_tokens` of output) before submission, so a batch job never takes the stage past the cap. Scott sets the cap per stage on the spend-approval issue (#7); nothing runs without it.
- **Order of operations for E0 spend.** Once the caps are set: the tag pass on the calibration subsample first; then re-run `estimate`, which now uses measured leaves per request and leaf token lengths, post the revised projection on #7, and stop. Matrix and label then run first on a pilot (`harvest pilot`: 50 calibration leaves per stratum, source with repos split by author_kind, seed `seeds.pilot`; matrix on demand), under their own caps. `estimate` then uses the pilot's measured output tokens, label cost per cell and reference cost, and reports judge agreement and stop reasons; that revision is posted on #7 and approved before the full matrix and label. Pilot cells are kept and not re-run.

## 8. Related internal work

- `go-cascade`: verifier ladder and LTT certificates; E3 is its generalization across classes. Reuse the certificate code.
- `quarry`: decomposition executor with provenance; the E2 DAG runner should sit on it or copy its interfaces.
- `assay`: certified error-rate reporting conventions.
- `scientific-codes-cookbook`: harness conventions (recipe = reproducible run with a check).
- `ember` (cloned at `~/src/ember` on terror): batched prompt scheduling with per-prompt deadlines; the E2 scheduler's cost/latency model.

## 9. What this is not

Not an instance-difficulty router. Not a new router architecture. Not a benchmark leaderboard. The contribution, if any, is the measured class-oracle fraction on real compound traffic, the segment-not-classify design, and the per-class diagnostic that says *why* routing worked or did not.
