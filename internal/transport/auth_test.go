package transport

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestOAuth2Provider_FetchesToken(t *testing.T) {
	var requestCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)

		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("grant_type") != "client_credentials" {
			t.Errorf("grant_type=%s, want client_credentials", r.Form.Get("grant_type"))
		}
		if r.Form.Get("client_id") != "test-id" {
			t.Errorf("client_id=%s, want test-id", r.Form.Get("client_id"))
		}
		if r.Form.Get("client_secret") != "test-secret" {
			t.Errorf("client_secret=%s, want test-secret", r.Form.Get("client_secret"))
		}
		if r.Form.Get("scope") != "read write" {
			t.Errorf("scope=%q, want 'read write'", r.Form.Get("scope"))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "tok-abc123",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	defer srv.Close()

	p := NewOAuth2Provider(OAuth2Config{
		TokenURL:     srv.URL,
		ClientID:     "test-id",
		ClientSecret: "test-secret",
		Scopes:       []string{"read", "write"},
	})

	token, err := p.Token()
	if err != nil {
		t.Fatalf("Token(): %v", err)
	}
	if token != "tok-abc123" {
		t.Errorf("token=%q, want tok-abc123", token)
	}

	// Second call should use cache (no new HTTP request)
	token2, err := p.Token()
	if err != nil {
		t.Fatalf("Token() cached: %v", err)
	}
	if token2 != "tok-abc123" {
		t.Errorf("cached token=%q, want tok-abc123", token2)
	}
	if atomic.LoadInt32(&requestCount) != 1 {
		t.Errorf("expected 1 request (cached), got %d", requestCount)
	}
}

func TestOAuth2Provider_Invalidate(t *testing.T) {
	var requestCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&requestCount, 1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": fmt.Sprintf("tok-%d", n),
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	defer srv.Close()

	p := NewOAuth2Provider(OAuth2Config{
		TokenURL:     srv.URL,
		ClientID:     "id",
		ClientSecret: "secret",
	})

	tok1, _ := p.Token()
	if tok1 != "tok-1" {
		t.Errorf("tok1=%q, want tok-1", tok1)
	}

	p.Invalidate()

	tok2, _ := p.Token()
	if tok2 != "tok-2" {
		t.Errorf("tok2=%q, want tok-2", tok2)
	}
	if atomic.LoadInt32(&requestCount) != 2 {
		t.Errorf("expected 2 requests after invalidation, got %d", requestCount)
	}
}

func TestOAuth2Provider_ErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"invalid_client"}`)
	}))
	defer srv.Close()

	p := NewOAuth2Provider(OAuth2Config{
		TokenURL:     srv.URL,
		ClientID:     "bad",
		ClientSecret: "bad",
	})

	_, err := p.Token()
	if err == nil {
		t.Fatal("expected error for bad credentials")
	}
}

func TestOAuth2Provider_EmptyToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "",
			"token_type":   "Bearer",
		})
	}))
	defer srv.Close()

	p := NewOAuth2Provider(OAuth2Config{
		TokenURL:     srv.URL,
		ClientID:     "id",
		ClientSecret: "secret",
	})

	_, err := p.Token()
	if err == nil {
		t.Fatal("expected error for empty access_token")
	}
}

func TestCommandProvider(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	p := NewCommandProvider("echo", []string{"my-token-123"}, 5*time.Minute)

	token, err := p.Token()
	if err != nil {
		t.Fatalf("Token(): %v", err)
	}
	if token != "my-token-123" {
		t.Errorf("token=%q, want my-token-123", token)
	}
}

func TestCommandProvider_Invalidate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	p := NewCommandProvider("echo", []string{"tok"}, 1*time.Hour)

	tok1, _ := p.Token()
	if tok1 != "tok" {
		t.Errorf("tok1=%q", tok1)
	}

	p.Invalidate()

	// After invalidate, should re-execute command
	tok2, _ := p.Token()
	if tok2 != "tok" {
		t.Errorf("tok2=%q", tok2)
	}
}

func TestCommandProvider_BadCommand(t *testing.T) {
	p := NewCommandProvider("nonexistent-cmd-xyz", nil, 0)

	_, err := p.Token()
	if err == nil {
		t.Fatal("expected error for nonexistent command")
	}
}

func TestCommandProvider_EmptyOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	p := NewCommandProvider("true", nil, 0)

	_, err := p.Token()
	if err == nil {
		t.Fatal("expected error for empty output")
	}
}
