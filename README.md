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
- **Dual transport**: stdio (default) and HTTP/SSE (Streamable HTTP)
- **MCP Authorization Discovery**: auto-discovers OAuth2 endpoints and registers clients dynamically -- no manual OAuth app setup required
- **OAuth2 authentication**: client_credentials and authorization_code (browser login) with automatic token refresh
- **Browser login**: `--login` auto-discovers OAuth2, registers a client, opens browser, stores tokens
- **External token command**: integrate with `gcloud`, `vault`, or any CLI tool
- **401 auto-retry**: transparent token refresh on authentication failure
- Hash-chained receipt ledger (JSONL, verifiable)
- **Receipt auto-purge**: configurable retention period, OTLP/Splunk for long-term storage
- 5-gate governance pipeline
- 5 injected meta-tools for agent self-governance
- Post-session analysis CLI (view, verify, explain)
- Webhook notifications (generic, Discord, Telegram)
- **Pluggable telemetry**: OTLP/HTTP and Splunk HEC drivers (run in parallel)
- Tool masking with glob patterns (`--mask`, `--profile`)
- Two-tier configuration (system global config + server profiles)
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

### Using server profiles (recommended)

Create a profile at `~/.config/mcp-guardian/profiles/filesystem.json`:

```json
{
  "name": "filesystem",
  "upstream": {
    "command": "npx",
    "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
  },
  "governance": { "enforcement": "advisory" }
}
```

```bash
mcp-guardian --profile filesystem
```

SSE server with OAuth2 (auto-discovery):

```json
{
  "name": "atlassian",
  "upstream": {
    "transport": "sse",
    "url": "https://mcp.atlassian.com/v1/mcp"
  }
}
```

No OAuth2 configuration needed -- `--login` auto-discovers endpoints and registers a client:

```bash
# First time: discovers OAuth2, registers client, opens browser
mcp-guardian --login atlassian

# Subsequent runs: tokens auto-refresh
mcp-guardian --profile atlassian

# Add to Claude Code
claude mcp add atlassian -- mcp-guardian --profile atlassian
```

### Inline mode (no profile)

```bash
mcp-guardian -- npx -y @modelcontextprotocol/server-filesystem /tmp

# With options
mcp-guardian --enforcement advisory -- npx -y @modelcontextprotocol/server-filesystem /tmp

# SSE transport
mcp-guardian --transport sse --upstream-url http://localhost:8080/mcp
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
# Proxy mode (profile)
mcp-guardian --profile <name|path>

# Proxy mode (inline stdio)
mcp-guardian [options] -- command [args...]

# Proxy mode (inline SSE)
mcp-guardian --transport sse --upstream-url <url> [options]

# Core options
--enforcement strict|advisory   Enforcement mode (default: strict)
--schema off|warn|strict        Schema validation (default: warn)
--max-calls N                   Budget cap (0 = unlimited)
--timeout ms                    Upstream timeout (default: 300000)
--state-dir dir                 State directory (default: .governance)

# Transport
--transport stdio|sse           Upstream transport (default: stdio)
--upstream-url <url>            MCP server URL (required for sse)
--sse-header KEY=VALUE          SSE HTTP header (repeatable)

# Authentication (sse transport)
--oauth2-token-url <url>        OAuth2 token endpoint
--oauth2-client-id <id>         OAuth2 client ID
--oauth2-client-secret <secret> OAuth2 client secret
--oauth2-scope <scope>          OAuth2 scope (repeatable)
--token-command <cmd>           External token command
--token-command-arg <arg>       Token command argument (repeatable)

# Server profiles
--profile <name|path>           Server profile (name from ~/.config/mcp-guardian/profiles/ or path)
--profiles                      List available server profiles
--login <name|path>             Browser login for OAuth2 authorization_code flow

# Configuration files
--config <path>                 Global config file (telemetry, defaults)

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

# Via profile
mcp-guardian --profile my-server
```

Patterns use glob syntax (`*` matches any characters, `?` matches one character). In `advisory` mode, masked tools are logged but not hidden.

## Configuration

Two-tier configuration separates system-wide telemetry from per-server policies:

```
~/.config/mcp-guardian/
  config.json              # System global (telemetry + org defaults)
  profiles/
    github-mcp.json        # Server profile
    filesystem.json
```

See the [examples/](examples/) directory for ready-to-use templates.

### System global config

Auto-discovered from `~/.config/mcp-guardian/config.json`, or specified with `--config`.

