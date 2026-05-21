# OAuth2 Manual Setup Guide

This guide covers configuring mcp-guardian against MCP servers whose
authorization server **does not** support RFC 7591 Dynamic Client
Registration (DCR) — the user pre-registers an OAuth app at the
provider, then writes the resulting credentials into a profile.

If `mcp-guardian --login <profile>` returns:

> authorization server does not support dynamic client registration

…this is the guide for you. Slack, GitHub Apps, Microsoft Entra ID,
and most enterprise SaaS providers fall into this category.

Design rationale, decisions and trade-offs are recorded in
[`docs/en/adr/0001-pre-registered-oauth-client.md`](../adr/0001-pre-registered-oauth-client.md).

## When to use this guide

Use the manual path when **any** of the following is true:

- `--login` fails with the DCR error above.
- The provider's developer docs explicitly say "register an app and
  copy the client_id".
- You need a stable `redirect_uri` so the provider's app
  registration UI will accept it.
- You need HTTP Basic authentication on the token endpoint
  (Microsoft Entra ID, some Okta tenants).

If the provider supports DCR, just run `mcp-guardian --login <profile>`
with a minimal `upstream` block and let discovery handle the rest.

## The 5-step manual flow

### 1. Pick a callback port

Choose a free TCP port between 1024 and 65535. Common practice is a
"high" port unlikely to clash with anything else; the worked example
below uses `43117`. This becomes the redirect URI port:

```
http://127.0.0.1:43117/callback        (most providers)
https://localhost:43117/callback       (Slack and other providers that reject http:// redirects — see §HTTPS callback)
```

### 2. Register the OAuth app at the provider

Each provider has its own developer console. Common shape:

| Provider | Where to register |
|---|---|
| Slack MCP | https://api.slack.com/apps → Create New App → fill scopes → install to workspace |
| GitHub Apps | https://github.com/settings/apps → New GitHub App |
| Microsoft Entra ID | Azure Portal → Microsoft Entra ID → App registrations → New registration |

Three things to set on every provider:

1. **Redirect URI** — paste the exact loopback URI from step 1
   (`http://127.0.0.1:43117/callback`). It must match byte-for-byte.
2. **Scopes** — at minimum the scopes the MCP server documents as
   required. Slack publishes per-tool scope requirements at
   https://docs.slack.dev/ai/slack-mcp-server/.
3. **App type** — choose the type the MCP server accepts. Slack MCP
   for example only allows directory-published apps or internal apps.

When registration is done, the provider gives you:

- `client_id` — public identifier (safe to commit if your repo is
  private; treat as semi-secret otherwise).
- `client_secret` — confidential token (**never** commit; see §Security).

### 3. Write the profile

Create `~/.config/mcp-guardian/profiles/<name>.json`. The Slack
worked example is shipped as `examples/profiles/slack.json` — copy and
edit:

```json
{
  "name": "slack",
  "upstream": {
    "transport": "sse",
    "url": "https://mcp.slack.com/mcp"
  },
  "auth": {
    "oauth2": {
      "flow": "authorization_code",
      "authorizeUrl": "https://slack.com/oauth/v2_user/authorize",
      "tokenUrl":     "https://slack.com/api/oauth.v2.user.access",
      "clientId":     "<your Slack app's client_id>",
      "clientSecret": "<your Slack app's client_secret>",
      "callbackPort": 43117,
      "callbackScheme": "https",
      "clientAuthMethod": "post",
      "scopes": ["chat:write", "channels:history", "search:read"]
    }
  },
  "governance": {
    "enforcement": "strict"
  }
}
```

Field-by-field:

