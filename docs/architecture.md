# mcp-guardian Architecture

This document describes the internal architecture of mcp-guardian for contributors and security reviewers.

## System Overview

mcp-guardian is a transparent MITM governance proxy for MCP (Model Context Protocol) servers. It intercepts every JSON-RPC 2.0 message between an AI client and an MCP server, applies governance checks, and records tamper-evident audit receipts.

```
                       mcp-guardian
                  +--------------------+
                  |                    |
Agent (Claude) -->| Agent    Transport |<-- Upstream MCP Server
   stdin/stdout   | Side     Layer     |    (stdio or HTTP/SSE)
                  |          |         |
                  |    +-----+-----+   |
                  |    | JSON-RPC  |   |
                  |    | Router    |   |
                  |    +-----+-----+   |
                  |          |         |
                  |    +-----+-----+   |
                  |    | Governance |   |
                  |    | Pipeline   |   |
                  |    +-----+-----+   |
                  |          |         |
                  |    +-----+-----+   |
                  |    |  Receipt  |   |
                  |    |  Ledger   |   |
                  |    +-----------+   |
                  +--------------------+
                       |         |
                  Webhooks   OTLP Export
```

## Package Structure

```
internal/
+-- transport/        # Transport abstraction layer
|   +-- transport.go  #   Transport interface
|   +-- process.go    #   stdio process transport
|   +-- sse.go        #   HTTP/SSE client transport
|   +-- auth.go       #   OAuth2 client_credentials + command token providers
|   +-- authcode.go   #   OAuth2 authorization_code: stored tokens + refresh
|
+-- export/           # Telemetry exporter interface
|   +-- export.go     #   Exporter interface (Export + Shutdown)
|
+-- proxy/            # Core proxy loop
|   +-- proxy.go      #   Message routing, request-response matching
|   +-- splunkhec.go  #   Splunk HEC exporter driver
|
+-- jsonrpc/          # JSON-RPC 2.0 types and parsing
|   +-- jsonrpc.go    #   Message, Parse, Marshal, ToolCallParams
|
+-- governance/       # 5-gate governance pipeline (pure functions)
|   +-- gates.go      #   RunGates() entry point
|   +-- budget.go     #   Gate 1: call count budget
|   +-- schema.go     #   Gate 2: JSON schema validation
|   +-- constraint.go #   Gate 3: learned failure constraints
|   +-- authority.go  #   Gate 4: epoch authority check
|   +-- convergence.go#   Gate 5: loop/exhaustion detection
|
+-- classify/         # Mutation classification
|   +-- mutation.go   #   Read-only vs mutating classification
|   +-- target.go     #   Target extraction from arguments
|   +-- signature.go  #   Error fingerprinting
|
+-- metatool/         # 5 governance meta-tools
|   +-- metatool.go   #   Injected tools for agent self-governance
|
+-- receipt/          # SHA-256 hash-chained audit ledger
|   +-- receipt.go    #   Ledger: append-only log
|   +-- hash.go       #   Hash chaining
|   +-- types.go      #   Record struct
|
+-- state/            # .governance/ state files
|   +-- state.go      #   Directory initialization
|   +-- authority.go  #   Epoch management
|   +-- constraints.go#   Constraint CRUD with TTL
|   +-- controller.go #   UUID + session tracking
|   +-- intent.go     #   Declared agent goals
|   +-- atomic.go     #   Atomic file writes (tmp+rename)
|
+-- config/           # Configuration
|   +-- config.go     #   Config struct, validation
|   +-- file.go       #   GlobalConfig, ServerConfig, JSON loading
|
+-- webhook/          # Fire-and-forget HTTP notifications
+-- otlp/             # OTLP/HTTP exporter (Logs + Traces)
+-- mask/             # Tool name glob matching
+-- cli/              # Post-session CLI commands
    +-- login.go      #   --login: OAuth2 authorization_code flow
    +-- discover.go   #   MCP Authorization Discovery + Dynamic Client Registration
```

## Transport Layer

The transport layer abstracts the communication channel between mcp-guardian and the upstream MCP server.

### Transport Interface

```go
type Transport interface {
    Send(data []byte) error
    ReadLine() ([]byte, bool)
    Close() error
}
```

