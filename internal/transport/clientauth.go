package transport

import (
	"net/http"
	"net/url"
)

// ClientAuthMethod selects how OAuth2 client credentials are sent to a
// token endpoint. The values match the canonical RFC 6749 §2.3.1 and
// OpenID Connect Core §9 identifiers (`client_secret_post`,
// `client_secret_basic`, `none`).
//
//   - "" / "post":  client_id and client_secret as form body params.
//     This is the most widely accepted method and the default.
//   - "basic":      HTTP Basic auth (Authorization: Basic …). Required
//     by Microsoft Entra ID and some Okta tenants.
//   - "none":       PKCE-only public clients. client_id in the form,
//     never the secret. (Validation forbids a non-empty secret here.)
//
// Design rationale: docs/en/adr/0001-pre-registered-oauth-client.md
// §Decision §2 — keep the method explicit (no auto-discovery from
// token_endpoint_auth_methods_supported) because non-DCR providers
// frequently mis-publish that field.
type ClientAuthConfig struct {
	Method       string
	ClientID     string
	ClientSecret string
}

// ApplyClientAuth populates req with client credentials for a token
// endpoint request according to cfg.Method.
//
// The shape of form is mutated for "post" and "none"; for "basic",
// req.SetBasicAuth is used and form is left untouched (per RFC 6749
// §2.3.1 the credentials MUST NOT appear twice). Callers are
// expected to call this BEFORE writing form into req.Body.
//
// req must not be nil. form may be nil only when Method=="basic" and
// the caller has no other form parameters to send, which is rare —
// pass an empty url.Values{} when in doubt.
func ApplyClientAuth(req *http.Request, form url.Values, cfg ClientAuthConfig) {
	switch cfg.Method {
	case "basic":
		req.SetBasicAuth(cfg.ClientID, cfg.ClientSecret)
	case "none":
		form.Set("client_id", cfg.ClientID)
	default: // "" or "post"
		form.Set("client_id", cfg.ClientID)
		if cfg.ClientSecret != "" {
			form.Set("client_secret", cfg.ClientSecret)
		}
	}
}
