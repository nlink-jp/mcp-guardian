# ADR 0001 — Support pre-registered OAuth2 confidential clients

- **Status:** Accepted
- **Date:** 2026-05-21
- **Driver:** Slack official MCP server (`https://mcp.slack.com/mcp`)
- **Generalises to:** any MCP server whose authorization server does
  not implement RFC 7591 Dynamic Client Registration — Slack, GitHub
  Apps, Microsoft Entra ID, most enterprise SaaS providers.

---

## Context

mcp-guardian's `--login` flow (RFC 6749 authorization_code with PKCE)
currently assumes the upstream MCP server's authorization server
exposes a `registration_endpoint` per RFC 7591 (Dynamic Client
Registration, DCR). `internal/cli/discover.go:73` POSTs to it on every
login to obtain a fresh `client_id`.

The official Slack MCP endpoint explicitly does **not** support DCR.
Slack's documentation states:

> We do not support SSE-based connections or Dynamic Client
> Registration at this time. MCP clients must be backed by a
> registered Slack app with a fixed app ID and hardcode that app ID.

Reproduction:

```
$ mcp-guardian --login slack
Discovering OAuth2 configuration from MCP server...
error: OAuth2 discovery failed: client registration:
       authorization server does not support dynamic client registration
```

Slack is not unique. GitHub Apps, Microsoft Entra ID, Okta-managed
custom apps, and most other major OAuth providers require the same
"pre-register an app, hardcode client_id" model. A general solution
benefits every future provider that lands on this path.

## What already worked before this ADR

The building blocks for a pre-registered client were already present
(file:line):

| Capability | Reference |
|---|---|
| Skip discovery when `auth.oauth2` is set in the profile | `internal/cli/login.go:52-74` |
| Send `client_secret` in token exchange | `internal/cli/login.go:191-193` |
| Send `client_secret` in refresh request | `internal/transport/authcode.go:146-148` |
| PKCE always on (S256) | `internal/cli/login.go:88-89,149-150` |
| `scopes` + `extraParams` on authorize URL | `internal/cli/login.go:138-143` |
| Streamable HTTP transport (named `sse` internally) | `internal/transport/sse.go:14-23` |

So in principle, a profile that supplied an explicit `auth.oauth2`
block (with `authorizeUrl`, `tokenUrl`, `clientId`, `clientSecret`)
already bypassed discovery and exchanged the code with the secret.

## Decision

Add two new fields to `OAuth2Block` and one new CLI flag, plus a
shared client-authentication helper. The minimum surface that
unblocks every identified provider is:

### 1. Fixed callback port

`internal/cli/login.go:44` listens on `127.0.0.1:0`, so the
redirect_uri changes on every `--login` invocation. Pre-registered
OAuth apps require an **exact** `redirect_uri` match against the
allow-list configured in the provider's app settings.

RFC 8252 §7.3 recommends loopback redirects with flexible ports, and
some providers honour that. Slack does not — its app registration UI
demands a fully-qualified URI. GitHub and Microsoft Entra ID are the
same.

**Decision:** add `callbackPort` to `OAuth2Block`, plus a
`--callback-port` CLI flag that overrides the profile value. The CLI
flag wins so power users can register one port at the provider and
override it from the command line for one-off debugging without
editing the profile.

### 2. Token-endpoint client authentication method

Current code sends `client_id` + `client_secret` as form parameters
(`client_secret_post`). This is widely accepted, including by Slack.
Some providers — notably Microsoft Entra ID and certain Okta tenants
— require HTTP Basic authentication on the token endpoint
(`client_secret_basic`). Without an opt-in, mcp-guardian fails with a
401 against those servers.

**Decision:** add `clientAuthMethod` to `OAuth2Block` with values
`"post"` (default), `"basic"`, `"none"`. The implementation is a
single helper `applyClientAuth(req, form, cfg)` used from both the
initial token exchange (`login.go`) and the refresh
(`authcode.go`).

| `clientAuthMethod` | Behaviour |
|---|---|
| `post` (default, current behaviour) | `form.Set("client_id", id); if secret != "" { form.Set("client_secret", secret) }` |
| `basic` | `req.SetBasicAuth(id, secret)`; do NOT put client_id/secret in form. Per RFC 6749 §2.3.1, the values are URL-form-encoded before being percent-encoded for the Basic header. |
| `none` | `form.Set("client_id", id)` only; never send secret. Validation forbids `clientSecret` with this method. |