All transports implement this minimal interface. The proxy core (`proxy.go`) depends only on this interface, not on concrete transport types.

### stdio Transport (ProcessTransport)

The default transport. Spawns the MCP server as a child process and communicates via piped stdin/stdout.

```
mcp-guardian process
    |
    +-- exec.Command(upstream, args...)
    |       |
    |       +-- stdin pipe  --> Send()
    |       +-- stdout pipe --> ReadLine() via bufio.Scanner
    |       +-- stderr pipe --> forwarded to os.Stderr
    |
    +-- Process lifecycle managed by transport
```

- 1MB scanner buffer for large JSON-RPC messages
- Direct binary execution (no shell) prevents command injection
- Process stderr is forwarded to proxy stderr for logging

### SSE Transport (sseClientTransport)

Connects to MCP servers that use the Streamable HTTP transport (the HTTP-based alternative to stdio defined in the MCP spec).

```
mcp-guardian
    |
    +-- HTTP POST  --> Send() (JSON-RPC request in body)
    |
    +-- Response handling:
    |   +-- Content-Type: application/json --> single response
    |   +-- Content-Type: text/event-stream --> SSE stream
    |       +-- event: message --> JSON-RPC message(s)
    |       +-- Batch: JSON array --> split into individual messages
    |
    +-- Mcp-Session-Id header tracking
    +-- DELETE on Close() to terminate session
```

Key design decisions:

- **Asynchronous incoming channel**: SSE responses arrive asynchronously. A buffered channel (`chan []byte`, capacity 64) decouples SSE parsing from proxy message routing.
- **Mixed response modes**: The same endpoint may return JSON for some requests and SSE for others. Both are handled transparently.
- **Session affinity**: The `Mcp-Session-Id` header is tracked and sent with every request.

### Authentication

The SSE transport supports three authentication mechanisms via the `TokenProvider` interface, all providing automatic token refresh on 401.

```
+-- TokenProvider interface
    |
    +-- oauth2Provider (client_credentials grant)
    |   +-- POST to token_url with client_id + client_secret
    |   +-- Caches token until 30s before expiry
    |   +-- Invalidate() forces re-fetch
    |
    +-- storedTokenProvider (authorization_code flow)
    |   +-- Reads tokens from stateDir/tokens.json (written by --login)
    |   +-- Auto-refreshes using refresh_token when expired
    |   +-- Persists refreshed tokens back to disk
    |
    +-- commandProvider (external command)
        +-- exec.Command(token_command, args...)
        +-- stdout trimmed = Bearer token
        +-- Optional TTL-based caching
```

**MCP Authorization Discovery (`--login` without explicit OAuth2 config):**

When a profile has no `auth` block, `--login` auto-discovers everything:

```
mcp-guardian --login <profile>
  |
  +-- Start local callback server on random port
  |
  +-- POST to MCP server URL → 401
  |   +-- Try: resource_metadata from WWW-Authenticate header
  |   +-- Fallback: .well-known/oauth-authorization-server on MCP host
  |
  +-- Fetch Authorization Server Metadata (RFC 8414)
  |   → authorization_endpoint, token_endpoint, registration_endpoint
  |
  +-- Dynamic Client Registration (RFC 7591)
  |   POST registration_endpoint:
  |     client_name: "mcp-guardian"
  |     redirect_uris: ["http://127.0.0.1:PORT/callback"]
  |     grant_types: ["authorization_code", "refresh_token"]
  |     token_endpoint_auth_method: "none"
  |   → client_id (auto-generated)
  |
  +-- Save discovery to stateDir/oauth2-discovery.json
  |
  +-- Authorization Code Flow with PKCE:
  |   +-- Generate code_verifier + code_challenge (S256)
  |   +-- Open browser: authorization_endpoint?client_id=...&code_challenge=...
  |   +-- User authenticates in browser
  |   +-- Callback receives authorization code
  |   +-- Exchange code for tokens at token_endpoint
  |
  +-- Save tokens to stateDir/tokens.json (mode 0600)
```

At runtime, the proxy loads stored tokens and the discovery cache for
token refresh. No OAuth2 configuration in the profile is needed.

**Authorization Code Login Flow (explicit OAuth2 config):**

