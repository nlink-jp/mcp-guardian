# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).


## [0.9.0] - 2026-07-12

### Removed

- **darwin/amd64 (Intel) pre-built binary.** macOS releases now ship
  **arm64 only**, per the org-wide policy (darwin is Apple-Silicon only; no
  universal binaries). Intel Mac users can build from source.

### Changed

- **Release archive names now put the version before the os/arch**
  (`mcp-guardian-vX.Y.Z-<os>-<arch>.<ext>` instead of the old
  `mcp-guardian-<os>-<arch>-vX.Y.Z.zip`), matching the org-wide Release
  Archive Standard (`nlink-jp/.github` CONVENTIONS.md).
- **Linux release archives are now `.tar.gz`** (darwin/windows remain `.zip`).
- **`LICENSE` is now bundled** in every release archive alongside `README.md`.
- **darwin code-signature identifier** is now the canonical `mcp-guardian`
  (was `mcp-guardian-darwin-arm64`), set via `codesign -i` so it stays
  stable after the archived binary is renamed to its canonical name.
- **Dropped the `-s -w` linker strip flags**, aligning `GOFLAGS` with the
  org-standard form; also avoids a false-positive antivirus quarantine of
  the stripped Windows binary during cross-build.

No change to the binary's behaviour — a packaging / build-config release.

## [0.8.3] - 2026-05-27

### Fixed

- **Tokens issued without a refresh token are now treated as
  non-expiring instead of being declared dead after one hour.**
  Providers such as Slack (without token rotation enabled) return an
  access token with no `expires_in` and no `refresh_token`; the token
  never expires. Previously `--login` recorded a synthetic 1-hour
  `expires_at`, after which the proxy reported `access token expired …
  run --login again` even though the token was still valid — forcing a
  needless re-login every hour. `--login` now stores `expires_at: 0`
  ("no known expiry") for such tokens, and the token provider returns
  them as-is, relying on a real upstream 401 (surfaced as a JSON-RPC
  error, see 0.8.2) to detect genuine revocation. Existing
  `tokens.json` files written with the old synthetic expiry start
  working again with no re-login. Token rotation (refresh-token) flows
  are unchanged. (ADR-0003.)

## [0.8.2] - 2026-05-26

### Fixed

- **A client request that can't be forwarded upstream now returns a
  JSON-RPC error instead of hanging.** Previously, when forwarding a
  request failed (e.g. the SSE transport couldn't obtain an auth token
  because the stored OAuth token had expired with no refresh token),
  the proxy logged the error and sent nothing back — the MCP client
  blocked until its own timeout. The proxy now replies with a JSON-RPC
  error (code -32603) carrying the reason
  (`...access token expired ... run --login again`), so the client
  surfaces it immediately. (ADR-0002.)
- **Release zips store the binary under its canonical name** (e.g.
  `mcp-guardian`, not `mcp-guardian-darwin-arm64`), so it no longer
  needs renaming/symlinking on deploy. The zip filename keeps its
  arch suffix.

## [0.8.1] - 2026-05-22

### Changed

- **Releases are now Developer ID signed and Apple-notarized.**
  Darwin release zips (`mcp-guardian-darwin-{amd64,arm64}-vX.Y.Z.zip`)
  carry full Apple Developer ID Application signatures and
  notarization tickets from Apple. End users on macOS no longer need
  to bypass Gatekeeper with "right-click → Open" or
  `xattr -d com.apple.quarantine` on first launch — the binary is
  trusted by the OS out of the box. Local users who place
  `mcp-guardian` under Dropbox-synced (or any other FileProvider-
  managed) paths are no longer killed by macOS's ad-hoc + provenance
  distrust policy. Pipeline: `scripts/codesign-darwin.sh` +
  `scripts/notarize-darwin.sh`, driven by `make package`.
- **Release zip filenames now embed the version**
  (`mcp-guardian-<os>-<arch>-vX.Y.Z.zip`), matching the GH release
  asset convention this project has used since v0.8.0. No behaviour
  change to the binary itself.

## [0.8.0] - 2026-05-21

