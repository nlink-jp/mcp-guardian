package cli

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/nlink-jp/mcp-guardian/internal/config"
	"github.com/nlink-jp/mcp-guardian/internal/transport"
)

// LoginOptions tunes the behaviour of Login.
//
// CallbackPort, when non-zero, forces the OAuth callback server to
// bind that specific loopback port instead of letting the OS pick an
// ephemeral one. This is required by OAuth providers that do not
// support RFC 7591 Dynamic Client Registration and demand an exact
// redirect_uri at app-registration time (Slack, GitHub, Microsoft
// Entra ID, most enterprise SaaS). Zero means "use the profile's
// auth.oauth2.callbackPort, or fall back to ephemeral".
type LoginOptions struct {
	CallbackPort int
}

// Login performs the OAuth2 Authorization Code flow with PKCE.
// Opens a browser for user authentication, receives the callback,
// exchanges the code for tokens, and stores them in the profile's state directory.
func Login(profileNameOrPath string, opts LoginOptions) error {
	profile, err := config.ResolveProfile(profileNameOrPath)
	if err != nil {
		return fmt.Errorf("load profile: %w", err)
	}

	// Determine state directory
	stateDir := profile.StateDir
	if stateDir == "" {
		stateDir = config.DefaultStateDir(profile.Name)
	}
	if stateDir == "" {
		return fmt.Errorf("cannot determine state directory for profile %q", profile.Name)
	}

	// Resolve the OAuth2 callback port: CLI flag wins, then profile,
	// else 0 (= ephemeral, current behaviour). Pre-registered OAuth
	// apps require an exact redirect_uri so the port must be stable
	// across logins; ephemeral remains the default for DCR-capable
	// providers where the port is registered fresh each time.
	port := opts.CallbackPort
	if port == 0 && profile.Auth != nil && profile.Auth.OAuth2 != nil {
		port = profile.Auth.OAuth2.CallbackPort
	}
	// Resolve the callback scheme. Some providers (notably Slack)
	// reject http:// loopback URIs at app registration time and only
	// accept https://. When the profile asks for https we wrap the
	// TCP listener in a TLS listener using an ephemeral self-signed
	// certificate (see generateLoopbackCert). The browser will show
	// a "not secure" warning that the user must click through — the
	// cert is loopback-only and never leaves memory.
	scheme := "http"
	if profile.Auth != nil && profile.Auth.OAuth2 != nil && profile.Auth.OAuth2.CallbackScheme == "https" {
		scheme = "https"
	}
	// Start local callback server first (need the port for client registration)
	tcpListener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("start callback server on port %d: %w (pick another port via callbackPort or --callback-port)", port, err)
	}
	port = tcpListener.Addr().(*net.TCPAddr).Port

	var listener net.Listener = tcpListener
	host := "127.0.0.1"
	if scheme == "https" {
		cert, certErr := generateLoopbackCert()
		if certErr != nil {
			tcpListener.Close()
			return fmt.Errorf("generate self-signed cert for https callback: %w", certErr)
		}
		listener = tls.NewListener(tcpListener, &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		})
		// Use the DNS name in the redirect URI so providers that
		// restrict redirect URIs to hostnames (e.g. Slack) accept
		// the registration; the cert SAN covers both forms.
		host = "localhost"
		fmt.Println("Note: the browser will show a \"not secure\" warning for the self-signed TLS callback — clicking through is expected.")
	}
	redirectURI := fmt.Sprintf("%s://%s:%d/callback", scheme, host, port)

	// If no explicit OAuth2 config, auto-discover from MCP server
	if profile.Auth == nil || profile.Auth.OAuth2 == nil {
		if profile.Upstream == nil || profile.Upstream.URL == "" {
			listener.Close()
			return fmt.Errorf("profile %q has no oauth2 configuration and no upstream URL for discovery", profile.Name)
		}

		discovered, err := DiscoverOAuth2(profile.Upstream.URL, stateDir, redirectURI)
		if err != nil {
			listener.Close()
			return fmt.Errorf("OAuth2 discovery failed: %w", err)
		}

		profile.Auth = &config.AuthBlock{
			OAuth2: &config.OAuth2Block{
				Flow:         "authorization_code",
				AuthorizeURL: discovered.AuthorizeURL,
				TokenURL:     discovered.TokenURL,
				ClientID:     discovered.ClientID,
				ClientSecret: discovered.ClientSecret,
				Scopes:       discovered.Scopes,
			},
		}
	}

	oauth := profile.Auth.OAuth2
	if oauth.Flow != "" && oauth.Flow != "authorization_code" {
		listener.Close()
		return fmt.Errorf("profile %q: --login requires flow 'authorization_code', got %q", profile.Name, oauth.Flow)
	}

	// Generate PKCE code verifier and challenge
	verifier, err := generateCodeVerifier()
	if err != nil {
		listener.Close()
		return fmt.Errorf("generate PKCE verifier: %w", err)
	}
	challenge := computeCodeChallenge(verifier)

	// Generate state parameter for CSRF protection
	stateParam, err := generateState()
	if err != nil {
		listener.Close()
		return fmt.Errorf("generate state: %w", err)
	}

	// Channel to receive the authorization code
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		// Verify state (constant-time comparison to prevent timing attacks)
		if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("state")), []byte(stateParam)) != 1 {
			errCh <- fmt.Errorf("state parameter mismatch (possible CSRF)")
			http.Error(w, "State mismatch", http.StatusBadRequest)
			return
		}

		// Check for error
		if errMsg := r.URL.Query().Get("error"); errMsg != "" {
			desc := r.URL.Query().Get("error_description")
			errCh <- fmt.Errorf("authorization error: %s: %s", errMsg, desc)
			fmt.Fprintf(w, "<html><body><h2>Authorization failed</h2><p>%s: %s</p><p>You can close this window.</p></body></html>",
				html.EscapeString(errMsg), html.EscapeString(desc))
			return
		}

		code := r.URL.Query().Get("code")
		if code == "" {
			errCh <- fmt.Errorf("no authorization code in callback")
			http.Error(w, "No code", http.StatusBadRequest)
			return
		}

		codeCh <- code
		fmt.Fprint(w, "<html><body><h2>Authorization successful</h2><p>You can close this window and return to the terminal.</p></body></html>")
	})

	server := &http.Server{Handler: mux}
	go server.Serve(listener)
	defer server.Close()

	// Build authorization URL
	// Apply extra parameters first, then set security-critical params
	// so they cannot be overridden by ExtraParams
	params := url.Values{}
	for k, v := range oauth.ExtraParams {
		params.Set(k, v)
	}
	if len(oauth.Scopes) > 0 {
		params.Set("scope", strings.Join(oauth.Scopes, " "))
	}
	// Security-critical parameters (set last to prevent override)
	params.Set("response_type", "code")
	params.Set("client_id", oauth.ClientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("state", stateParam)
	params.Set("code_challenge", challenge)
	params.Set("code_challenge_method", "S256")

	// Properly append params to authorizeUrl (handles existing query string)
	authBase, err := url.Parse(oauth.AuthorizeURL)
	if err != nil {
		return fmt.Errorf("parse authorizeUrl: %w", err)
	}
	existing := authBase.Query()
	for k, vs := range params {
		for _, v := range vs {
			existing.Set(k, v)
		}
	}
	authBase.RawQuery = existing.Encode()
	authURL := authBase.String()

	// Open browser
	fmt.Printf("Opening browser for authentication...\n")
	fmt.Printf("If the browser doesn't open, visit:\n%s\n\n", authURL)
	openBrowserFunc(authURL)

	// Wait for callback
	fmt.Println("Waiting for authorization callback...")
	var code string
	select {
	case code = <-codeCh:
	case err := <-errCh:
		return err
	case <-time.After(5 * time.Minute):
		return fmt.Errorf("authorization timed out (5 minutes)")
	}

	// Exchange code for tokens.
	// Build the request explicitly (rather than http.PostForm) so we
	// can honour ClientAuthMethod=basic by setting an Authorization
	// header instead of stuffing the secret into the form body.
	fmt.Println("Exchanging authorization code for tokens...")
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequest("POST", oauth.TokenURL, nil)
	if err != nil {
		return fmt.Errorf("build token exchange request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	transport.ApplyClientAuth(req, form, transport.ClientAuthConfig{
		Method:       oauth.ClientAuthMethod,
		ClientID:     oauth.ClientID,
		ClientSecret: oauth.ClientSecret,
	})
	req.Body = io.NopCloser(strings.NewReader(form.Encode()))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("token exchange request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("token exchange failed (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return fmt.Errorf("parse token response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return fmt.Errorf("empty access_token in token response")
	}

	// Decide the stored expiry:
	//   - expires_in present                → honour it.
	//   - no expires_in but a refresh_token → probe again in 1 hour; the
	//     provider can renew silently before then.
	//   - no expires_in and no refresh_token → non-expiring, non-refreshable
	//     token (e.g. Slack without token rotation). Store 0 ("no known
	//     expiry"); the provider returns it as-is and relies on the upstream
	//     401 path to surface a real revocation. (ADR-0003.)
	var expiresAt int64
	switch {
	case tokenResp.ExpiresIn > 0:
		expiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).Unix()
	case tokenResp.RefreshToken != "":
		expiresAt = time.Now().Add(1 * time.Hour).Unix()
	}

	tokens := &transport.StoredTokens{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		TokenType:    tokenResp.TokenType,
		ExpiresAt:    expiresAt,
	}

	if err := transport.SaveTokens(stateDir, tokens); err != nil {
		return fmt.Errorf("save tokens: %w", err)
	}

	fmt.Printf("Login successful. Tokens saved to %s/tokens.json\n", stateDir)
	if tokenResp.RefreshToken != "" {
		fmt.Println("Refresh token stored — access tokens will be renewed automatically.")
	} else {
		fmt.Println("Warning: no refresh token received. You may need to --login again when the token expires.")
	}

	return nil
}

// generateCodeVerifier creates a random PKCE code verifier (43-128 chars).
func generateCodeVerifier() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// computeCodeChallenge computes S256 code challenge from verifier.
func computeCodeChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// generateState creates a random state parameter for CSRF protection.
func generateState() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// openBrowserFunc is the function used to open a URL in the browser.
// Replaced in tests to prevent actual browser launches.
// Set MCP_GUARDIAN_NO_BROWSER=1 to suppress browser opening (for CI/testing).
var openBrowserFunc = openBrowser

// openBrowser opens a URL in the default browser.
func openBrowser(url string) {
	if os.Getenv("MCP_GUARDIAN_NO_BROWSER") != "" {
		return
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to open browser: %v\n", err)
	}
}
