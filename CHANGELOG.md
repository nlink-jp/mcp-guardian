# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).


## [0.7.0] - 2026-04-06

### Changed

- **Breaking: Default state directory moved** from `<cwd>/.governance/` to `~/.config/mcp-guardian/state/<profile-name>/`. Profiles with explicit `stateDir` are not affected. This fixes receipts being written to unpredictable locations when launched by MCP clients.
- **Receipt files are now per-process** -- Each proxy writes to `receipts-<unixmilli>-<pid>.jsonl` instead of a shared `receipts.jsonl`. This eliminates concurrent write conflicts without file locking.
- Analysis commands (`--view`, `--verify`, `--explain`, `--receipts`) now aggregate all receipt files, sorted by timestamp.
- `VerifyChain` verifies each receipt file independently and reports which file is broken.

### Fixed

- Receipt `Append` no longer updates in-memory state (seq, lastHash) when disk write fails. Previously, `governance_status` could report receipt depth higher than what was actually persisted.
- `DefaultStateDir` rejects empty profile names and path traversal attempts.
- `LoadAllReceipts` continues reading remaining files when one file has errors, instead of stopping at the first failure.
- `Purge` deletes receipt files that become empty after purging old records, instead of leaving zero-byte files.
- Removed stale "Inline mode" section from README (removed in v0.5.0).

### Backward Compatibility

- Legacy `receipts.jsonl` files are still read by all analysis commands and `LoadAllReceipts`.
- Profiles with explicit `stateDir` continue to use the specified path.

## [0.6.1] - 2026-04-06

### Fixed

- Mutation classifier now splits camelCase tool names into verb tokens. Tools like `getConfluenceSpaces` and `atlassianUserInfo` were misclassified as `mutating` because only `_` and `-` delimiters were recognized.

## [0.6.0] - 2026-04-06

### Added

- **`--inspect`** -- Show server info and available tools for a profile. Connects to the MCP server, retrieves capabilities, and displays tool names, descriptions, and parameter schemas.

### Fixed

- Tool masking documentation clarified: requires `enforcement: "strict"` to take effect. In `advisory` mode, masked tools are logged but not hidden.

## [0.5.1] - 2026-04-05

### Fixed

- Receipt timeline (`--view`) now shows date and time (`2026-04-05 21:09:40`) instead of time only (`21:09:40`).

## [0.5.0] - 2026-04-05

### Changed

- **Breaking: `--profile` is now required for proxy mode** -- All transport, authentication, governance, and masking settings are configured exclusively via server profiles (JSON files). No inline CLI flags.
- **Breaking: Removed 22 inline CLI flags** -- `--enforcement`, `--schema`, `--max-calls`, `--timeout`, `--transport`, `--upstream-url`, `--sse-header`, `--oauth2-*`, `--token-command*`, `--mask`, `--mask-file`, `--otlp-*`, `--webhook`.
- **Breaking: Removed `--wrap` / `--unwrap`** -- Use `claude mcp add <name> -- mcp-guardian --profile <name>` directly.
- CLI reduced from 34 flags to 13. `main.go` reduced from 405 to 170 lines.
- Analysis commands (`--view`, `--verify`, `--explain`, `--receipts`) now require `--profile` or `--state-dir`.

### Fixed

- Analysis commands show "No receipts" instead of error when receipts file does not exist.
- E2E test no longer opens browser (MCP_GUARDIAN_NO_BROWSER environment variable).
- AutoDiscoveryLogin E2E test simulates browser redirect instead of timing out.

## [0.4.0] - 2026-04-05

### Added

