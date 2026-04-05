package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// newMockAuthServer creates a mock MCP + OAuth2 server for discovery tests.
func newMockAuthServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	// MCP endpoint: returns 401 with resource_metadata
	mux.HandleFunc("/v1/mcp", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate",
			fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource"`, "BASEURL"))
		w.WriteHeader(http.StatusUnauthorized)
	})

	// Resource metadata
	mux.HandleFunc("/.well-known/oauth-protected-resource", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"resource":              "BASEURL",
			"authorization_servers": []string{"BASEURL/auth"},
		})
	})

	// Auth server metadata
	mux.HandleFunc("/.well-known/oauth-authorization-server/auth", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"issuer":                 "BASEURL/auth",
			"authorization_endpoint": "BASEURL/auth/authorize",
			"token_endpoint":         "BASEURL/auth/token",
			"registration_endpoint":  "BASEURL/auth/register",
			"scopes_supported":       []string{"read", "write", "offline_access"},
			"code_challenge_methods_supported": []string{"S256"},
		})
	})

	// Fallback: auth server metadata without path
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"issuer":                 "BASEURL",
			"authorization_endpoint": "BASEURL/authorize",
			"token_endpoint":         "BASEURL/token",
			"registration_endpoint":  "BASEURL/register",
			"scopes_supported":       []string{"read", "write"},
		})
	})

	// Dynamic client registration
	mux.HandleFunc("/auth/register", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"client_id":                req["client_name"].(string) + "-001",
			"client_name":             req["client_name"],
			"redirect_uris":           req["redirect_uris"],
			"token_endpoint_auth_method": "none",
		})
	})

	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"client_id":   "fallback-client-001",
			"client_name": req["client_name"],
		})
	})

	srv := httptest.NewServer(mux)

	// We can't dynamically replace BASEURL in handler closures easily,
	// so we use a wrapper approach
	return srv
}

// newMockAuthServerWithBaseURL creates a mock server that replaces BASEURL
// in responses with the actual server URL.
func newMockAuthServerWithBaseURL(t *testing.T) *httptest.Server {
	t.Helper()
	var baseURL string

	mux := http.NewServeMux()

	mux.HandleFunc("/v1/mcp", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate",
			fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource"`, baseURL))
		w.WriteHeader(http.StatusUnauthorized)
	})

	mux.HandleFunc("/.well-known/oauth-protected-resource", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"resource":"%s","authorization_servers":["%s/auth"]}`, baseURL, baseURL)
	})

	mux.HandleFunc("/.well-known/oauth-authorization-server/auth", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"issuer":"%s/auth",
			"authorization_endpoint":"%s/auth/authorize",
			"token_endpoint":"%s/auth/token",
			"registration_endpoint":"%s/auth/register",
			"scopes_supported":["read","write","offline_access"],
			"code_challenge_methods_supported":["S256"]
		}`, baseURL, baseURL, baseURL, baseURL)
	})

	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"issuer":"%s",
			"authorization_endpoint":"%s/authorize",
			"token_endpoint":"%s/token",
			"registration_endpoint":"%s/register",
			"scopes_supported":["read","write"]
		}`, baseURL, baseURL, baseURL, baseURL)
	})

	mux.HandleFunc("/auth/register", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"client_id":     "dyn-test-001",
			"client_name":   req["client_name"],
			"redirect_uris": req["redirect_uris"],
		})
	})

	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"client_id":   "fallback-dyn-001",
			"client_name": req["client_name"],
		})
	})

	srv := httptest.NewServer(mux)
	baseURL = srv.URL
	return srv
}

func TestDiscoverOAuth2_FullFlow(t *testing.T) {
	srv := newMockAuthServerWithBaseURL(t)
	defer srv.Close()

	dir := t.TempDir()
	redirectURI := "http://127.0.0.1:9999/callback"

	d, err := DiscoverOAuth2(srv.URL+"/v1/mcp", dir, redirectURI)
	if err != nil {
		t.Fatalf("DiscoverOAuth2: %v", err)
	}

	if d.AuthorizeURL != srv.URL+"/auth/authorize" {
		t.Errorf("AuthorizeURL=%q", d.AuthorizeURL)
	}
	if d.TokenURL != srv.URL+"/auth/token" {
		t.Errorf("TokenURL=%q", d.TokenURL)
	}
	if d.ClientID != "dyn-test-001" {
		t.Errorf("ClientID=%q, want dyn-test-001", d.ClientID)
	}
	if len(d.Scopes) != 3 {
		t.Errorf("Scopes=%v, want 3 items", d.Scopes)
	}

	// Verify discovery was saved to disk
	saved, err := loadDiscovery(dir)
	if err != nil {
		t.Fatalf("loadDiscovery: %v", err)
	}
	if saved.ClientID != "dyn-test-001" {
		t.Errorf("saved ClientID=%q", saved.ClientID)
	}
}

func TestDiscoverOAuth2_FallbackNoResourceMetadata(t *testing.T) {
	// Server returns 401 but without resource_metadata in WWW-Authenticate
	var baseURL string
	mux := http.NewServeMux()

	mux.HandleFunc("/v1/mcp", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="OAuth"`)
		w.WriteHeader(http.StatusUnauthorized)
	})

	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"authorization_endpoint":"%s/authorize",
			"token_endpoint":"%s/token",
			"registration_endpoint":"%s/register"
		}`, baseURL, baseURL, baseURL)
	})

	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"client_id":"fallback-001"}`)
	})

	srv := httptest.NewServer(mux)
	baseURL = srv.URL
	defer srv.Close()

	dir := t.TempDir()
	d, err := DiscoverOAuth2(srv.URL+"/v1/mcp", dir, "http://127.0.0.1:9999/callback")
	if err != nil {
		t.Fatalf("DiscoverOAuth2: %v", err)
	}
	if d.ClientID != "fallback-001" {
		t.Errorf("ClientID=%q, want fallback-001", d.ClientID)
	}
	if d.AuthorizeURL != srv.URL+"/authorize" {
		t.Errorf("AuthorizeURL=%q", d.AuthorizeURL)
	}
}

