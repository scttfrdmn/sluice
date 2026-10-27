# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
semantic versioning on the 0.x series.

## [Unreleased]

### Added
- `author_kind` (`human`/`agent`/`unknown`) and `author_rule` on every record and leaf. Agent-written GitHub issues (generated footer, or ≥2 `## ` headings and ≥400 chars) are kept and identified instead of skipped.
- `harvest subsample`: seeded, stratified E0 subsample of the calibration split (1,000 per stratum) written to `runs/<id>/subsample.json`; `tag` and `report` restrict to it (`tag -all` overrides).
- `estimate`: projected E0 spend by model and stage from the price table and the subsample's token statistics.
- Report: every table broken out by source and by `author_kind`; a source-balanced pooled ρ next to the natural-weight one; noise by group; class histogram by group.
- `runs/<id>/repo_prompt_files.txt`: every matched repo prompt file and its outcome.
- ChatGPT export: `-chatgpt-export` and `probe chatgpt-export` accept a directory of per-conversation JSON files as well as `conversations.json`. Observed schema in `docs/SCHEMAS.md`.

### Changed
- Repo prompt files match case-insensitive globs (`*kickoff*prompt*.md`, `*CLAUDE_CODE_PROMPT*.md`, `*PROMPT*.md`, `*_prompt.md`, `*.prompt.md`) plus `PROJECT.md`, excluding `README*`, `CHANGELOG*`, `LICENSE*`.

## [0.1.0] - 2026-10-26

### Added
- E0 pipeline: `harvest`, `tag`, `matrix`, `label`, `report`, plus a `probe` for raw-input schemas.
- Harvest sources: Claude Code session logs (multi-host via `make pull-sessions`), claude.ai and ChatGPT exports, repo prompt files and owner-authored GitHub issues, and a WildChat public control (`tools/wildchat.py`).
- Credential, account-ID, and bucket filters (hard drop), English filter, MinHash near-dedup, and a seeded calibration/test split.
- Strong-model tagger with verbatim span location and a taxonomy re-tag mode (`tag -reclass`).
- Outcome matrix over Bedrock (Converse on demand, model-invocation jobs in batch) with a seeded on-demand latency subsample. Stored as parquet part files.
- Labeling rungs: Docker-sandboxed test execution, compile-only, numeric exact match, format parse checks, and a two-judge panel.
- E0 report: selection-valid intervals, class-oracle fraction ρ with a paired bootstrap, per-class diagnostics, noise, cost and latency, and plots.
- Fake backend and synthetic testdata so `make e0` runs end to end offline.
- Interface stubs for E1–E3: `internal/tagger` encoder, `internal/router`, `internal/dag`, and `internal/certify` (code copied from go-cascade).
