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

## Features

- Single static binary (~6MB), no runtime dependencies
- Go standard library only -- zero external modules
- stdio MITM proxy (transparent to both client and server)
- Hash-chained receipt ledger (JSONL, verifiable)
- 5-gate governance pipeline
- 5 injected meta-tools for agent self-governance
- Post-session analysis CLI (view, verify, explain)
- Webhook notifications (generic, Discord, Telegram)
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

# Options
--enforcement strict|advisory   Enforcement mode (default: strict)
--schema off|warn|strict        Schema validation (default: warn)
--max-calls N                   Budget cap (0 = unlimited)
--timeout ms                    Upstream timeout (default: 300000)
--webhook url                   Webhook URL (repeatable)
--state-dir dir                 State directory (default: .governance)

# Analysis
--view                          Receipt timeline
--verify                        Hash chain verification
--explain                       Session narrative
--receipts                      Compact summary

# Integration
--wrap <server>                 Interpose proxy in .mcp.json
--unwrap <server>               Restore original .mcp.json
--config <path>                 Path to .mcp.json

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
make build       # Build to dist/
make install     # Install to /usr/local/bin
make test        # Run tests
make check       # Lint + test
make clean       # Clean build artifacts
make help        # Show all targets
```

## License

MIT License. Copyright (c) 2026 magifd2

## Acknowledgments

This project is inspired by and pays respect to [@sovereign-labs/mcp-proxy](https://github.com/Born14/mcp-proxy) by Born14. The original Node.js implementation established the governance proxy concept for MCP servers. This Go reimplementation aims to provide the same capabilities with enhanced supply chain security through a zero-dependency single binary.