func TestDiscoverOAuth2_NoRegistrationEndpoint(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/mcp", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="test"`)
		w.WriteHeader(http.StatusUnauthorized)
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"authorization_endpoint":"http://x/auth","token_endpoint":"http://x/token"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := DiscoverOAuth2(srv.URL+"/v1/mcp", t.TempDir(), "http://127.0.0.1:9999/callback")
	if err == nil {
		t.Fatal("expected error when no registration endpoint")
	}
}

func TestDiscoverOAuth2_ServerNotRequiringAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":"probe","result":{}}`)
	}))
	defer srv.Close()

	_, err := DiscoverOAuth2(srv.URL, t.TempDir(), "http://127.0.0.1:9999/callback")
	if err == nil {
		t.Fatal("expected error when server doesn't require auth")
	}
}

func TestExtractParam(t *testing.T) {
	tests := []struct {
		header, param, want string
	}{
		{
			`Bearer resource_metadata="https://example.com/.well-known/oauth"`,
			"resource_metadata",
			"https://example.com/.well-known/oauth",
		},
		{
			`Bearer realm="OAuth", error="invalid_token"`,
			"resource_metadata",
			"",
		},
		{
			`Bearer realm="OAuth", resource_metadata="https://x.com/meta", error="invalid"`,
			"resource_metadata",
			"https://x.com/meta",
		},
	}
	for _, tt := range tests {
		got := extractParam(tt.header, tt.param)
		if got != tt.want {
			t.Errorf("extractParam(%q, %q)=%q, want %q", tt.header, tt.param, got, tt.want)
		}
	}
}

func TestDiscoverySaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	d := &DiscoveredOAuth2{
		AuthorizeURL: "https://auth.example.com/authorize",
		TokenURL:     "https://auth.example.com/token",
		ClientID:     "test-client",
		Scopes:       []string{"read", "write"},
	}

	if err := saveDiscovery(dir, d); err != nil {
		t.Fatalf("saveDiscovery: %v", err)
	}

	// Verify file permissions
	info, err := os.Stat(filepath.Join(dir, "oauth2-discovery.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("permissions=%o, want 0600", info.Mode().Perm())
	}

	loaded, err := loadDiscovery(dir)
	if err != nil {
		t.Fatalf("loadDiscovery: %v", err)
	}
	if loaded.ClientID != "test-client" {
		t.Errorf("ClientID=%q", loaded.ClientID)
	}
	if loaded.AuthorizeURL != "https://auth.example.com/authorize" {
		t.Errorf("AuthorizeURL=%q", loaded.AuthorizeURL)
	}
}

func TestDiscoverySaveAndLoad_NoDir(t *testing.T) {
	_, err := loadDiscovery("/nonexistent/path")
	if err == nil {
		t.Fatal("expected error for nonexistent dir")
	}
}
