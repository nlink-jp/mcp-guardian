package transport

import (
	"net/http"
	"net/url"
	"testing"
)

// TestApplyClientAuth exhaustively verifies the credential placement
// for each ClientAuthMethod × (secret present / absent) combination.
// See docs/en/adr/0001-pre-registered-oauth-client.md §Decision §2.
func TestApplyClientAuth(t *testing.T) {
	cases := []struct {
		name          string
		method        string
		clientID      string
		clientSecret  string
		wantFormID    string // expected form value for client_id, "" = not set
		wantFormSec   string // expected form value for client_secret, "" = not set
		wantBasicHdr  bool   // expected Authorization: Basic ... header
		wantBasicUser string
		wantBasicPass string
	}{
		// post (default) — form-encoded credentials, the existing behaviour
		{
			name:        "post with secret",
			method:      "post",
			clientID:    "myid",
			clientSecret: "mysec",
			wantFormID:  "myid",
			wantFormSec: "mysec",
		},
		{
			name:       "post without secret (PKCE-style public client)",
			method:     "post",
			clientID:   "myid",
			wantFormID: "myid",
		},
		{
			name:        "empty method defaults to post",
			method:      "",
			clientID:    "myid",
			clientSecret: "mysec",
			wantFormID:  "myid",
			wantFormSec: "mysec",
		},

		// basic — Authorization header, NO form credentials
		{
			name:          "basic with secret",
			method:        "basic",
			clientID:      "myid",
			clientSecret:  "mysec",
			wantBasicHdr:  true,
			wantBasicUser: "myid",
			wantBasicPass: "mysec",
		},

		// none — form-encoded client_id only, never secret
		{
			name:       "none ignores secret",
			method:     "none",
			clientID:   "myid",
			clientSecret: "should-not-leak",
			wantFormID: "myid",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest("POST", "https://example.com/token", nil)
			form := url.Values{}
			form.Set("grant_type", "authorization_code")

			ApplyClientAuth(req, form, ClientAuthConfig{
				Method:       tc.method,
				ClientID:     tc.clientID,
				ClientSecret: tc.clientSecret,
			})

			if got := form.Get("client_id"); got != tc.wantFormID {
				t.Errorf("form client_id=%q, want %q", got, tc.wantFormID)
			}
			if got := form.Get("client_secret"); got != tc.wantFormSec {
				t.Errorf("form client_secret=%q, want %q", got, tc.wantFormSec)
			}

			user, pass, ok := req.BasicAuth()
			if tc.wantBasicHdr {
				if !ok {
					t.Errorf("expected Authorization: Basic header, none set")
				}
				if user != tc.wantBasicUser {
					t.Errorf("Basic user=%q, want %q", user, tc.wantBasicUser)
				}
				if pass != tc.wantBasicPass {
					t.Errorf("Basic pass=%q, want %q", pass, tc.wantBasicPass)
				}
			} else {
				if ok {
					t.Errorf("unexpected Authorization: Basic header: user=%q pass=%q", user, pass)
				}
			}

			// grant_type must survive untouched in every case
			if form.Get("grant_type") != "authorization_code" {
				t.Errorf("grant_type clobbered: %q", form.Get("grant_type"))
			}
		})
	}
}