Shared across all MCP server instances. Ideal for MDM/EMM deployment.

```json
{
  "telemetry": {
    "otlp": {
      "endpoint": "http://otel-collector:4318",
      "headers": { "Authorization": "Bearer org-token" },
      "batchSize": 10,
      "batchTimeout": 5000
    },
    "webhooks": ["https://hooks.slack.com/..."]
  },
  "defaults": {
    "enforcement": "strict",
    "schema": "warn"
  }
}
```

The legacy format (top-level `otlp`/`webhooks`) is still supported for backward compatibility.

### Server profiles (`--profile`)

Per-MCP-server configuration. Stored in `~/.config/mcp-guardian/profiles/` or referenced by path.

SSE server with auto-discovery (minimal):

```json
{
  "name": "atlassian",
  "upstream": { "transport": "sse", "url": "https://mcp.atlassian.com/v1/mcp" }
}
```

stdio server:

```json
{
  "name": "my-server",
  "upstream": {
    "transport": "stdio",
    "command": "npx",
    "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
  },
  "governance": {
    "enforcement": "advisory",
    "schema": "strict",
    "maxCalls": 50
  },
  "mask": ["write_*", "execute_*"]
}
```

SSE with OAuth2 client_credentials (M2M):

```json
{
  "name": "api-server",
  "upstream": { "transport": "sse", "url": "http://mcp.example.com/mcp" },
  "auth": {
    "oauth2": {
      "tokenUrl": "https://auth.example.com/oauth2/token",
      "clientId": "my-client",
      "clientSecret": "my-secret",
      "scopes": ["mcp:read", "mcp:write"]
    }
  },
  "governance": { "enforcement": "strict" }
}
```

SSE with OAuth2 authorization_code (explicit config -- usually not needed, `--login` auto-discovers):

```json
{
  "name": "github-mcp",
  "upstream": { "transport": "sse", "url": "https://mcp.github.com/sse" },
  "auth": {
    "oauth2": {
      "flow": "authorization_code",
      "authorizeUrl": "https://github.com/login/oauth/authorize",
      "tokenUrl": "https://github.com/login/oauth/access_token",
      "clientId": "my-app",
      "scopes": ["repo"]
    }
  }
}
```

External token command:

```json
{
  "name": "gcp-server",
  "upstream": {
    "transport": "sse",
    "url": "http://mcp.example.com/mcp"
  },
  "auth": {
    "tokenCommand": {
      "command": "gcloud",
      "args": ["auth", "print-access-token"]
    }
  }
}
```

### Priority order

```
Defaults → Global config (auto-discovered or --config) → Profile (--profile) → CLI flags
```

CLI flags always win. Sensitive values (e.g., OAuth2 secrets) in profiles avoid exposure via `ps`.

## MCP Client Integration (.mcp.json)

From the MCP client's perspective, mcp-guardian is always a stdio process -- regardless of whether the upstream MCP server uses stdio or SSE. Profiles encapsulate all transport and auth complexity.

```json
{
  "mcpServers": {
    "filesystem": {
      "command": "mcp-guardian",
      "args": ["--profile", "filesystem"]
    },
    "github": {
      "command": "mcp-guardian",
      "args": ["--profile", "github-mcp"]
    }
  }
}
```

For SSE servers requiring authentication, run `mcp-guardian --login <profile>` once. OAuth2 endpoints and client registration are handled automatically.

## Telemetry Export

Pluggable telemetry with two built-in drivers that can run in parallel. Zero external dependencies.

| Driver | Use case | Config key |
|--------|----------|------------|
| **OTLP/HTTP** | CloudWatch, GCP, Grafana Cloud, Datadog, etc. | `telemetry.otlp` |
| **Splunk HEC** | Splunk Enterprise / Cloud (direct, no collector) | `telemetry.splunk` |

Both drivers can run simultaneously. Local receipts auto-purge after `maxReceiptAgeDays` (default: 7) -- telemetry backends are the durable store.

For setup guides (AWS, GCP, Grafana Cloud, Datadog, Splunk, self-hosted), see [docs/otlp-setup.md](docs/otlp-setup.md).

## Architecture

```
Agent (Claude, GPT, etc.)
  | stdin/stdout (JSON-RPC 2.0)
mcp-guardian
  | Transport interface
  +-- stdio: stdin/stdout pipe to child process (default)
  +-- sse:   HTTP POST + SSE stream to remote server
Upstream MCP Server
```

For detailed architecture documentation, see [docs/architecture.md](docs/architecture.md).

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
