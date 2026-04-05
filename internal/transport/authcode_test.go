package transport

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestStoredTokenProvider_LoadsAndReturns(t *testing.T) {
	dir := t.TempDir()

	// Save tokens
	tokens := &StoredTokens{
		AccessToken:  "stored-access-tok",
		RefreshToken: "stored-refresh-tok",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(1 * time.Hour).Unix(),
	}
	if err := SaveTokens(dir, tokens); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}

	// Verify file exists with correct permissions
	info, err := os.Stat(filepath.Join(dir, "tokens.json"))
	if err != nil {
		t.Fatalf("tokens.json not found: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("tokens.json permissions=%o, want 0600", info.Mode().Perm())
	}

	provider, err := NewStoredTokenProvider(StoredTokenConfig{
		StateDir: dir,
		TokenURL: "http://unused",
		ClientID: "unused",
	})
	if err != nil {
		t.Fatalf("NewStoredTokenProvider: %v", err)
	}

	tok, err := provider.Token()
	if err != nil {
		t.Fatalf("Token(): %v", err)
	}
	if tok != "stored-access-tok" {
		t.Errorf("token=%q, want stored-access-tok", tok)
	}
}

func TestStoredTokenProvider_RefreshesExpired(t *testing.T) {
	dir := t.TempDir()

	var refreshCount int32
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("grant_type") != "refresh_token" {
			t.Errorf("grant_type=%q, want refresh_token", r.Form.Get("grant_type"))
		}
		if r.Form.Get("refresh_token") != "my-refresh-tok" {
			t.Errorf("refresh_token=%q", r.Form.Get("refresh_token"))
		}
		atomic.AddInt32(&refreshCount, 1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "refreshed-tok",
			"refresh_token": "new-refresh-tok",
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	}))
	defer tokenSrv.Close()

	// Save expired tokens
	tokens := &StoredTokens{
		AccessToken:  "expired-tok",
		RefreshToken: "my-refresh-tok",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(-1 * time.Hour).Unix(), // expired
	}
	SaveTokens(dir, tokens)

	provider, err := NewStoredTokenProvider(StoredTokenConfig{
		StateDir: dir,
		TokenURL: tokenSrv.URL,
		ClientID: "test-client",
	})
	if err != nil {
		t.Fatalf("NewStoredTokenProvider: %v", err)
	}

	tok, err := provider.Token()
	if err != nil {
		t.Fatalf("Token(): %v", err)
	}
	if tok != "refreshed-tok" {
		t.Errorf("token=%q, want refreshed-tok", tok)
	}

	// Verify refreshed tokens were saved to disk
	saved, _ := loadTokens(dir)
	if saved.AccessToken != "refreshed-tok" {
		t.Errorf("saved access_token=%q", saved.AccessToken)
	}
	if saved.RefreshToken != "new-refresh-tok" {
		t.Errorf("saved refresh_token=%q", saved.RefreshToken)
	}
}

func TestStoredTokenProvider_InvalidateForcesRefresh(t *testing.T) {
	dir := t.TempDir()

	var callCount int32
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&callCount, 1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": fmt.Sprintf("tok-%d", n),
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	defer tokenSrv.Close()

	tokens := &StoredTokens{
		AccessToken:  "initial",
		RefreshToken: "refresh",
		ExpiresAt:    time.Now().Add(1 * time.Hour).Unix(),
	}
	SaveTokens(dir, tokens)

	provider, _ := NewStoredTokenProvider(StoredTokenConfig{
		StateDir: dir,
		TokenURL: tokenSrv.URL,
		ClientID: "test",
	})

	// First call returns cached
	tok1, _ := provider.Token()
	if tok1 != "initial" {
		t.Errorf("tok1=%q, want initial", tok1)
	}

	// Invalidate forces refresh
	provider.Invalidate()
	tok2, _ := provider.Token()
	if tok2 != "tok-1" {
		t.Errorf("tok2=%q, want tok-1", tok2)
	}
}

func TestStoredTokenProvider_NoTokensFile(t *testing.T) {
	dir := t.TempDir()
	_, err := NewStoredTokenProvider(StoredTokenConfig{
		StateDir: dir,
		TokenURL: "http://unused",
		ClientID: "unused",
	})
	if err == nil {
		t.Fatal("expected error when no tokens.json exists")
	}
}

func TestStoredTokenProvider_LoadsFromDiscoveryCache(t *testing.T) {
	dir := t.TempDir()

	// Save tokens
	SaveTokens(dir, &StoredTokens{
		AccessToken:  "disc-tok",
		RefreshToken: "disc-refresh",
		ExpiresAt:    time.Now().Add(1 * time.Hour).Unix(),
	})

	// Save discovery cache (simulating what --login writes)
	discData := `{"token_url":"http://disc-token-url","client_id":"disc-client","client_secret":"disc-secret"}`
	os.WriteFile(filepath.Join(dir, "oauth2-discovery.json"), []byte(discData), 0600)

	// Create provider with empty tokenURL/clientID — should load from discovery
	provider, err := NewStoredTokenProvider(StoredTokenConfig{
		StateDir: dir,
		// TokenURL and ClientID intentionally empty
	})
	if err != nil {
		t.Fatalf("NewStoredTokenProvider: %v", err)
	}

	tok, _ := provider.Token()
	if tok != "disc-tok" {
		t.Errorf("token=%q, want disc-tok", tok)
	}
}

func TestStoredTokenProvider_NoDiscoveryNoTokenURL(t *testing.T) {
	dir := t.TempDir()

	SaveTokens(dir, &StoredTokens{
		AccessToken: "tok",
		ExpiresAt:   time.Now().Add(1 * time.Hour).Unix(),
	})

	// No discovery cache and no tokenURL — should error
	_, err := NewStoredTokenProvider(StoredTokenConfig{
		StateDir: dir,
	})
	if err == nil {
		t.Fatal("expected error when no tokenURL and no discovery cache")
	}
}

func TestStoredTokenProvider_PreservesRefreshToken(t *testing.T) {
	dir := t.TempDir()

	// Server returns no refresh_token in response
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "new-access",
			"token_type":   "Bearer",
			"expires_in":   3600,
			// no refresh_token
		})
	}))
	defer tokenSrv.Close()

	tokens := &StoredTokens{
		AccessToken:  "old",
		RefreshToken: "original-refresh",
		ExpiresAt:    time.Now().Add(-1 * time.Hour).Unix(),
	}
	SaveTokens(dir, tokens)

	provider, _ := NewStoredTokenProvider(StoredTokenConfig{
		StateDir: dir,
		TokenURL: tokenSrv.URL,
		ClientID: "test",
	})

	provider.Token()

	// Original refresh token should be preserved
	saved, _ := loadTokens(dir)
	if saved.RefreshToken != "original-refresh" {
		t.Errorf("refresh_token=%q, want original-refresh (should be preserved)", saved.RefreshToken)
	}
}
