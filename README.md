# mcp-guardian

A governance proxy for MCP (Model Context Protocol) servers, built as a single binary with zero external dependencies.

Inspired by [@sovereign-labs/mcp-proxy](https://github.com/Born14/mcp-proxy), reimplemented in Go for supply chain security and operational robustness.

## Why

MCP tool servers give AI agents powerful capabilities. Without oversight, agents can repeat failed operations, exhaust resources, or make unauthorized mutations. `mcp-guardian` sits transparently between the MCP client and server, providing:

- **Tamper-evident audit trail** -- Every tool call produces a SHA-256 hash-chained receipt
- **Failure-based constraint learning** -- Automatically blocks retries of the same failed operation
- **Budget and convergence controls** -- Prevents runaway loops and excessive API calls
- **Schema validation** -- Validates tool arguments before forwarding
- **Authority tracking** -- Epoch-based session validity
- **Tool masking** -- Forcibly hide tools from agents (wildcard patterns supported)
- **OpenTelemetry export** -- OTLP/HTTP Logs + Traces for enterprise telemetry collection

## Features

- Single static binary (~6MB), no runtime dependencies
- Go standard library only -- zero external modules
- stdio MITM proxy (transparent to both client and server)
- Hash-chained receipt ledger (JSONL, verifiable)
- 5-gate governance pipeline
- 5 injected meta-tools for agent self-governance
- Post-session analysis CLI (view, verify, explain)
- Webhook notifications (generic, Discord, Telegram)
- OTLP/HTTP export (Logs + Traces, batched, zero-dependency)
- Tool masking with glob patterns (`--mask`, `--server-config`)
- Two-tier configuration (global system config + per-server config)
- `.mcp.json` wrap/unwrap for easy integration

## Install

```bash
# From source
git clone https://github.com/nlink-jp/mcp-guardian.git
cd mcp-guardian
make install

# Or specify prefix
make install PREFIX=$HOME/.local
```

## Quick Start

### Proxy mode

```bash
mcp-guardian -- npx -y @modelcontextprotocol/server-filesystem /tmp

# With options
mcp-guardian --enforcement advisory -- npx -y @modelcontextprotocol/server-filesystem /tmp
```

### Wrap an existing MCP server

```bash
# Wrap a server defined in .mcp.json
mcp-guardian --wrap filesystem

# Restore original
mcp-guardian --unwrap filesystem
```

### Post-session analysis

```bash
# View receipt timeline
mcp-guardian --view
mcp-guardian --view --tool write_file --outcome error

# Verify hash chain integrity
mcp-guardian --verify

# Session summary
mcp-guardian --explain
mcp-guardian --receipts
```

## CLI Reference

```
# Proxy mode
mcp-guardian [options] -- command [args...]

# Core options
--enforcement strict|advisory   Enforcement mode (default: strict)
--schema off|warn|strict        Schema validation (default: warn)
--max-calls N                   Budget cap (0 = unlimited)
--timeout ms                    Upstream timeout (default: 300000)
--state-dir dir                 State directory (default: .governance)

# Configuration files
--config <path>                 Global config file (OTLP, webhooks, defaults)
--server-config <path>          Per-server config file (mask, enforcement, etc.)

# Tool masking
--mask <pattern>                Mask tool by glob pattern (repeatable)
--mask-file <path>              Mask patterns file (one per line)

# OTLP telemetry export
--otlp-endpoint <url>           OTLP/HTTP endpoint (empty = disabled)
--otlp-header KEY=VALUE         OTLP HTTP header (repeatable)
--otlp-batch-size N             Batch size (default: 10)
--otlp-batch-timeout ms         Batch timeout (default: 5000)

# Webhooks
--webhook url                   Webhook URL (repeatable)

# Analysis
--view                          Receipt timeline
--verify                        Hash chain verification
--explain                       Session narrative
--receipts                      Compact summary

# Integration
--wrap <server>                 Interpose proxy in .mcp.json
--unwrap <server>               Restore original .mcp.json
--mcp-config <path>             Path to .mcp.json (for wrap/unwrap)

# Info
--version                       Show version
```

## Governance Pipeline

Every `tools/call` passes through 5 gates:

1. **Budget** -- Rejects if call count exceeds `--max-calls`
2. **Schema** -- Validates arguments against cached `inputSchema`
3. **Constraint** -- Blocks if tool+target matches a prior failure (TTL: 1 hour)
4. **Authority** -- Verifies session epoch matches authority epoch
5. **Convergence** -- Detects loops (3+ same failure, 5+ same tool+target in 2 min)

In `strict` mode, any gate failure blocks the call. In `advisory` mode, violations are logged but forwarded.

## Meta-Tools

The proxy injects 5 governance tools that agents can call:

| Tool | Description |
|------|-------------|
| `governance_status` | Inspect controller ID, epoch, constraints, receipt depth |
| `governance_bump_authority` | Advance epoch (invalidates current session) |
| `governance_declare_intent` | Declare goal + predicates for attribution |
| `governance_clear_intent` | Clear declared intent |
| `governance_convergence_status` | Inspect loop detection state |

## Tool Masking

Hide tools from agents entirely. Masked tools are removed from `tools/list` responses and calls return a generic "tool not found" error, preventing agents from knowing the tool exists or attempting to circumvent restrictions.

```bash
# Via CLI flags
mcp-guardian --mask "write_*" --mask "delete_*" -- npx server

# Via patterns file
mcp-guardian --mask-file masks.txt -- npx server

# Via server config file
mcp-guardian --server-config server.json -- npx server
```

Patterns use glob syntax (`*` matches any characters, `?` matches one character). In `advisory` mode, masked tools are logged but not hidden.

## Configuration Files

Two-tier configuration separates global settings from per-server policies:

### Global config (`--config`)

Shared across all MCP server instances. Ideal for MDM/EMM deployment.

```json
{
  "otlp": {
    "endpoint": "http://otel-collector:4318",
    "headers": { "Authorization": "Bearer org-token" },
    "batchSize": 10,
    "batchTimeout": 5000
  },
  "webhooks": ["https://hooks.slack.com/..."],
  "defaults": {
    "enforcement": "strict",
    "schema": "warn"
  }
}
```

### Server config (`--server-config`)

Per-MCP-server policy. Overrides global defaults.

```json
{
  "enforcement": "advisory",
  "mask": ["write_*", "execute_*"],
  "maxCalls": 50,
  "schema": "strict"
}
```

### Priority order

```
Defaults → --config (global) → --server-config (per-server) → CLI flags
```

CLI flags always win. Sensitive values (e.g., OTLP auth tokens) in config files avoid exposure via `ps`.

## OTLP Telemetry Export

Export audit data to any OpenTelemetry-compatible backend (Datadog, Grafana, Splunk, etc.) via OTLP/HTTP with JSON encoding. Zero external dependencies -- implemented with Go standard library only.

```bash
mcp-guardian \
  --otlp-endpoint http://otel-collector:4318 \
  --otlp-header "Authorization=Bearer token" \
  -- npx server
```

- **Logs**: Each tool call receipt becomes a structured log record
- **Traces**: Each tool call becomes a span with duration, status, and attributes
- **Batched**: Records are buffered and flushed by size, timer, or on shutdown
- **Local receipts are authoritative**: OTLP export is secondary; failures log to stderr without blocking MCP traffic

## Architecture

```
Agent (Claude, GPT, etc.)
  | stdin/stdout (JSON-RPC 2.0)
mcp-guardian
  | stdin/stdout (JSON-RPC 2.0)
Upstream MCP Server
```

### State directory (.governance/)

| File | Contents |
|------|----------|
| `receipts.jsonl` | Append-only hash-chained audit trail |
| `constraints.json` | Learned failure fingerprints with TTL |
| `controller.json` | Stable controller UUID |
| `authority.json` | Epoch + session binding + genesis hash |
| `intent.json` | Currently declared intent |

## Build

```bash
make build              # Build to dist/
make install            # Install to /usr/local/bin
make test               # Run unit tests
make check              # Lint + test
make integration-test   # Run OTLP integration tests (requires podman/docker)
make otel-up            # Start OTel Collector for manual testing
make otel-down          # Stop OTel Collector
make clean              # Clean build artifacts
make help               # Show all targets
```

## License

MIT License. Copyright (c) 2026 magifd2

## Acknowledgments

This project owes its core design to [@sovereign-labs/mcp-proxy](https://github.com/Born14/mcp-proxy) by [Born14](https://github.com/Born14).

The original Node.js/TypeScript implementation pioneered the idea of a **transparent governance proxy for MCP servers** -- inserting an auditing layer between AI agents and tool servers without either side knowing. The key concepts we adopted from that work include:

- **Hash-chained receipt ledger** -- treating every tool call as an immutable, tamper-evident record (like git commits for agent actions)
- **Failure-based constraint learning** -- fingerprinting failed calls and automatically blocking identical retries within a TTL window
- **Authority tracking with epochs** -- a monotonic counter proving which controller was active during each call
- **Pure-function governance gates** -- separating governance math from I/O so invariants can be verified in isolation

`mcp-guardian` is a ground-up reimplementation in Go, not a fork or a port, but the architectural blueprint and the insight that MCP tool calls need governance -- not just logging -- came directly from Born14's work. We chose Go and zero external dependencies to address supply chain security concerns in security-sensitive environments, but the "what to build" was already answered by `@sovereign-labs/mcp-proxy`.

If you find `mcp-guardian` useful, please also star the [original project](https://github.com/Born14/mcp-proxy) that made it possible.
