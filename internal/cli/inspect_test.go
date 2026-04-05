package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func buildBinaryForInspect(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "mcp-guardian")
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find project root")
		}
		dir = parent
	}
	cmd := exec.Command("go", "build", "-o", binary, ".")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return binary
}

func newMockMCPServerForInspect(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			w.WriteHeader(http.StatusOK)
			return
		}
		body := make([]byte, r.ContentLength)
		r.Body.Read(body)
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		json.Unmarshal(body, &msg)
		id := string(msg.ID)

		var resp string
		switch msg.Method {
		case "initialize":
			resp = fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2024-11-05","serverInfo":{"name":"test-server","version":"2.0"},"capabilities":{"tools":{}}}}`, id)
		case "tools/list":
			resp = fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{"tools":[{"name":"read_file","description":"Read a file","inputSchema":{"type":"object","properties":{"path":{"type":"string","description":"File path"}},"required":["path"]}},{"name":"write_file","description":"Write a file","inputSchema":{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}}]}}`, id)
		default:
			resp = fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{}}`, id)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, resp)
	}))
}

func TestInspect_ShowsServerAndTools(t *testing.T) {
	srv := newMockMCPServerForInspect(t)
	defer srv.Close()

	dir := t.TempDir()
	profilePath := filepath.Join(dir, "test.json")
	profile := fmt.Sprintf(`{
		"name": "test",
		"upstream": {"transport": "sse", "url": %q},
		"stateDir": %q
	}`, srv.URL, filepath.Join(dir, "state"))
	os.WriteFile(profilePath, []byte(profile), 0644)

	binary := buildBinaryForInspect(t)
	out, err := exec.Command(binary, "--profile", profilePath, "--inspect").CombinedOutput()
	if err != nil {
		t.Fatalf("--inspect failed: %v\n%s", err, out)
	}

	output := string(out)

	// Check server info
	if !strings.Contains(output, "test-server") {
		t.Errorf("expected server name, got: %s", output)
	}
	if !strings.Contains(output, "2.0") {
		t.Errorf("expected server version, got: %s", output)
	}
	if !strings.Contains(output, "2024-11-05") {
		t.Errorf("expected protocol version, got: %s", output)
	}

	// Check tools
	if !strings.Contains(output, "Tools: 2") {
		t.Errorf("expected 'Tools: 2', got: %s", output)
	}
	if !strings.Contains(output, "read_file") {
		t.Errorf("expected read_file tool, got: %s", output)
	}
	if !strings.Contains(output, "write_file") {
		t.Errorf("expected write_file tool, got: %s", output)
	}

	// Check parameters
	if !strings.Contains(output, "path") {
		t.Errorf("expected 'path' parameter, got: %s", output)
	}
	if !strings.Contains(output, "(required)") {
		t.Errorf("expected '(required)' marker, got: %s", output)
	}
	if !strings.Contains(output, "File path") {
		t.Errorf("expected parameter description, got: %s", output)
	}
}

func TestInspect_NoProfile(t *testing.T) {
	binary := buildBinaryForInspect(t)
	out, err := exec.Command(binary, "--inspect").CombinedOutput()
	if err == nil {
		t.Fatal("expected error without --profile")
	}
	if !strings.Contains(string(out), "--profile is required") {
		t.Errorf("expected profile required message, got: %s", out)
	}
}

func TestInspect_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		r.Body.Read(body)
		var msg struct {
			ID json.RawMessage `json:"id"`
		}
		json.Unmarshal(body, &msg)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"error":{"code":-32600,"message":"server unavailable"}}`, string(msg.ID))
	}))
	defer srv.Close()

	dir := t.TempDir()
	profilePath := filepath.Join(dir, "test.json")
	profile := fmt.Sprintf(`{
		"name": "test",
		"upstream": {"transport": "sse", "url": %q},
		"stateDir": %q
	}`, srv.URL, filepath.Join(dir, "state"))
	os.WriteFile(profilePath, []byte(profile), 0644)

	binary := buildBinaryForInspect(t)
	out, err := exec.Command(binary, "--profile", profilePath, "--inspect").CombinedOutput()
	if err == nil {
		t.Fatal("expected error for server error response")
	}
	if !strings.Contains(string(out), "server unavailable") {
		t.Errorf("expected error message, got: %s", out)
	}
}

func TestInspect_StdioTransport(t *testing.T) {
	dir := t.TempDir()

	// Create a mock stdio MCP server
	serverScript := `#!/bin/sh
while IFS= read -r line; do
  id=$(echo "$line" | sed -n 's/.*"id" *: *"\([^"]*\)".*/\1/p')
  method=$(echo "$line" | sed -n 's/.*"method" *: *"\([^"]*\)".*/\1/p')
  case "$method" in
    initialize)
      printf '{"jsonrpc":"2.0","id":"%s","result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"serverInfo":{"name":"stdio-mock","version":"0.1"}}}\n' "$id"
      ;;
    tools/list)
      printf '{"jsonrpc":"2.0","id":"%s","result":{"tools":[{"name":"hello","description":"Say hello","inputSchema":{"type":"object","properties":{}}}]}}\n' "$id"
      ;;
  esac
done
`
	serverPath := filepath.Join(dir, "server.sh")
	os.WriteFile(serverPath, []byte(serverScript), 0755)

	profilePath := filepath.Join(dir, "test.json")
	profile := fmt.Sprintf(`{
		"name": "stdio-test",
		"upstream": {"command": "sh", "args": [%q]},
		"stateDir": %q
	}`, serverPath, filepath.Join(dir, "state"))
	os.WriteFile(profilePath, []byte(profile), 0644)

	binary := buildBinaryForInspect(t)
	out, err := exec.Command(binary, "--profile", profilePath, "--inspect").CombinedOutput()
	if err != nil {
		t.Fatalf("--inspect failed: %v\n%s", err, out)
	}

	output := string(out)
	if !strings.Contains(output, "stdio-mock") {
		t.Errorf("expected 'stdio-mock', got: %s", output)
	}
	if !strings.Contains(output, "Tools: 1") {
		t.Errorf("expected 'Tools: 1', got: %s", output)
	}
	if !strings.Contains(output, "hello") {
		t.Errorf("expected 'hello' tool, got: %s", output)
	}
}
