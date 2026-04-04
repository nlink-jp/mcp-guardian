# Changelog

## v0.1.0 (2026-04-04)

Initial release.

### Features

- stdio MITM proxy for MCP servers (JSON-RPC 2.0)
- Hash-chained receipt ledger (SHA-256, JSONL)
- 5-gate governance pipeline (budget, schema, constraint, authority, convergence)
- Failure-based constraint learning with TTL
- Convergence/loop detection
- 3-layer mutation classification (schema, verb heuristic, argument inspection)
- 5 governance meta-tools injected into tools/list
- Post-session analysis CLI (--view, --verify, --explain, --receipts)
- Webhook notifications (generic, Discord, Telegram)
- .mcp.json wrap/unwrap integration
- Zero external dependencies (Go standard library only)
- Single static binary (~6MB)