### Added

- **Pre-registered OAuth2 confidential client support.** mcp-guardian
  can now talk to MCP servers whose authorization server does not
  implement RFC 7591 Dynamic Client Registration — Slack
  (`https://mcp.slack.com/mcp`), GitHub Apps, Microsoft Entra ID, and
  most enterprise SaaS providers. The user pre-registers an OAuth app
  at the provider and writes the resulting `client_id` /
  `client_secret` into the profile. Design rationale and the resolved
  open questions are recorded in
  [`docs/en/adr/0001-pre-registered-oauth-client.md`](docs/en/adr/0001-pre-registered-oauth-client.md);
  the operational walkthrough lives in
  [`docs/en/reference/oauth2-manual-setup.md`](docs/en/reference/oauth2-manual-setup.md)
  (Japanese mirror under `docs/ja/`).
- **`auth.oauth2.callbackPort`** profile field. Fixed loopback port
  for the `--login` OAuth callback server. Pre-registered OAuth apps
  require an exact `redirect_uri` allow-list match; the
  previously-ephemeral port made this impossible. Zero / unset keeps
  the current ephemeral behaviour for DCR-capable providers.
- **`auth.oauth2.callbackScheme`** profile field. `"http"` (default,
  current behaviour) or `"https"`. When `"https"`, `--login` mints an
  ephemeral self-signed TLS certificate (SANs for `127.0.0.1`, `::1`,
  `localhost`; ECDSA P-256; 1-hour validity; held in memory only) and
  wraps the callback listener in `tls.NewListener`. Required by
  providers that reject `http://` loopback redirect URIs at app
  registration time — Slack is the documented case. Browsers display
  a one-time self-signed-cert warning that the user clicks through
  (loopback-only, never leaves the process). See ADR 0001 §Decision §4.
- **`auth.oauth2.clientAuthMethod`** profile field. Selects how
  client credentials are sent to the token endpoint:
  `"post"` (default — form body, the current behaviour), `"basic"`
  (HTTP `Authorization: Basic ...` header — required by Microsoft
  Entra ID and some Okta tenants), or `"none"` (PKCE-only public
  client; secret forbidden). Used by both initial token exchange and
  refresh.
- **`--callback-port N`** CLI flag. Overrides
  `profile.auth.oauth2.callbackPort` for a single invocation, handy
  for one-off debugging without editing the profile.
- **`examples/profiles/slack.json`**: worked-example profile for the
  official Slack MCP server. `<replace-with-…>` placeholders to
  prevent accidentally-committed secrets.
- **`scripts/docs-mirror-check.sh`**: enforces the `docs/en/` ↔
  `docs/ja/` structural mirror rule. Wired into `make check`.

### Changed

- **Profile JSON decoding is now strict.** Unknown fields are
  rejected at load time instead of being silently ignored. Found in
  practice: a profile with `"callbackSchema": "https"` (misspelled —
  the correct field is `callbackScheme`) used to load cleanly and
  then silently fall back to `http://`, which the Slack OAuth
  registration rejects. The strict path turns this kind of typo into
  an immediate parse error citing the offending field name.
- **Docs restructured to `docs/{en,ja}/{adr,reference,history}/`
  three-layer.** `docs/architecture.md` →
  `docs/en/reference/architecture.md` (+ Japanese mirror); same for
  `docs/otlp-setup.md`.
- **DCR-failed error message** now hints at the manual setup path.
  Previously: `authorization server does not support dynamic client
  registration`. Now: same line followed by a pointer to
  `docs/en/reference/oauth2-manual-setup.md` and the profile shape
  the user needs.
- `cli.Login(profileNameOrPath string)` is now
  `cli.Login(profileNameOrPath string, opts cli.LoginOptions)`.
  Internal-only function; the sole caller (main.go) has been updated.

## [0.7.1] - 2026-04-06

### Fixed

- `--login` and `--inspect` commands used the old `.governance` fallback for state directory, causing OAuth2 tokens and discovery cache to be saved in the wrong location after v0.7.0 migration.

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
