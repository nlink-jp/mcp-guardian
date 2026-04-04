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

MCP governance proxy. Sits between an MCP client (Claude, GPT, etc.) and an MCP server via stdio, providing tamper-evident auditing, constraint learning, and loop detection.

```
mcp-guardian/
├── main.go                    # CLI entry point
├── internal/
│   ├── jsonrpc/               # JSON-RPC 2.0 types and parsing
│   ├── config/                # Configuration struct
│   ├── proxy/                 # stdio MITM proxy core
│   ├── governance/            # 5-gate governance pipeline (pure functions)
│   ├── classify/              # Mutation classification, signature extraction
│   ├── metatool/              # 5 governance meta-tools
│   ├── receipt/               # SHA-256 hash-chained receipt ledger
│   ├── state/                 # .governance/ state file management
│   ├── webhook/               # Fire-and-forget HTTP notifications
│   └── cli/                   # CLI analysis commands (view, verify, explain, wrap)
├── Makefile
└── go.mod                     # github.com/nlink-jp/mcp-guardian (no require)
```

## Key design decisions

- **json.RawMessage** for pass-through fields — avoids unnecessary deserialization
- **Governance gates are pure functions** — take state, return result, no side effects
- **sync.Mutex + pending map** for request-response matching
- **bufio.Scanner (1MB buffer)** for newline-delimited JSON-RPC
- **Atomic file writes** via tmp+rename pattern
- **crypto/rand UUID v4** — no external UUID library

## Build

```sh
make build      # → dist/mcp-guardian
make test       # go test ./...
make check      # lint + test
```