| Field | Required? | Notes |
|---|---|---|
| `flow` | yes | Must be `"authorization_code"` for browser-based user OAuth. |
| `authorizeUrl` | yes | Provider's authorization endpoint. Slack: `https://slack.com/oauth/v2_user/authorize` (note: `v2_user`, not `v2`). |
| `tokenUrl` | yes | Provider's token exchange endpoint. Slack: `https://slack.com/api/oauth.v2.user.access`. |
| `clientId` | yes | From the registered app. |
| `clientSecret` | usually | Confidential clients only. Omit (and use `clientAuthMethod: "none"`) for PKCE-only public clients. |
| `callbackPort` | yes for manual setup | Must match the port in the provider-registered redirect URI. 0 / omitted = ephemeral (the legacy behaviour, only useful with DCR-capable providers). |
| `callbackScheme` | depends on provider | `"http"` (default — RFC 8252 §7.3 loopback recommendation, accepted by most providers) or `"https"` (Slack and a few others reject `http://` redirects). See §HTTPS callback. |
| `clientAuthMethod` | no | Default `"post"`. See §Choosing clientAuthMethod. |
| `scopes` | depends | Space-joined in the authorize URL. Some providers honour, others ignore in favour of registered scopes. |
| `extraParams` | no | Provider-specific additional query params on the authorize URL (e.g. `audience`, `prompt=consent`). |

### 4. Run `--login`

```sh
mcp-guardian --login slack
```

What happens:

1. mcp-guardian binds `127.0.0.1:43117` (the configured callback port).
2. A browser opens the provider's authorize URL with `redirect_uri` =
   `http://127.0.0.1:43117/callback` and a PKCE challenge.
3. You authenticate at the provider in the browser.
4. The provider redirects back to mcp-guardian with an authorization
   code.
5. mcp-guardian exchanges the code at the token endpoint using the
   configured `clientAuthMethod`.
6. The access token + refresh token are written to
   `~/.config/mcp-guardian/state/slack/tokens.json` with mode 0600.

If you need to override the port for a single invocation (e.g. to
debug a port conflict without editing the profile):

```sh
mcp-guardian --login slack --callback-port 43118
```

The CLI flag wins over the profile value.

### 5. Verify

Launch the proxy and inspect the MCP server's tool list:

```sh
mcp-guardian --inspect slack
```

You should see the provider's MCP tools. The access token will be
auto-refreshed via the stored refresh token on expiry.

## HTTPS callback

Some providers — notably **Slack** — reject any redirect URI that
is not `https://` at app-registration time. RFC 8252 §7.3 *recommends*
`http://` for loopback but does not *require* providers to accept it.

Set `"callbackScheme": "https"` in the profile to make `--login`:

1. Mint an ephemeral self-signed ECDSA certificate covering
   `127.0.0.1`, `::1` and DNS `localhost` (validity: 1 hour; held in
   memory, never written to disk).
2. Wrap the TCP callback listener in a TLS listener using that cert.
3. Use `https://localhost:<port>/callback` (DNS name, not IP literal
   — some provider UIs reject IP literals in redirect URIs).

You register exactly that URI at the provider:

```
https://localhost:43117/callback
```

**Browser warning is normal.** The first time you hit
`https://localhost:43117/...` the browser shows "Not secure" /
"Your connection is not private" because the cert is self-signed.
Click "Advanced" → "Proceed to localhost (unsafe)". The connection
is loopback-only and the cert never leaves the process; there is no
realistic MITM threat.

If you'd rather avoid the warning entirely, see whether your provider
accepts `http://` (most do) — that path needs no cert at all.

## Choosing `clientAuthMethod`

| Method | When | What it sends |
|---|---|---|
| `post` (default) | Most providers including Slack | `client_id`, `client_secret` as form body params |
| `basic` | Microsoft Entra ID, some Okta tenants | `Authorization: Basic <base64(client_id:client_secret)>` header; **no** credentials in the form body |
| `none` | PKCE-only public clients (rare) | `client_id` only, never the secret. Validation forbids configuring `clientSecret` alongside. |

If you don't know which the provider wants: try `post` first. If the
token exchange comes back with HTTP 401 and an error about
`client_authentication`, switch to `basic`.

## Per-provider notes

### Slack MCP

- **App type**: must be directory-published OR internal. Custom
  third-party apps cannot use the MCP endpoint.
- **Redirect URI must be `https://`**: Slack's app registration UI
  rejects `http://` even for loopback. Set
  `"callbackScheme": "https"` in the profile and register
  `https://localhost:<callbackPort>/callback` at the Slack app. The
  browser will show a one-time self-signed-cert warning that you
  click through — see §HTTPS callback.