- **SSE transport** -- Connect to HTTP/SSE (Streamable HTTP) MCP servers via `--transport sse --upstream-url <url>`, or via server profiles.
- **Server profiles** -- One JSON file per MCP server (`--profile <name|path>`). Replaces `--server-config`. Stored in `~/.config/mcp-guardian/profiles/`.
- **MCP Authorization Discovery** -- Auto-discovers OAuth2 endpoints and registers clients dynamically (RFC 8414 + RFC 7591). No manual OAuth app setup required.
- **OAuth2 authorization_code flow** -- `--login <profile>` opens browser for interactive authentication with PKCE. Tokens stored locally, auto-refreshed via refresh_token.
- **OAuth2 client_credentials flow** -- Machine-to-machine authentication with automatic token caching and refresh.
- **External token command** -- `tokenCommand` in profiles to integrate with `gcloud`, `vault`, etc.
- **401 auto-retry** -- Transparently invalidates and refreshes tokens on HTTP 401 responses.
- **Splunk HEC driver** -- Built-in Splunk HTTP Event Collector exporter, runs in parallel with OTLP.
- **Exporter interface** -- Pluggable telemetry backend architecture (`export.Exporter`).
- **Receipt auto-purge** -- `maxReceiptAgeDays` (default: 7) removes old receipts on startup. Telemetry backends are the durable store.
- **Tail-read startup** -- Ledger reads only the last line on startup, O(1) regardless of file size.
- **Global config auto-discovery** -- System config auto-loaded from `~/.config/mcp-guardian/config.json`.
- **Profile listing** -- `--profiles` lists available profiles.
- New CLI flags: `--profile`, `--profiles`, `--login`, `--transport`, `--upstream-url`, `--sse-header`, `--oauth2-token-url`, `--oauth2-client-id`, `--oauth2-client-secret`, `--oauth2-scope`, `--token-command`, `--token-command-arg`.
- New packages: `internal/transport`, `internal/export`.
- `docs/architecture.md` -- Comprehensive architecture reference.
- `docs/otlp-setup.md` -- Setup guides for AWS, GCP, Grafana Cloud, Datadog, Splunk HEC, self-hosted.
- `examples/` -- Config and profile templates (stdio, SSE, OAuth2, Atlassian, auto-discover).

### Changed

- **Breaking: `--server-config` removed** -- Use `--profile` instead. Server profiles use a structured format with `upstream`, `auth`, `governance` blocks.
- **Global config format** -- Telemetry settings moved under `telemetry` block. Legacy top-level `otlp`/`webhooks` fields removed.
- Proxy internals refactored: upstream communication via `transport.Transport` interface, telemetry via `export.Exporter` interface.
- `ServerConfig` type removed from codebase.

### Security

- OAuth callback HTML output escaped with `html.EscapeString` to prevent XSS.
- OAuth state parameter compared with `crypto/subtle.ConstantTimeCompare`.
- ExtraParams cannot override security-critical OAuth parameters (state, redirect_uri, code_challenge).
- Receipt files created with mode `0600` (was `0644`).
- Receipt writes and purge use `f.Sync()` before close to prevent data loss on crash.
- All HTTP response bodies limited to 1MB via `io.LimitReader`.
- `writeToAgent` protected by mutex for concurrent stdout safety.
- Discovery and Splunk HEC use dedicated `http.Client` with timeouts.
- `readLastRecord` backward scan capped at 64KB to prevent OOM on corrupted files.

## [0.3.0] - 2026-04-05

### Added

- **Tool masking** -- Hide tools from agents via glob patterns (`--mask`, `--mask-file`, or `--server-config`). Masked tools are removed from `tools/list` and calls return generic "tool not found". In `advisory` mode, masked tools are logged but not hidden.
- **OTLP/HTTP telemetry export** -- Export audit data as OpenTelemetry Logs + Traces via OTLP/HTTP JSON encoding. Zero external dependencies. Batched with flush on size, timer, or shutdown. Local receipts remain authoritative.
- **Two-tier configuration files** -- `--config` for global/system settings (OTLP, webhooks, defaults) and `--server-config` for per-MCP-server policies (mask, enforcement, etc.). CLI flags override both.
- **Integration test infrastructure** -- `make integration-test` runs OTLP E2E tests against a real OTel Collector container (podman/docker). Scripts: `scripts/otel-up.sh`, `scripts/otel-down.sh`.
- New CLI flags: `--mask`, `--mask-file`, `--otlp-endpoint`, `--otlp-header`, `--otlp-batch-size`, `--otlp-batch-timeout`, `--config`, `--server-config`.
- New packages: `internal/mask`, `internal/otlp`.

### Changed

- **Breaking: `--config` flag repurposed** -- Previously pointed to `.mcp.json` (wrap/unwrap). Now points to global guardian config. Use `--mcp-config` for `.mcp.json`.

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


[0.3.0]: https://github.com/nlink-jp/mcp-guardian/releases/tag/v0.3.0
[0.2.0]: https://github.com/nlink-jp/mcp-guardian/releases/tag/v0.2.0
[0.1.0]: https://github.com/nlink-jp/mcp-guardian/releases/tag/v0.1.0
