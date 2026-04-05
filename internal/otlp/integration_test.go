//go:build integration

package otlp_test

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestIntegration_OTLPCollector runs mcp-guardian against a real OTel Collector.
// Requires: OTEL_ENDPOINT and OTEL_OUTPUT_DIR env vars (set by scripts/otel-up.sh).
func TestIntegration_OTLPCollector(t *testing.T) {
	endpoint := os.Getenv("OTEL_ENDPOINT")
	outputDir := os.Getenv("OTEL_OUTPUT_DIR")
	if endpoint == "" || outputDir == "" {
		t.Skip("OTEL_ENDPOINT and OTEL_OUTPUT_DIR not set; run: eval \"$(scripts/otel-up.sh)\"")
	}

	// Build mcp-guardian binary
	binary := filepath.Join(t.TempDir(), "mcp-guardian")
	buildCmd := exec.Command("go", "build", "-o", binary, ".")
	buildCmd.Dir = projectRoot(t)
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}

	// Write mock MCP server
	dir := t.TempDir()
	serverPath := filepath.Join(dir, "mock_mcp_server.sh")
	os.WriteFile(serverPath, []byte(mockServer), 0755)

	stateDir := filepath.Join(dir, "state")

	// Run mcp-guardian with OTLP pointing to the real collector
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"integration-test","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","id":"2","method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":"3","method":"tools/call","params":{"name":"echo_tool","arguments":{"msg":"hello from integration test"}}}`,
		`{"jsonrpc":"2.0","id":"4","method":"tools/call","params":{"name":"fail_tool","arguments":{}}}`,
	}, "\n") + "\n"

	cmd := exec.Command(binary,
		"--state-dir", stateDir,
		"--enforcement", "advisory",
		"--otlp-endpoint", endpoint,
		"--otlp-batch-size", "1",
		"--", "sh", serverPath,
	)
	cmd.Stdin = strings.NewReader(input)
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	// Drain stdout
	scanner := bufio.NewScanner(stdout)
	var responses int
	done := make(chan struct{})
	go func() {
		for scanner.Scan() {
			responses++
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		cmd.Process.Kill()
		t.Fatal("proxy timed out")
	}
	cmd.Wait()

	if responses < 4 {
		t.Fatalf("expected at least 4 responses, got %d", responses)
	}

	// Wait for collector to flush to files
	time.Sleep(3 * time.Second)

	// ── Verify logs output ──────────────────────────────────────────────
	logsPath := filepath.Join(outputDir, "logs.jsonl")
	logRecords := readJSONLFile(t, logsPath)
	if len(logRecords) == 0 {
		t.Fatal("no log records found in collector output")
	}

	// Find our records by looking for mcp.tool.name attributes
	var foundEchoLog, foundFailLog bool
	for _, record := range logRecords {
		toolName := findNestedAttr(record, "mcp.tool.name")
		if toolName == "echo_tool" {
			foundEchoLog = true
		}
		if toolName == "fail_tool" {
			foundFailLog = true
		}
	}
	if !foundEchoLog {
		t.Error("echo_tool log record not found in collector output")
	}
	if !foundFailLog {
		t.Error("fail_tool log record not found in collector output")
	}

	// ── Verify traces output ────────────────────────────────────────────
	tracesPath := filepath.Join(outputDir, "traces.jsonl")
	traceRecords := readJSONLFile(t, tracesPath)
	if len(traceRecords) == 0 {
		t.Fatal("no trace records found in collector output")
	}

	var foundEchoSpan, foundFailSpan bool
	for _, record := range traceRecords {
		spanName := findNestedSpanName(record)
		if spanName == "echo_tool" {
			foundEchoSpan = true
		}
		if spanName == "fail_tool" {
			foundFailSpan = true
		}
	}
	if !foundEchoSpan {
		t.Error("echo_tool span not found in collector output")
	}
	if !foundFailSpan {
		t.Error("fail_tool span not found in collector output")
	}

	t.Logf("Integration test passed: %d log records, %d trace records in collector output",
		len(logRecords), len(traceRecords))
}

const mockServer = `#!/bin/sh
while IFS= read -r line; do
  id=$(echo "$line" | sed -n 's/.*"id" *: *"\([^"]*\)".*/\1/p')
  method=$(echo "$line" | sed -n 's/.*"method" *: *"\([^"]*\)".*/\1/p')
  case "$method" in
    initialize)
      printf '{"jsonrpc":"2.0","id":"%s","result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"serverInfo":{"name":"mock","version":"0.1"}}}\n' "$id"
      ;;
    tools/list)
      printf '{"jsonrpc":"2.0","id":"%s","result":{"tools":[{"name":"echo_tool","description":"echo","inputSchema":{"type":"object","properties":{"msg":{"type":"string"}},"required":["msg"]}},{"name":"fail_tool","description":"always fails","inputSchema":{"type":"object","properties":{}}}]}}\n' "$id"
      ;;
    tools/call)
      tool=$(echo "$line" | sed -n 's/.*"name" *: *"\([^"]*\)".*/\1/p')
      case "$tool" in
        echo_tool)
          printf '{"jsonrpc":"2.0","id":"%s","result":{"content":[{"type":"text","text":"ok"}]}}\n' "$id"
          ;;
        fail_tool)
          printf '{"jsonrpc":"2.0","id":"%s","result":{"content":[{"type":"text","text":"Permission denied: EACCES"}],"isError":true}}\n' "$id"
          ;;
      esac
      ;;
  esac
done
`

func projectRoot(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find project root")
		}
		dir = parent
	}
}

// readJSONLFile reads a JSONL file and returns parsed records.
// OTel Collector file exporter wraps each batch in a JSON object.
func readJSONLFile(t *testing.T, path string) []map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Logf("could not read %s: %v", path, err)
		return nil
	}

	var records []map[string]interface{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var obj map[string]interface{}
		if json.Unmarshal([]byte(line), &obj) == nil {
			records = append(records, obj)
		}
	}
	return records
}

// findNestedAttr searches deeply for an attribute value in OTel Collector file output.
func findNestedAttr(record map[string]interface{}, key string) string {
	data, _ := json.Marshal(record)
	s := string(data)

	// Look for pattern: "key":"mcp.tool.name","value":{"stringValue":"echo_tool"}
	target := `"key":"` + key + `"`
	idx := strings.Index(s, target)
	if idx < 0 {
		return ""
	}
	// Find stringValue after the key
	rest := s[idx:]
	svIdx := strings.Index(rest, `"stringValue":"`)
	if svIdx < 0 {
		return ""
	}
	start := svIdx + len(`"stringValue":"`)
	rest = rest[start:]
	endIdx := strings.Index(rest, `"`)
	if endIdx < 0 {
		return ""
	}
	return rest[:endIdx]
}

// findNestedSpanName searches for span name in OTel Collector file output.
func findNestedSpanName(record map[string]interface{}) string {
	data, _ := json.Marshal(record)
	s := string(data)

	// Look for "name":"<tool>" in span objects
	// The collector file exporter nests: resourceSpans[].scopeSpans[].spans[].name
	idx := strings.Index(s, `"spans":[`)
	if idx < 0 {
		return ""
	}
	rest := s[idx:]
	nameIdx := strings.Index(rest, `"name":"`)
	if nameIdx < 0 {
		return ""
	}
	start := nameIdx + len(`"name":"`)
	rest = rest[start:]
	endIdx := strings.Index(rest, `"`)
	if endIdx < 0 {
		return ""
	}
	return rest[:endIdx]
}
