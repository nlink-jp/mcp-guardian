# CLAUDE.md — mcp-guardian

**Organization rules (mandatory): https://github.com/nlink-jp/.github/blob/main/CONVENTIONS.md**

## Non-negotiable rules

- **Tests are mandatory** — write them with the implementation.
- **Design for testability** — pure functions, injected dependencies, no untestable globals.
- **Never `go build` directly** — always use `make build` (outputs to `dist/`).
- **Docs in sync** — update `README.md` and `README.ja.md` in the same commit as behaviour changes.
- **Small, typed commits** — `feat:`, `fix:`, `test:`, `chore:`, `docs:`, `refactor:`, `security:`
- **Zero external dependencies** — only Go standard library. This is the core security property.

## This project

MCP governance proxy. Sits between an MCP client (Claude, GPT, etc.) and an MCP server, providing tamper-evident auditing, constraint learning, and loop detection. Supports stdio and HTTP/SSE upstream transports.

```
mcp-guardian/
├── main.go                    # CLI entry point
├── docs/
│   └── architecture.md        # Detailed architecture reference
├── internal/
│   ├── transport/             # Transport abstraction layer
│   │   ├── transport.go       #   Transport interface
│   │   ├── process.go         #   stdio process transport
│   │   ├── sse.go             #   HTTP/SSE client transport
│   │   ├── auth.go            #   OAuth2 client_credentials + command token providers
│   │   └── authcode.go        #   OAuth2 authorization_code: stored tokens + refresh
│   ├── export/                # Telemetry exporter interface
│   │   └── export.go          #   Exporter interface (Export + Shutdown)
│   ├── proxy/                 # Core proxy loop and message routing
│   │   ├── proxy.go           #   Message routing, exporter dispatch
│   │   └── splunkhec.go       #   Splunk HEC exporter driver
│   ├── jsonrpc/               # JSON-RPC 2.0 types and parsing
│   ├── config/                # Configuration (struct, files, profiles, validation)
│   ├── governance/            # 5-gate governance pipeline (pure functions)
│   ├── classify/              # Mutation classification, signature extraction
│   ├── metatool/              # 5 governance meta-tools
│   ├── receipt/               # SHA-256 hash-chained receipt ledger
│   ├── state/                 # .governance/ state file management
│   ├── webhook/               # Fire-and-forget HTTP notifications
│   ├── otlp/                  # OTLP/HTTP exporter (Logs + Traces)
│   ├── mask/                  # Tool name glob matching
│   └── cli/                   # CLI commands (view, verify, explain, wrap, login, discover)
├── Makefile
└── go.mod                     # github.com/nlink-jp/mcp-guardian (no require)
```

## Key design decisions

- **Transport interface** — `Send`, `ReadLine`, `Close` — decouples proxy from stdio/SSE
- **json.RawMessage** for pass-through fields — avoids unnecessary deserialization
- **Governance gates are pure functions** — take state, return result, no side effects
- **sync.Mutex + pending map** for request-response matching
- **bufio.Scanner (1MB buffer)** for newline-delimited JSON-RPC
- **Atomic file writes** via tmp+rename pattern
- **crypto/rand UUID v4** — no external UUID library
- **OAuth2 client_credentials tokens in memory only** — never written to disk
- **OAuth2 authorization_code tokens on disk** — `stateDir/tokens.json` (mode 0600), auto-refresh via refresh_token
- **401 retry with single attempt** — invalidate token, retry once, fail on second 401
- **Pluggable Exporter interface** — OTLP and Splunk HEC run in parallel
- **Receipt tail read** — startup reads last line only, O(1) regardless of file size
- **Receipt auto-purge** — `maxReceiptAgeDays` (default 7), telemetry backends are durable store

## Build

```sh
make build      # → dist/mcp-guardian
make test       # go test ./...
make check      # lint + test
```