### 3. DCR-failed error message

When `registration_endpoint` is missing (or registration POSTs fail),
the user sees `authorization server does not support dynamic client
registration` with no hint that a manual path exists. They have to
read the source.

**Decision:** rewrite the error to point at the user-facing setup
guide (see ADR §Resolved Q3) so the workflow is discoverable.

### 4. HTTPS loopback callback (added 2026-05-21)

The initial draft of this ADR listed "HTTPS loopback" as a non-goal
on the assumption that all identified providers accept
`http://127.0.0.1:<port>/callback`. First-contact testing against
Slack contradicted that: Slack's OAuth app registration UI rejects
any redirect URI that is not `https://`. RFC 8252 §7.3 permits this
— it recommends `http://` for loopback but does not require
providers to accept it.

**Decision:** add `callbackScheme` to `OAuth2Block` with values
`"http"` (default, current behaviour) or `"https"`. When `"https"`
is selected, `--login`:

1. Binds the TCP listener on `127.0.0.1:<port>` as usual.
2. Calls `generateLoopbackCert()` to mint an ephemeral self-signed
   ECDSA P-256 certificate with SANs for `127.0.0.1`, `::1`, and
   the DNS name `localhost`. The cert is held in memory only —
   never written to disk — and is regenerated on every `--login`.
3. Wraps the TCP listener in `tls.NewListener` with that cert.
4. Constructs `redirect_uri` as `https://localhost:<port>/callback`
   (the DNS name, not the IP literal). Some providers' app
   registration UIs reject IP literals in redirect URIs; the cert
   SAN covers `localhost` for exactly this case.
5. Prints a one-line "the browser will show a not-secure warning"
   notice so the user is not surprised by it.

The browser warning is unavoidable without going through a public
CA; loopback IPs cannot be issued public certs. The threat model
is benign: at-rest TLS on a loopback listener has no realistic MITM
exposure, and the cert never escapes the process. Users click
through once per `--login`.

Implementation: see `internal/cli/tlscert.go` for cert generation
and `internal/cli/login.go` for the listener wrapping.

## Naming

Names converged after review:

- Field for the loopback port: **`callbackPort`** (rejected
  alternatives: `loginPort`, `redirectPort`, `loopbackPort`).
  Rationale: the field lives inside `auth.oauth2`, so OAuth
  terminology ("callback URL" ≈ "redirect URI") reads more
  naturally than tying it to the CLI subcommand name.
- Field for the client authentication method: **`clientAuthMethod`**
  (rejected: `tokenAuthMethod`, `tokenEndpointAuthMethod`).
  Rationale: RFC 6749 names this "client authentication"; the
  *client* authenticates to the token endpoint, not the token.
- CLI flag: **`--callback-port`**. Symmetric with the field name.
- Values for `clientAuthMethod`: `"post"` / `"basic"` / `"none"`,
  matching the canonical RFC 6749 §2.3.1 and OpenID Connect Core §9
  identifiers (`client_secret_post`, `client_secret_basic`, `none`).

## Schema

`internal/config/profile.go`:

```go
type OAuth2Block struct {
    // ... existing fields ...
    CallbackPort     int    `json:"callbackPort,omitempty"`     // 1-65535. Fixed loopback port for the --login callback. 0/unset = ephemeral (existing behaviour). Needed when the provider requires a pre-registered redirect_uri.
    CallbackScheme   string `json:"callbackScheme,omitempty"`   // "http" (default) or "https". Some providers (Slack) reject http:// loopback URIs at app-registration time.
    ClientAuthMethod string `json:"clientAuthMethod,omitempty"` // "post" (default), "basic", "none". How to send client credentials to the token endpoint.
}
```

Validation rules added to `Profile.Validate()`:

- `callbackPort`: 0 (unset) or 1..65535.
- `callbackScheme`: empty (= `http`) or one of `http|https`.
- `clientAuthMethod`: empty (= `post`) or one of `post|basic|none`.
- `clientAuthMethod=basic` requires `clientSecret`.
- `clientAuthMethod=none` rejects `clientSecret` (public client).

No existing field semantics change. Profiles without the new fields
keep their current behaviour bit-for-bit.

