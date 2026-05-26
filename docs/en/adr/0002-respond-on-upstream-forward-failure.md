# ADR 0002 — Reply with a JSON-RPC error when a client request can't be forwarded upstream

- **Status:** Accepted
- **Date:** 2026-05-26
- **Driver:** shell-agent-v2 hangs for 15s when a login-required MCP (e.g. Slack) has an expired OAuth token
- **Generalises to:** any upstream send/connect/auth failure while a client request is in flight

---

## Context

When the proxy forwards a client **request** (which expects a response)
to the upstream and that forward fails, the client is left waiting with
no reply until its own timeout fires.

Reproduction (Slack MCP, expired `authorization_code` token, no refresh
token):

```
# diagnostic mode surfaces the error and exits:
$ mcp-guardian --inspect --profile slack
mcp-guardian: OAuth2 authorization_code enabled (stored tokens)
error: send initialize: obtain auth token: access token expired and no refresh token available (run --login again)

# proxy mode swallows it and hangs:
$ mcp-guardian --profile slack
mcp-guardian: OAuth2 authorization_code enabled (stored tokens)
mcp-guardian: proxy started (controller=…, transport=sse)
# … client sends initialize, no response ever comes back
```

The downstream client (shell-agent-v2) blocks on its stdout read until
its 15s guardian-start timeout, then reports an opaque
`guardian start timed out after 15s` — the user never learns they need
to re-login.

### Root cause

`forwardRequest` (`internal/proxy/proxy.go`) sends to the upstream and
waits on a per-request channel. It already converts an **upstream
timeout** into a JSON-RPC error *response*:

```go
case <-timer.C:
    …
    return jsonrpc.NewErrorResponse(msg.ID, -32603, "upstream timeout"), nil
```

but an **upstream send failure** returns a bare error with no response:

```go
if err := p.upstream.Send(raw); err != nil {
    …
    return nil, fmt.Errorf("send to upstream: %w", err)   // ← no client reply
}
```

For the Slack case the SSE transport's `Send` triggers `obtain auth
token` (`internal/transport/sse.go:130` → `authcode.go:110`), which
returns `access token expired and no refresh token available (run
--login again)`. That error propagates up through `handleInitialize` →
`routeAgentMessage` to `readAgent`, which only logs it:

```go
if err := p.routeAgentMessage(msg, line); err != nil {
    logStderr("mcp-guardian: route error: %v\n", err)   // ← logged, client not answered
}
```

So the failure is asymmetric: **upstream timeout → client gets an error
response; upstream send/auth failure → client hangs.** The proxy already
replies with `jsonrpc.NewErrorResponse` for every other client-visible
failure (tool-not-found, governance block, internal errors); only the
request-forward failure path is missing it.

## Decision

Establish the invariant: **a client request whose routing returns an
error always receives a JSON-RPC error response.** Implement it centrally
in `readAgent`: when `routeAgentMessage` returns an error and the message
was a request (has an id), reply with a JSON-RPC error carrying the
underlying message; notifications (no id) are logged only, as today.

```go
if err := p.routeAgentMessage(msg, line); err != nil {
    logStderr("mcp-guardian: route error: %v\n", err)
    if msg.IsRequest() {
        _ = writeMessage(jsonrpc.NewErrorResponse(msg.ID, -32603, err.Error()))
    }
}
```

`-32603` (Internal error) matches the code the timeout path already uses.
`err.Error()` already carries the actionable text
(`…access token expired … (run --login again)`), so the client surfaces
the real reason.

### Why central (readAgent) rather than per-handler

All request paths funnel through `readAgent`, and several reach the
upstream via routes other than `forwardRequest` (the `tools/call`
unparseable-params fallthrough and the unknown-message default both call
`p.upstream.Send(raw)` directly). A single catch in `readAgent` covers
every present and future request-forward error in one place.

No double-response risk: audit of the request handlers shows none writes
a response and *then* returns an error — `forwardRequest` returns either
`(resp, nil)` (caller writes it) or `(nil, err)` (nothing written); the
masked/governance/meta paths `writeMessage(...)` and return `nil`. The
`readAgent` catch only fires on errors that propagate out unanswered.

## Consequences

- shell-agent-v2 (and any MCP client) gets an immediate JSON-RPC error on
  `initialize` instead of hanging; its `call()` already maps that to a
  fast failure with the message, which surfaces in the MCP settings as
  `…access token expired … (run --login again)`. The 15s wait and the
  opaque "timed out" message both disappear.
- A request that fails to forward for any reason (auth, transport,
  connect) now always gets a reply — a robustness improvement beyond the
  Slack/auth trigger.
- Behaviour for notifications, successful requests, and the existing
  upstream-timeout path is unchanged.

## Out of scope

- Auto-refresh / re-login of expired tokens (separate flow; the message
  already tells the user to run `--login`).
- Surfacing auth state proactively at proxy startup (the proxy starts
  before the first request; failing the first request fast is sufficient).
- shell-agent-v2-side changes — it already surfaces JSON-RPC errors
  quickly; no change needed for this fix. (Parallel guardian spawn / a
  shorter start-timeout backstop remain possible separate hardening for
  *other* hang causes.)

## Implementation

- `internal/proxy/proxy.go` — `readAgent`: reply with
  `jsonrpc.NewErrorResponse(msg.ID, -32603, err.Error())` when
  `routeAgentMessage` errors on a request.
- `internal/proxy/proxy_test.go` — test: a request whose upstream `Send`
  fails (inject a transport stub returning a send error) yields a
  JSON-RPC error response to the client (matching id, code -32603,
  message carrying the underlying error) rather than no output; a
  notification that fails produces no response.
- `README.md` / `README.ja.md` — note in the troubleshooting/behaviour
  section that an upstream/auth failure on a request returns a JSON-RPC
  error to the client (e.g. expired token → "run --login again").

Verification: `make test`; manual — point shell-agent-v2 at a profile
with an expired token, confirm the MCP settings show the re-login message
within ~1s instead of a 15s hang.
