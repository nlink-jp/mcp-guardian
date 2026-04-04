# AGENTS.md — mcp-guardian

## Summary

MCP governance proxy that interposes between an MCP client and server via stdio. Provides tamper-evident auditing (SHA-256 hash-chained receipts), failure-based constraint learning, loop detection, schema validation, and authority tracking. Built as a single binary with zero external dependencies.

## Build & Test

```sh
make build       # Build binary → dist/mcp-guardian
make test        # Run all tests
make check       # go vet + go test
make clean       # Remove dist/
make install     # Install to /usr/local/bin (or PREFIX=...)
```

**Never run `go build` directly** — always use `make build`.

## Module Path

```
github.com/nlink-jp/mcp-guardian
```

No external dependencies — `go.mod` has no `require` block.

## Key Directory Structure

```
main.go                          # CLI entry point, flag parsing
internal/
  jsonrpc/jsonrpc.go             # JSON-RPC 2.0 message types
  config/config.go               # ProxyConfig struct with defaults
  proxy/
    proxy.go                     # Core proxy loop: message routing, governance pipeline
    upstream.go                  # Child process spawn and line-buffered I/O
  governance/
    gates.go                     # 5-gate orchestration (RunGates)
    budget.go                    # Call count limit
    schema.go                    # JSON Schema validation
    constraint.go                # Failure-based blocking (tool+target+TTL)
    authority.go                 # Epoch-based session validity
    convergence.go               # Loop detection (failure repeat, tool+target repeat)
  classify/
    mutation.go                  # 3-layer mutation classification
    target.go                    # Target extraction from tool arguments
    signature.go                 # Error signature extraction
    normalize.go                 # Volatile component stripping
  metatool/metatool.go           # 5 governance meta-tools
  receipt/
    types.go                     # ToolCallRecord struct
    hash.go                      # StableStringify + SHA-256
    receipt.go                   # JSONL ledger: append, load, verify
  state/                         # .governance/ file I/O (atomic writes)
  webhook/webhook.go             # Fire-and-forget HTTP notifications
  cli/                           # Analysis commands: view, verify, explain, wrap
```

## Gotchas

- **StableStringify** must sort map keys deterministically for hash chain integrity
- **json.RawMessage** is used for ID fields because JSON-RPC IDs can be string or number
- **Upstream command** with spaces and no args is run via `sh -c` (shell interpretation)
- **1MB scanner buffer** — tool responses larger than 1MB will be truncated
- **syscall.TCGETS** is used for TTY detection (Linux-specific; needs adjustment for other platforms)

## Environment Variables

None required. All configuration via CLI flags.
