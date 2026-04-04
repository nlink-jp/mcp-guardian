# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).


## [0.2.0] - 2026-04-05

### Changed

- **Breaking: Remove `--upstream` flag** — Use `-- command [args...]` form exclusively. The `--upstream` flag used `sh -c` which allowed command injection. Direct `exec.Command` is now the only execution path.

### Added

- Input validation for all CLI flags (`--enforcement`, `--schema`, `--max-calls`, `--timeout`).
- Path canonicalization for `--state-dir` to prevent path traversal.
- Config `Validate()` method with unit tests.
- Unit tests for `jsonrpc` package (Parse, message classification, ID handling, error/result responses).
- Unit tests for `state` package (AtomicWrite, Controller, Authority, Constraints, Intent).
- E2E tests for proxy (initialize, tools/list, tools/call, budget enforcement, meta-tools).
- Error handling in `mustJSON()` (metatool) — no longer silently returns empty string.
- Proper error handling for `ledger.LoadAll()` in genesis hash initialization.
- Response body drain in webhook to prevent connection leaks.

### Fixed

- `UpstreamArgs` not passed to `proxy.Run()` — `-- command arg1 arg2` form lost all arguments.
- README.ja.md missing CLI Reference, Meta-Tools, and Architecture sections.

## [0.1.0] - 2026-04-04

### Added

- Initial release.
- stdio MITM proxy for MCP servers (JSON-RPC 2.0).
- Hash-chained receipt ledger (SHA-256, JSONL).
- 5-gate governance pipeline (budget, schema, constraint, authority, convergence).
- Failure-based constraint learning with TTL.
- Convergence/loop detection.
- 3-layer mutation classification (schema, verb heuristic, argument inspection).
- 5 governance meta-tools injected into tools/list.
- Post-session analysis CLI (--view, --verify, --explain, --receipts).
- Webhook notifications (generic, Discord, Telegram).
- .mcp.json wrap/unwrap integration.
- Zero external dependencies (Go standard library only).
- Single static binary (~6MB).


[0.2.0]: https://github.com/nlink-jp/mcp-guardian/releases/tag/v0.2.0
[0.1.0]: https://github.com/nlink-jp/mcp-guardian/releases/tag/v0.1.0
