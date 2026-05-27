# ADR 0003 — Treat refresh-less OAuth tokens as non-expiring instead of imposing a synthetic 1-hour expiry

- **Status:** Accepted
- **Date:** 2026-05-27
- **Driver:** `--login slack` stores `refresh_token: ""` and a 1-hour
  `expires_at`, after which the proxy reports the token as expired even
  though a Slack token issued without rotation never expires
- **Generalises to:** any provider that issues a non-expiring access
  token with no `refresh_token` and no `expires_in`
- **Relates to:** ADR-0002 (this removes the *spurious* trigger for that
  fix's error path)

---

## Context

A real Slack `--login` produces this `tokens.json`:

```json
{
  "access_token": "xoxp-…",
  "refresh_token": "",
  "token_type": "Bearer",
  "expires_at": 1779760685
}
```

`expires_at` is exactly the login time **+ 1 hour**. That value was not
sent by Slack — it is a fallback invented by `--login`.

### Slack's actual behaviour

Slack issues refresh tokens only when **token rotation** is explicitly
enabled on the app. Without rotation, the token-exchange response
contains **no `expires_in` and no `refresh_token`**, and the access token
(here a `xoxp-` user token) **does not expire**. `refresh_token: ""` is
therefore the correct, expected state — not an error.

### How the synthetic expiry is created and then misread

1. `internal/cli/login.go` — when the response has no `expires_in`
   (`ExpiresIn <= 0`), it stores `expires_at = now + 1h`:

   ```go
   expiresAt := time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).Unix()
   if tokenResp.ExpiresIn <= 0 {
       expiresAt = time.Now().Add(1 * time.Hour).Unix()
   }
   ```

2. `internal/transport/authcode.go` — one hour later `Token()` sees the
   token as expired, has no refresh token, and fails:

   ```go
   if p.tokens.RefreshToken == "" {
       return "", fmt.Errorf("access token expired and no refresh token available (run --login again)")
   }
   ```

Net effect: **a valid, non-expiring Slack token is declared dead one hour
after login, forcing a needless `--login` every hour.**

### Relationship to ADR-0002

ADR-0002 made this failure *fail fast* — the proxy now returns a JSON-RPC
error to the client instead of hanging. But ADR-0002 treated the symptom
on the assumption that the token had genuinely expired. It had not: the
expiry was synthetic. ADR-0002's invariant (a request that can't be
forwarded always gets a JSON-RPC error) remains correct and unchanged;
this ADR removes the *false* trigger so that path only fires on real
failures.

## Decision

The only authority on whether a non-refreshable token is still valid is
the upstream server; a local clock cannot know an expiry the server never
declared. So:

1. **Stop inventing a 1-hour expiry when the server gives no
   `expires_in`.** In `--login`, compute `expires_at` by case:
   - `expires_in > 0` → honour it.
   - no `expires_in` **but** a `refresh_token` is present → store a
     1-hour probe (the provider can renew silently before that).
   - no `expires_in` **and** no `refresh_token` → store `0`, meaning "no
     known expiry".

2. **When there is no refresh token, ignore the stored expiry in
   `Token()` and return the access token as-is.** There is nothing to
   renew with, so a local expiry check is unactionable. A genuine
   revocation surfaces as an upstream **401**, which the proxy converts
   to a JSON-RPC error per ADR-0002. Only error if there is literally no
   access token.

Token rotation is fully preserved: whenever a `refresh_token` exists, the
existing expiry check + auto-refresh path is untouched.

## Consequences

- Slack (and any non-rotating provider) works indefinitely after a single
  `--login`; the hourly re-login disappears.
- **Existing `tokens.json` files** written before this change carry the
  synthetic +1h `expires_at`. Because the new `Token()` ignores the
  expiry when `refresh_token` is empty, they start working again with no
  re-login required.
- The **401 retry path still terminates correctly.** On a real 401 the
  proxy calls `Invalidate()`, which clears the access token; the retry's
  `Token()` then returns `no access token available (run --login)` and
  the proxy surfaces it (ADR-0002 invariant holds) instead of looping.
- The literal string `access token expired and no refresh token available
  (run --login again)` is removed from the no-refresh path. The
  forward-failure test (`proxy_forward_failure_test.go`) injects that text
  via a stub transport, so it is unaffected, but the real provider can no
  longer emit it from a synthetic expiry.

## Out of scope

- **Slack token rotation support.** If a user opts into rotation, Slack
  returns `refresh_token`/`expires_in` nested under `authed_user` for user
  tokens; `--login` currently parses only top-level fields. Wiring
  `authed_user.{access_token,refresh_token,expires_in}` is a separate
  change, tracked for when rotation is actually requested.
- Proactive auth-state surfacing at proxy startup (unchanged from
  ADR-0002).

## Implementation

- `internal/cli/login.go` — replace the `<= 0 → +1h` fallback with the
  three-case `switch`; `expires_at = 0` when there is no `expires_in` and
  no `refresh_token`.
- `internal/transport/authcode.go` — `Token()`: short-circuit when
  `RefreshToken == ""` (return the access token, or error only if the
  access token is also empty); remove the now-dead "expired and no refresh
  token" branch.
- Tests:
  - `authcode_test.go` — a refresh-less token with a past/zero
    `expires_at` returns the access token **without contacting the token
    endpoint** (point `TokenURL` at a server that fails the test if hit);
    a refresh-less token with an empty access token errors.
  - `login_test.go` — a token response with no `expires_in` and no
    `refresh_token` stores `expires_at == 0`; one with a `refresh_token`
    but no `expires_in` stores ~1h.
- `README.md` / `README.ja.md` — Slack/manual-setup section: note that
  without token rotation Slack issues a non-expiring token and no refresh
  token, which is expected; mcp-guardian uses it indefinitely and reports
  an auth failure only on a real 401.
- `CHANGELOG.md` — `0.8.3` Fixed entry.
- `AGENTS.md` — gotcha note.

Verification: `make test`; manual — re-run `--login slack`, confirm
`tokens.json` shows `expires_at: 0`, and the proxy serves requests more
than an hour after login without a re-login.