## Backwards compatibility

All new fields default to the empty value, which preserves today's
behaviour:

- `callbackPort = 0` → ephemeral port (current default).
- `clientAuthMethod = ""` → treated as `"post"` (current default).
- The DCR error message change is observable but non-breaking.
- The CLI flag `--callback-port` is additive. Existing scripts that
  call `mcp-guardian --login <name>` keep working.

## Testing plan

### Unit

- `Profile.Validate` for the new fields (valid + boundary cases:
  `callbackPort` 0/1/65535/65536/-1; `clientAuthMethod`
  empty/post/basic/none/junk; basic without secret; none with
  secret).
- `applyClientAuth` table-driven: each method × (with/without
  secret).

### Integration (httptest)

- `--login` with a stub OAuth server, fixed port from profile:
  verify the listener actually binds the requested port and the
  redirect_uri reflects it.
- Token exchange with `clientAuthMethod=basic`: verify the stub
  receives `Authorization: Basic …` and no client_secret in the
  form body.
- Token refresh with `clientAuthMethod=basic`: same shape on the
  refresh request path.

### Manual smoke

- Real Slack app registration → real `--login slack` → real
  `--inspect slack`. Documented as the acceptance criterion in the
  user-facing setup guide.

## Risks and mitigations

| Risk | Mitigation |
|---|---|
| Fixed port already in use → `Listen` fails | Surface a clean error: "port N in use; pick another in `callbackPort` or pass `--callback-port`". |
| User commits `clientSecret` to a profile checked into git | The Slack sample uses `<replace-me>` placeholders; the setup guide warns explicitly. State directory is mode 0600; profile directory inherits user umask. |
| Slack changes their OAuth shape after this lands | Manual config means we own no Slack-specific code paths; only the example profile would need an update. |
| `callbackPort` clashes between profiles using the same provider | Each profile has its own field. Providers accept multiple redirect URIs (Slack does), so different profiles can register different ports. |

## Non-goals

- A UI/TUI for OAuth app registration. The user registers in the
  provider's web console.
- Auto-detecting `clientAuthMethod` from
  `token_endpoint_auth_methods_supported`. Providers without DCR
  frequently mis-publish this field, so we keep it explicit.
- Reworking the SSE transport. mcp-guardian's `sse` transport
  already speaks Streamable HTTP (`sse.go:14-23`).
- Working around Slack's known `issuer` mismatch bug
  (https://github.com/slackapi/slack-mcp-plugin/issues/7). The
  manual path skips discovery entirely, so the bug does not bite.
- Supporting redirect_uri paths other than `/callback`. Slack,
  GitHub and Microsoft Entra all accept the default path.

(HTTPS loopback was originally listed as a non-goal here based on
the same assumption; first-contact testing against Slack
contradicted that assumption and the support was added — see
§Decision §4.)

## Resolved open questions

The proposal that preceded this ADR carried five open questions.
Resolutions:

1. **Schema field names.** `callbackPort` and `clientAuthMethod`
   (see ADR §Naming).
2. **CLI flag name.** `--callback-port` (symmetric with the field
   name).
3. **Docs structure.** Adopt the `docs/{en,ja}/{adr,reference,history}/`
   three-layer layout shared with other newer projects in the org
   (per `feedback_docs_structure_updated`). Existing
   `docs/architecture.md` and `docs/otlp-setup.md` are migrated in
   the same commit, full Japanese translations included. A
   `scripts/docs-mirror-check.sh` enforces the `en/ja` mirror
   contract.
4. **Slack profile sample location.** `examples/profiles/slack.json`,
   matching the existing `examples/profiles/atlassian.json` precedent.
5. **`clientAuthMethod` scope.** Include now. Marginal cost is small
   (one helper + three branches) and the work concretely unblocks
   future GitHub / Microsoft Entra users.

## See also

- `docs/en/reference/oauth2-manual-setup.md` — user-facing setup
  guide. Written after implementation lands so it reflects actual
  behaviour rather than design assumptions.
- `examples/profiles/slack.json` — worked example.
- https://docs.slack.dev/ai/slack-mcp-server/ — Slack MCP server
  reference.
- https://github.com/slackapi/slack-mcp-plugin/issues/7 — known
  Slack discovery `issuer` mismatch (not blocking on the manual
  path).