- **OAuth endpoint**: use `oauth.v2.user.access`, not `oauth.v2.access` —
  the MCP authorization uses the *user* token grant, not the bot grant.
- **Known issuer mismatch**: discovery metadata at
  `https://mcp.slack.com/.well-known/oauth-authorization-server` reports
  `issuer: "https://slack.com"`. This bites only clients that strictly
  validate the issuer; the manual path skips discovery entirely so it
  does not affect mcp-guardian.
- **Scope list**: https://docs.slack.dev/ai/slack-mcp-server/ — required
  scopes vary per tool. Start narrow and expand as needed.
- **Transport**: Slack MCP only supports Streamable HTTP. mcp-guardian's
  `"transport": "sse"` is actually Streamable HTTP under the hood (the
  name is legacy), so this just works.

### GitHub Apps

- Register at https://github.com/settings/apps → New GitHub App.
- Set Callback URL to your `http://127.0.0.1:<port>/callback`.
- Generate a client secret in the app settings.
- `clientAuthMethod`: `"post"` works.

### Microsoft Entra ID (formerly Azure AD)

- Azure Portal → Entra ID → App registrations → New registration.
- Add a redirect URI of type "Public client/native (mobile & desktop)"
  pointing at `http://127.0.0.1:<port>/callback`.
- For "Web" platform with a client secret, `clientAuthMethod: "basic"`
  is the safer default.

## Security

- `clientSecret` is a **confidential** credential. Treat it like a
  password.
- **Never commit profiles with real secrets to a public repository.**
  The shipped `examples/profiles/slack.json` uses `<replace-...>`
  placeholders for exactly this reason.
- Profiles live in `~/.config/mcp-guardian/profiles/`, which inherits
  your user umask. State directory (`~/.config/mcp-guardian/state/<profile>/`)
  is created mode 0700 and `tokens.json` is written mode 0600 by
  mcp-guardian.
- If you must commit a profile (e.g. for a team), keep `clientSecret`
  in a separate `.env` file or secret manager and reference it via an
  environment variable expansion in your wrapper script.

## Refresh and expiry

`tokens.json` stores `access_token`, `refresh_token` and `expires_at`.
On each request mcp-guardian:

1. If `access_token` is valid (expiry > now + 30 s), use it.
2. Otherwise, POST `grant_type=refresh_token` to `tokenUrl` using the
   configured `clientAuthMethod`. Save the new tokens.
3. If the refresh token has expired or been revoked, the next request
   returns an error and you must re-run `mcp-guardian --login <profile>`.

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| `parse profile: json: unknown field "callbackSchema"` (or similar) | Field name typo. Profile loading is strict so spelling mistakes surface immediately. Correct field names are `callbackScheme` (not `Schema` — it's the URL scheme), `callbackPort`, `clientAuthMethod`. |
| `--login` runs but the redirect URI in the browser is `http://…` even though you set `callbackScheme: "https"` | Either the JSON has a typo in the field name (above) or the profile is from an older mcp-guardian. Verify with `cat <profile>.json | jq '.auth.oauth2.callbackScheme'`. |
| `start callback server on port N: bind: address already in use` | Another process holds that port. Try `lsof -i :N`, or pick a different `callbackPort` and update the provider's redirect URI list. |
| `authorization error: invalid_redirect_uri` | The `redirect_uri` mcp-guardian sends doesn't match the provider's allow-list. Verify the port matches exactly; protocol (`http`), host (`127.0.0.1`), and path (`/callback`) are also load-bearing. |
| `token exchange failed (HTTP 401)` mentioning `client_authentication` | Try switching `clientAuthMethod` from `"post"` to `"basic"` or vice versa. |
| `token exchange failed (HTTP 400) invalid_grant` | The authorization code is single-use and short-lived. Re-run `--login`. Time skew between client and provider can also cause this. |
| `no stored tokens found (run --login first)` at proxy start | The state directory has no `tokens.json` for this profile. Run `--login` first. |
| Browser opens but never returns | Check that the callback port matches between profile and the provider registration. Also that no firewall is blocking 127.0.0.1. |

For deeper diagnosis, raise the log level via the global config's
`logLevel`, then reproduce.
