# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
semantic versioning on the 0.x series.

## [Unreleased]

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
