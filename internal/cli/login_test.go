package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/nlink-jp/mcp-guardian/internal/transport"
)

// newMockLoginServer creates a mock server that handles the full login flow:
// MCP 401 → discovery → registration → authorize (auto-redirect) → token exchange
func newMockLoginServer(t *testing.T) *httptest.Server {
	t.Helper()
	var baseURL string

	mux := http.NewServeMux()

	// MCP endpoint: 401
	mux.HandleFunc("/v1/mcp", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="test"`)
		w.WriteHeader(http.StatusUnauthorized)
	})

	// Auth server metadata (fallback)
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"authorization_endpoint":"%s/authorize",
			"token_endpoint":"%s/token",
			"registration_endpoint":"%s/register",
			"scopes_supported":["read","offline_access"]
		}`, baseURL, baseURL, baseURL)
	})

	// Dynamic registration
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"client_id":     "login-test-client",
			"redirect_uris": req["redirect_uris"],
		})
	})

	// Authorize: auto-redirect back with code (simulates user clicking approve)
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		redirectURI := r.URL.Query().Get("redirect_uri")
		state := r.URL.Query().Get("state")
		http.Redirect(w, r, fmt.Sprintf("%s?code=test-auth-code&state=%s", redirectURI, state), http.StatusFound)
	})

	// Token exchange
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "login-access-tok",
			"refresh_token": "login-refresh-tok",
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	})

	srv := httptest.NewServer(mux)
	baseURL = srv.URL
	return srv
}

func init() {
	// Replace browser opener with HTTP GET (mock servers auto-redirect)
	openBrowserFunc = func(url string) {
		http.Get(url) //nolint:errcheck
	}
}

func TestLogin_AutoDiscovery(t *testing.T) {
	srv := newMockLoginServer(t)
	defer srv.Close()

	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	profilePath := filepath.Join(dir, "test-profile.json")

	// Write minimal profile — no auth config
	profile := fmt.Sprintf(`{
		"name": "test",
		"upstream": {"transport": "sse", "url": "%s/v1/mcp"},
		"stateDir": "%s"
	}`, srv.URL, stateDir)
	os.WriteFile(profilePath, []byte(profile), 0644)

	// Run login — it will auto-discover, register, and simulate browser
	// The mock server auto-redirects authorize → callback
	err := Login(profilePath)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	// Verify tokens were saved
	tokensPath := filepath.Join(stateDir, "tokens.json")
	data, err := os.ReadFile(tokensPath)
	if err != nil {
		t.Fatalf("tokens.json not found: %v", err)
	}

	var tokens transport.StoredTokens
	json.Unmarshal(data, &tokens)

	if tokens.AccessToken != "login-access-tok" {
		t.Errorf("access_token=%q, want login-access-tok", tokens.AccessToken)
	}
	if tokens.RefreshToken != "login-refresh-tok" {
		t.Errorf("refresh_token=%q, want login-refresh-tok", tokens.RefreshToken)
	}

	// Verify discovery cache was saved
	discPath := filepath.Join(stateDir, "oauth2-discovery.json")
	discData, err := os.ReadFile(discPath)
	if err != nil {
		t.Fatalf("oauth2-discovery.json not found: %v", err)
	}

	var disc DiscoveredOAuth2
	json.Unmarshal(discData, &disc)

	if disc.ClientID != "login-test-client" {
		t.Errorf("discovered client_id=%q, want login-test-client", disc.ClientID)
	}
}

func TestLogin_ExplicitOAuth2Config(t *testing.T) {
	// Server with authorize auto-redirect + token endpoint
	var baseURL string
	mux := http.NewServeMux()

	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		redirectURI := r.URL.Query().Get("redirect_uri")
		state := r.URL.Query().Get("state")
		http.Redirect(w, r, fmt.Sprintf("%s?code=explicit-code&state=%s", redirectURI, state), http.StatusFound)
	})

	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "explicit-access-tok",
			"refresh_token": "explicit-refresh-tok",
			"token_type":    "Bearer",
			"expires_in":    7200,
		})
	})

	srv := httptest.NewServer(mux)
	baseURL = srv.URL
	defer srv.Close()

	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	profilePath := filepath.Join(dir, "profile.json")

	// Write profile with explicit OAuth2 config
	profile := fmt.Sprintf(`{
		"name": "explicit",
		"upstream": {"transport": "sse", "url": "http://unused"},
		"auth": {
			"oauth2": {
				"flow": "authorization_code",
				"authorizeUrl": "%s/authorize",
				"tokenUrl": "%s/token",
				"clientId": "explicit-client"
			}
		},
		"stateDir": "%s"
	}`, baseURL, baseURL, stateDir)
	os.WriteFile(profilePath, []byte(profile), 0644)

	err := Login(profilePath)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(stateDir, "tokens.json"))
	var tokens transport.StoredTokens
	json.Unmarshal(data, &tokens)

	if tokens.AccessToken != "explicit-access-tok" {
		t.Errorf("access_token=%q", tokens.AccessToken)
	}
}