When `auth.oauth2` is explicitly configured in the profile, discovery
is skipped and the provided endpoints are used directly:

```
mcp-guardian --login <profile>
  |
  +-- Start local callback server
  +-- Generate PKCE code_verifier + code_challenge (S256)
  +-- Open browser: authorizeUrl?response_type=code&code_challenge=...
  +-- User authenticates → callback receives code
  +-- Exchange code for tokens at tokenUrl
  +-- Save tokens to stateDir/tokens.json (mode 0600)
```

**401 Retry Flow:**

```
Send(data)
  |
  +-- doPost(data) with current token
  |
  +-- 401? --yes--> Invalidate() token
  |                 doPost(data) with fresh token
  |                   |
  |                   +-- 401 again? --> error (auth failed)
  |                   +-- 2xx --> handleResponse()
  |
  +-- 2xx --> handleResponse()
```

## Message Flow

### Agent-side (downstream)

The proxy reads from os.Stdin (agent) and writes to os.Stdout. This is always stdio because MCP clients spawn the proxy as a subprocess.

```
os.Stdin --> bufio.Scanner --> jsonrpc.Parse() --> routeAgentMessage()
                                                       |
                                +----------------------+
                                |                      |
                          IsNotification()       IsRequest()
                                |                      |
                         forward as-is          handleRequest()
                                                       |
                                +--------+--------+----+
                                |        |        |
                           initialize  tools/   tools/  other
                                       list     call
                                         |        |
                                    cache      governance
                                    schemas    pipeline
                                    inject
                                    meta-tools
```

### Request-Response Matching

```go
pending map[string]chan *jsonrpc.Message  // id -> response channel
```

1. **Send**: Register `ch` in `pending[id]`, forward to upstream
2. **Receive**: `readUpstream()` goroutine matches response to pending channel
3. **Wait**: `forwardRequest()` blocks on `select { ch, timeout }`
4. **Cleanup**: Channel removed from pending on receive or timeout

This works identically for both stdio and SSE transports because the Transport interface abstracts the framing.

## Governance Pipeline

Every `tools/call` request passes through 5 gates in sequence. All gates are **pure functions** -- they take state and return a result with no side effects.

```go
func RunGates(input GateInput) GateResult
```

```
tools/call
    |
    v
+--------+     +--------+     +----------+     +---------+     +------------+
| Budget |---->| Schema |---->|Constraint|---->|Authority|---->|Convergence |
| Gate   |     | Gate   |     | Gate     |     | Gate    |     | Gate       |
+--------+     +--------+     +----------+     +---------+     +------------+
    |               |               |               |                |
  count >=        validate       match           epoch            3+ same
  maxCalls?       args vs        against         mismatch?       failure?
                  cached         learned                         5+ same
                  schema         constraints                     target in
                                 (TTL-based)                     2 min?
```

| Gate | Blocks when | Mode-dependent |
|------|-------------|----------------|
| Budget | `callCount >= maxCalls` | No (always enforced if set) |
| Schema | Arguments violate `inputSchema` | `strict` blocks, `warn` logs |
| Constraint | Tool+target matches a learned failure | `strict` blocks, `advisory` logs |
| Authority | Session epoch != authority epoch | `strict` blocks, `advisory` logs |
| Convergence | Loop or exhaustion detected | Returns signal, does not block directly |

## Receipt Ledger

Every `tools/call` (whether forwarded, blocked, or errored) produces a receipt appended to `receipts.jsonl`.

```
Record N:
  +-- Timestamp
  +-- ToolName, Arguments, Target
  +-- MutationType (read/create/update/delete)
  +-- Outcome (success/error/blocked)
  +-- DurationMs
  +-- ConstraintCheck, AuthorityCheck
  +-- Hash = SHA-256(Record N content + Record N-1 hash)
  +-- PreviousHash = Record N-1 hash

Receipt chain: R0 --> R1 --> R2 --> ... --> Rn
               ^hash   ^hash   ^hash
```

Any modification to a historical receipt breaks the chain, which `mcp-guardian --verify` detects.

### Auto-Purge

On startup, receipts older than `maxReceiptAgeDays` (default: 7) are removed. Remaining records are re-chained from "genesis". Long-term retention is handled by telemetry exporters (OTLP, Splunk HEC).

### Tail Read

`NewLedger()` reads only the last line of `receipts.jsonl` to recover `seq` and `lastHash`, avoiding a full file scan. This makes startup O(1) regardless of file size.

## Telemetry Exporters

The proxy dispatches receipts to pluggable telemetry backends via the `Exporter` interface:

```go
type Exporter interface {
    Export(r *receipt.Record)
    Shutdown()
}
```

```
proxy.go
  |
  +-- []export.Exporter (multiple drivers in parallel)
       |
       +-- otlp.Exporter         -> OTLP/HTTP (Logs + Traces)
       +-- splunkHECExporter     -> Splunk HEC (Events)
```

Both drivers batch records and flush on size threshold, timer, or shutdown. Export failures log to stderr without blocking MCP traffic.

## Configuration Priority

```
Defaults (hardcoded)
    |
    v
Global config (auto-discovered or --config) -- telemetry, webhooks, org defaults
    |
    v
Server profile (--profile) -- upstream, auth, governance, mask
    |
    v
CLI flags (highest priority) -- always win
```

### Two-Tier Model

```
~/.config/mcp-guardian/
  config.json              <- System global (Layer 1)
  profiles/
    github-mcp.json        <- Server profile (Layer 2)
    filesystem.json
```

| Layer | Scope | Contains |
|-------|-------|----------|
| System global | 1 per environment | OTLP, webhooks, org defaults |
| Server profile | 1 per MCP server | upstream, auth, governance, mask |

Global config is auto-discovered from `~/.config/mcp-guardian/config.json`.
Profiles are loaded by name from `~/.config/mcp-guardian/profiles/` or by path.

## Data Flow Diagram

Complete data flow for a `tools/call` request through SSE transport with OAuth2:

```
1. Agent writes to os.Stdin:
   {"jsonrpc":"2.0","id":"42","method":"tools/call","params":{"name":"write_file",...}}

2. proxy.readAgent() parses JSON-RPC

3. routeAgentMessage() -> handleRequest() -> handleToolsCall()

4. Governance pipeline: RunGates() -> all 5 gates pass

5. forwardRequest():
   a. Register pending["42"] = channel
   b. upstream.Send(raw)  [Transport interface]
      |
      SSE: doPost(data)
        +-- auth.Token() -> "Bearer eyJ..."  [OAuth2 client_credentials]
        +-- HTTP POST to upstream URL with Bearer token
        +-- Response: 200 + Content-Type: text/event-stream
        +-- consumeSSEStream() -> parse SSE -> incoming channel

6. readUpstream() goroutine:
   a. upstream.ReadLine()  [reads from incoming channel]
   b. jsonrpc.Parse() -> msg.IsResponse() -> match pending["42"]
   c. Send response to channel

7. forwardRequest() receives response from channel

8. recordReceipt() -> append to receipts.jsonl with hash chain
   +-- otlp.Export() if configured
   +-- webhook.Fire() if blocked

9. writeMessage() -> os.Stdout -> Agent receives response
```

## Security Properties

| Property | Mechanism |
|----------|-----------|
| No external dependencies | `go.mod` has zero `require` lines |
| No command injection | Direct `exec.Command`, no shell |
| Tamper-evident audit | SHA-256 hash chain, verifiable offline |
| Atomic state writes | tmp file + rename pattern |
| No credential exposure | Config files avoid `ps` visibility |
| OAuth2 token isolation | Tokens cached in memory only, never written to disk |
| Session termination | DELETE sent on close for SSE sessions |
| 401 retry limit | Maximum 1 retry to prevent infinite loops |

## Concurrency Model

```
Main goroutine:
  proxy.readAgent() -- blocking loop on os.Stdin

Background goroutines:
  proxy.readUpstream() -- blocking loop on Transport.ReadLine()
  io.Copy(stderr, process.Stderr) -- only for process transport
  consumeSSEStream() -- one per SSE response (short-lived)
  otlp.Exporter batch timer -- periodic flush

Synchronization:
  sync.Mutex -- protects pending map and SSE transport state
  Channels -- request-response matching (1-buffered per request)
  closed channel -- signals transport shutdown
```