func TestLogin_ExtraParams(t *testing.T) {
	var receivedAudience string
	var baseURL string
	mux := http.NewServeMux()

	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		receivedAudience = r.URL.Query().Get("audience")
		redirectURI := r.URL.Query().Get("redirect_uri")
		state := r.URL.Query().Get("state")
		http.Redirect(w, r, fmt.Sprintf("%s?code=c&state=%s", redirectURI, state), http.StatusFound)
	})

	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"t","token_type":"Bearer","expires_in":3600}`)
	})

	srv := httptest.NewServer(mux)
	baseURL = srv.URL
	defer srv.Close()

	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	profilePath := filepath.Join(dir, "profile.json")

	profile := fmt.Sprintf(`{
		"name": "extra",
		"upstream": {"transport": "sse", "url": "http://unused"},
		"auth": {
			"oauth2": {
				"flow": "authorization_code",
				"authorizeUrl": "%s/authorize",
				"tokenUrl": "%s/token",
				"clientId": "c",
				"extraParams": {"audience": "api.example.com", "prompt": "consent"}
			}
		},
		"stateDir": "%s"
	}`, baseURL, baseURL, stateDir)
	os.WriteFile(profilePath, []byte(profile), 0644)

	Login(profilePath)

	if receivedAudience != "api.example.com" {
		t.Errorf("audience=%q, want api.example.com", receivedAudience)
	}
}

func TestLogin_DefaultStateDir(t *testing.T) {
	srv := newMockLoginServer(t)
	defer srv.Close()

	dir := t.TempDir()
	profilePath := filepath.Join(dir, "myprofile.json")

	// Profile WITHOUT stateDir — must use DefaultStateDir
	profile := fmt.Sprintf(`{
		"name": "myprofile",
		"upstream": {"transport": "sse", "url": "%s/v1/mcp"}
	}`, srv.URL)
	os.WriteFile(profilePath, []byte(profile), 0644)

	err := Login(profilePath)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	// Tokens must be saved to DefaultStateDir("myprofile"), not cwd/.governance
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine HOME")
	}
	expectedDir := filepath.Join(home, ".config", "mcp-guardian", "state", "myprofile")
	tokensPath := filepath.Join(expectedDir, "tokens.json")
	data, err := os.ReadFile(tokensPath)
	if err != nil {
		t.Fatalf("tokens.json not found at %s: %v", tokensPath, err)
	}
	// Cleanup: remove the created state directory
	defer os.RemoveAll(expectedDir)

	var tokens transport.StoredTokens
	json.Unmarshal(data, &tokens)

	if tokens.AccessToken != "login-access-tok" {
		t.Errorf("access_token=%q, want login-access-tok", tokens.AccessToken)
	}

	// Verify .governance was NOT created in cwd
	if _, err := os.Stat(".governance/tokens.json"); err == nil {
		t.Error("tokens.json found in .governance/ — should use DefaultStateDir instead")
	}
}

func TestLogin_WrongFlow(t *testing.T) {
	dir := t.TempDir()
	profilePath := filepath.Join(dir, "profile.json")
	os.WriteFile(profilePath, []byte(`{
		"name": "test",
		"upstream": {"transport": "sse", "url": "http://unused"},
		"auth": {"oauth2": {"flow": "client_credentials", "tokenUrl": "http://x", "clientId": "x", "clientSecret": "x"}}
	}`), 0644)

	err := Login(profilePath)
	if err == nil {
		t.Fatal("expected error for client_credentials flow with --login")
	}
}

func TestLogin_NoUpstreamURL(t *testing.T) {
	dir := t.TempDir()
	profilePath := filepath.Join(dir, "profile.json")
	os.WriteFile(profilePath, []byte(`{"name": "test"}`), 0644)

	err := Login(profilePath)
	if err == nil {
		t.Fatal("expected error when no auth and no upstream URL")
	}
}
