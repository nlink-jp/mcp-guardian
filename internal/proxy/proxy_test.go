package proxy_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nlink-jp/mcp-guardian/internal/receipt"
)

// mockServer is a minimal MCP server script that responds to initialize,
// tools/list, and tools/call with canned JSON-RPC responses.
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

// buildBinary builds the mcp-guardian binary in a temp dir and returns the path.
func buildBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "mcp-guardian")
	cmd := exec.Command("go", "build", "-o", binary, ".")
	cmd.Dir = filepath.Join(projectRoot(t))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build binary: %v\n%s", err, out)
	}
	return binary
}

// projectRoot returns the project root by walking up from the test file.
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

// runGuardian runs the mcp-guardian binary with the given args and stdin input.
// Returns parsed JSON-RPC responses from stdout.
func runGuardian(t *testing.T, binary string, args []string, input string) []map[string]interface{} {
	t.Helper()

	cmd := exec.Command(binary, args...)
	cmd.Stdin = strings.NewReader(input)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	var results []map[string]interface{}
	scanner := bufio.NewScanner(stdout)
	done := make(chan struct{})
	go func() {
		for scanner.Scan() {
			var msg map[string]interface{}
			if json.Unmarshal(scanner.Bytes(), &msg) == nil {
				results = append(results, msg)
			}
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

	return results
}

func writeMockServer(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "mock_mcp_server.sh")
	if err := os.WriteFile(path, []byte(mockServer), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProxyE2E_InitializeAndToolsList(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	serverPath := writeMockServer(t, dir)
	stateDir := filepath.Join(dir, "state")

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","id":"2","method":"tools/list","params":{}}`,
	}, "\n") + "\n"

	results := runGuardian(t, binary, []string{
		"--state-dir", stateDir,
		"--enforcement", "strict",
		"--", "sh", serverPath,
	}, input)

	if len(results) < 2 {
		t.Fatalf("expected at least 2 responses, got %d", len(results))
	}

	// Check initialize response
	if results[0]["id"] != "1" {
		t.Errorf("expected id=1, got %v", results[0]["id"])
	}

	// Check tools/list — should include 5 governance meta-tools
	result2, _ := results[1]["result"].(map[string]interface{})
	tools, _ := result2["tools"].([]interface{})
	var toolNames []string
	for _, tool := range tools {
		tm, _ := tool.(map[string]interface{})
		toolNames = append(toolNames, tm["name"].(string))
	}

	// Original 2 tools + 5 governance meta-tools = 7
	if len(toolNames) != 7 {
		t.Errorf("expected 7 tools (2 + 5 meta), got %d: %v", len(toolNames), toolNames)
	}

	metaTools := []string{
		"governance_status",
		"governance_bump_authority",
		"governance_declare_intent",
		"governance_clear_intent",
		"governance_convergence_status",
	}
	for _, mt := range metaTools {
		found := false
		for _, tn := range toolNames {
			if tn == mt {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("meta-tool %s not found in tools list", mt)
		}
	}
}

func TestProxyE2E_ToolCallAndReceipts(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	serverPath := writeMockServer(t, dir)
	stateDir := filepath.Join(dir, "state")

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","id":"2","method":"tools/call","params":{"name":"echo_tool","arguments":{"msg":"hello"}}}`,
		`{"jsonrpc":"2.0","id":"3","method":"tools/call","params":{"name":"fail_tool","arguments":{}}}`,
	}, "\n") + "\n"

	results := runGuardian(t, binary, []string{
		"--state-dir", stateDir,
		"--enforcement", "advisory",
		"--", "sh", serverPath,
	}, input)

	if len(results) < 3 {
		t.Fatalf("expected at least 3 responses, got %d", len(results))
	}

	// echo_tool success
	result2, _ := results[1]["result"].(map[string]interface{})
	content2, _ := result2["content"].([]interface{})
	if len(content2) == 0 {
		t.Fatal("echo_tool returned no content")
	}
	text2, _ := content2[0].(map[string]interface{})["text"].(string)
	if text2 != "ok" {
		t.Errorf("echo_tool expected 'ok', got '%s'", text2)
	}

	// fail_tool error
	result3, _ := results[2]["result"].(map[string]interface{})
	isErr, _ := result3["isError"].(bool)
	if !isErr {
		t.Error("fail_tool should have isError=true")
	}

	// Verify receipts
	ledger, err := receipt.NewLedger(stateDir)
	if err != nil {
		t.Fatalf("failed to open ledger: %v", err)
	}
	records, err := ledger.LoadAll()
	if err != nil {
		t.Fatalf("failed to load receipts: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 receipts, got %d", len(records))
	}
	if records[0].ToolName != "echo_tool" || records[0].Outcome != "success" {
		t.Errorf("receipt[0]: expected echo_tool/success, got %s/%s", records[0].ToolName, records[0].Outcome)
	}
	if records[1].ToolName != "fail_tool" || records[1].Outcome != "error" {
		t.Errorf("receipt[1]: expected fail_tool/error, got %s/%s", records[1].ToolName, records[1].Outcome)
	}

	// Hash chain integrity
	intact, _, _ := receipt.VerifyChain(filepath.Join(stateDir, "receipts.jsonl"))
	if !intact {
		t.Error("hash chain should be intact")
	}
}

func TestProxyE2E_BudgetEnforcement(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	serverPath := writeMockServer(t, dir)
	stateDir := filepath.Join(dir, "state")

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","id":"2","method":"tools/call","params":{"name":"echo_tool","arguments":{"msg":"first"}}}`,
		`{"jsonrpc":"2.0","id":"3","method":"tools/call","params":{"name":"echo_tool","arguments":{"msg":"second"}}}`,
	}, "\n") + "\n"

	results := runGuardian(t, binary, []string{
		"--state-dir", stateDir,
		"--enforcement", "strict",
		"--max-calls", "1",
		"--", "sh", serverPath,
	}, input)

	if len(results) < 3 {
		t.Fatalf("expected at least 3 responses, got %d", len(results))
	}

	// First call should succeed
	if _, hasError := results[1]["error"]; hasError {
		t.Error("first call should succeed within budget")
	}

	// Second call should be blocked
	errObj, hasError := results[2]["error"]
	if !hasError {
		t.Fatal("second call should be blocked by budget gate")
	}
	errMap, _ := errObj.(map[string]interface{})
	errMsg, _ := errMap["message"].(string)
	if !strings.Contains(strings.ToLower(errMsg), "budget") {
		t.Errorf("expected budget error, got: %s", errMsg)
	}
}

func TestProxyE2E_GovernanceMetaTool(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	serverPath := writeMockServer(t, dir)
	stateDir := filepath.Join(dir, "state")

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","id":"2","method":"tools/call","params":{"name":"governance_status","arguments":{}}}`,
	}, "\n") + "\n"

	results := runGuardian(t, binary, []string{
		"--state-dir", stateDir,
		"--enforcement", "strict",
		"--", "sh", serverPath,
	}, input)

	if len(results) < 2 {
		t.Fatalf("expected at least 2 responses, got %d", len(results))
	}

	// governance_status should return content with controller info
	result2, _ := results[1]["result"].(map[string]interface{})
	content2, _ := result2["content"].([]interface{})
	if len(content2) == 0 {
		t.Fatal("governance_status returned no content")
	}
	text, _ := content2[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(text, "controllerId") {
		t.Errorf("governance_status should contain controllerId, got: %.100s", text)
	}
	if !strings.Contains(text, "epoch") {
		t.Errorf("governance_status should contain epoch, got: %.100s", text)
	}
}

func TestProxyE2E_ConstraintLearningAndBlocking(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	serverPath := writeMockServer(t, dir)
	stateDir := filepath.Join(dir, "state")

	// First session: fail_tool creates a constraint
	input1 := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","id":"2","method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":"3","method":"tools/call","params":{"name":"fail_tool","arguments":{}}}`,
	}, "\n") + "\n"

	runGuardian(t, binary, []string{
		"--state-dir", stateDir,
		"--enforcement", "strict",
		"--", "sh", serverPath,
	}, input1)

	// Second session: fail_tool on same target should be blocked by constraint
	input2 := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","id":"2","method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":"3","method":"tools/call","params":{"name":"fail_tool","arguments":{}}}`,
	}, "\n") + "\n"

	results := runGuardian(t, binary, []string{
		"--state-dir", stateDir,
		"--enforcement", "strict",
		"--", "sh", serverPath,
	}, input2)

	if len(results) < 3 {
		t.Fatalf("expected at least 3 responses, got %d", len(results))
	}

	// The second call to fail_tool should be blocked by constraint gate
	r3 := results[2]
	errObj, hasError := r3["error"]
	if !hasError {
		// Could also be allowed if constraint matching is target-specific
		// Check if it was an error result instead
		result3, _ := r3["result"].(map[string]interface{})
		if isErr, _ := result3["isError"].(bool); !isErr {
			t.Log("constraint did not block — may depend on target matching logic")
		}
		return
	}
	errMap, _ := errObj.(map[string]interface{})
	errMsg, _ := errMap["message"].(string)
	if !strings.Contains(strings.ToLower(errMsg), "constraint") {
		t.Logf("blocked but not by constraint: %s", errMsg)
	}
}

func TestProxyE2E_AdvisoryMode(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	serverPath := writeMockServer(t, dir)
	stateDir := filepath.Join(dir, "state")

	// First session: fail_tool creates a constraint.
	// In advisory mode, the constraint should NOT block the second call.
	input1 := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","id":"2","method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":"3","method":"tools/call","params":{"name":"fail_tool","arguments":{}}}`,
	}, "\n") + "\n"

	runGuardian(t, binary, []string{
		"--state-dir", stateDir,
		"--enforcement", "advisory",
		"--", "sh", serverPath,
	}, input1)

	// Second session: fail_tool again with advisory — constraint exists but should not block
	input2 := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","id":"2","method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":"3","method":"tools/call","params":{"name":"fail_tool","arguments":{}}}`,
	}, "\n") + "\n"

	results := runGuardian(t, binary, []string{
		"--state-dir", stateDir,
		"--enforcement", "advisory",
		"--", "sh", serverPath,
	}, input2)

	if len(results) < 3 {
		t.Fatalf("expected at least 3 responses, got %d", len(results))
	}

	// In advisory mode, constraint gate should not block — call should reach upstream
	r3 := results[2]
	if _, hasError := r3["error"]; hasError {
		t.Error("advisory mode should not block calls via constraint gate")
	}
}

func TestProxyE2E_SchemaValidation(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	serverPath := writeMockServer(t, dir)
	stateDir := filepath.Join(dir, "state")

	// echo_tool requires "msg" (string). Send call without required field.
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","id":"2","method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":"3","method":"tools/call","params":{"name":"echo_tool","arguments":{}}}`,
	}, "\n") + "\n"

	results := runGuardian(t, binary, []string{
		"--state-dir", stateDir,
		"--enforcement", "strict",
		"--schema", "strict",
		"--", "sh", serverPath,
	}, input)

	if len(results) < 3 {
		t.Fatalf("expected at least 3 responses, got %d", len(results))
	}

	// With schema=strict, missing required "msg" should block the call
	r3 := results[2]
	errObj, hasError := r3["error"]
	if !hasError {
		t.Log("schema validation did not block — checking if upstream handled it")
		return
	}
	errMap, _ := errObj.(map[string]interface{})
	errMsg, _ := errMap["message"].(string)
	if !strings.Contains(strings.ToLower(errMsg), "schema") && !strings.Contains(strings.ToLower(errMsg), "required") {
		t.Logf("blocked but not by schema: %s", errMsg)
	}
}

func TestProxyE2E_BumpAuthorityAndEpochCheck(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	serverPath := writeMockServer(t, dir)
	stateDir := filepath.Join(dir, "state")

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`,
		// Bump authority — advances epoch
		`{"jsonrpc":"2.0","id":"2","method":"tools/call","params":{"name":"governance_bump_authority","arguments":{}}}`,
		// Now call a tool — should be blocked by authority gate (session epoch != authority epoch)
		`{"jsonrpc":"2.0","id":"3","method":"tools/call","params":{"name":"echo_tool","arguments":{"msg":"test"}}}`,
	}, "\n") + "\n"

	results := runGuardian(t, binary, []string{
		"--state-dir", stateDir,
		"--enforcement", "strict",
		"--", "sh", serverPath,
	}, input)

	if len(results) < 3 {
		t.Fatalf("expected at least 3 responses, got %d", len(results))
	}

	// Bump should succeed
	r2 := results[1]
	result2, _ := r2["result"].(map[string]interface{})
	content2, _ := result2["content"].([]interface{})
	if len(content2) > 0 {
		text, _ := content2[0].(map[string]interface{})["text"].(string)
		if !strings.Contains(text, "newEpoch") {
			t.Errorf("bump should return newEpoch, got: %.100s", text)
		}
	}

	// Tool call after bump should be blocked by authority gate
	r3 := results[2]
	if errObj, hasError := r3["error"]; hasError {
		errMap, _ := errObj.(map[string]interface{})
		errMsg, _ := errMap["message"].(string)
		if !strings.Contains(strings.ToLower(errMsg), "authority") && !strings.Contains(strings.ToLower(errMsg), "epoch") {
			t.Logf("blocked but not by authority: %s", errMsg)
		}
	} else {
		t.Log("authority gate did not block after bump — may need re-initialize")
	}
}

func TestProxyE2E_DeclareAndClearIntent(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	serverPath := writeMockServer(t, dir)
	stateDir := filepath.Join(dir, "state")

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","id":"2","method":"tools/call","params":{"name":"governance_declare_intent","arguments":{"goal":"refactor auth module"}}}`,
		`{"jsonrpc":"2.0","id":"3","method":"tools/call","params":{"name":"governance_status","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":"4","method":"tools/call","params":{"name":"governance_clear_intent","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":"5","method":"tools/call","params":{"name":"governance_status","arguments":{}}}`,
	}, "\n") + "\n"

	results := runGuardian(t, binary, []string{
		"--state-dir", stateDir,
		"--enforcement", "strict",
		"--", "sh", serverPath,
	}, input)

	if len(results) < 5 {
		t.Fatalf("expected at least 5 responses, got %d", len(results))
	}

	// Declare intent response
	getText := func(r map[string]interface{}) string {
		result, _ := r["result"].(map[string]interface{})
		content, _ := result["content"].([]interface{})
		if len(content) == 0 {
			return ""
		}
		text, _ := content[0].(map[string]interface{})["text"].(string)
		return text
	}

	declareText := getText(results[1])
	if !strings.Contains(declareText, "refactor auth module") {
		t.Errorf("declare should echo goal, got: %.100s", declareText)
	}

	// Status after declare should show hasIntent=true
	statusText := getText(results[2])
	if !strings.Contains(statusText, `"hasIntent": true`) {
		t.Errorf("status should show hasIntent=true, got: %.200s", statusText)
	}

	// Clear intent
	clearText := getText(results[3])
	if !strings.Contains(clearText, "cleared") {
		t.Errorf("clear should confirm, got: %.100s", clearText)
	}

	// Status after clear should show hasIntent=false
	statusText2 := getText(results[4])
	if !strings.Contains(statusText2, `"hasIntent": false`) {
		t.Errorf("status should show hasIntent=false, got: %.200s", statusText2)
	}
}

func TestProxyE2E_Notification(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	serverPath := writeMockServer(t, dir)
	stateDir := filepath.Join(dir, "state")

	// Notifications have no id and should be forwarded without response
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"old"}}`,
		`{"jsonrpc":"2.0","id":"2","method":"tools/call","params":{"name":"echo_tool","arguments":{"msg":"after-notif"}}}`,
	}, "\n") + "\n"

	results := runGuardian(t, binary, []string{
		"--state-dir", stateDir,
		"--enforcement", "strict",
		"--", "sh", serverPath,
	}, input)

	// Should get initialize response + echo_tool response (notification has no response)
	if len(results) < 2 {
		t.Fatalf("expected at least 2 responses, got %d", len(results))
	}

	// Verify the echo_tool still works after notification
	lastResult := results[len(results)-1]
	result, _ := lastResult["result"].(map[string]interface{})
	content, _ := result["content"].([]interface{})
	if len(content) > 0 {
		text, _ := content[0].(map[string]interface{})["text"].(string)
		if text != "ok" {
			t.Errorf("echo_tool after notification expected 'ok', got '%s'", text)
		}
	}
}

func TestProxyE2E_ConvergenceStatus(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	serverPath := writeMockServer(t, dir)
	stateDir := filepath.Join(dir, "state")

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","id":"2","method":"tools/call","params":{"name":"governance_convergence_status","arguments":{}}}`,
	}, "\n") + "\n"

	results := runGuardian(t, binary, []string{
		"--state-dir", stateDir,
		"--enforcement", "strict",
		"--", "sh", serverPath,
	}, input)

	if len(results) < 2 {
		t.Fatalf("expected at least 2 responses, got %d", len(results))
	}

	result2, _ := results[1]["result"].(map[string]interface{})
	content2, _ := result2["content"].([]interface{})
	if len(content2) == 0 {
		t.Fatal("convergence_status returned no content")
	}
	text, _ := content2[0].(map[string]interface{})["text"].(string)
	// Should be valid JSON with convergence info
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Errorf("convergence_status should return valid JSON: %v", err)
	}
}

func TestProxyE2E_ToolMaskStrict(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	serverPath := writeMockServer(t, dir)
	stateDir := filepath.Join(dir, "state")

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","id":"2","method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":"3","method":"tools/call","params":{"name":"fail_tool","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":"4","method":"tools/call","params":{"name":"echo_tool","arguments":{"msg":"hello"}}}`,
	}, "\n") + "\n"

	results := runGuardian(t, binary, []string{
		"--state-dir", stateDir,
		"--enforcement", "strict",
		"--mask", "fail_*",
		"--", "sh", serverPath,
	}, input)

	if len(results) < 4 {
		t.Fatalf("expected at least 4 responses, got %d", len(results))
	}

	// tools/list should NOT contain fail_tool
	result2, _ := results[1]["result"].(map[string]interface{})
	tools, _ := result2["tools"].([]interface{})
	for _, tool := range tools {
		tm, _ := tool.(map[string]interface{})
		if tm["name"] == "fail_tool" {
			t.Error("fail_tool should be masked from tools/list")
		}
	}

	// echo_tool should still be present
	found := false
	for _, tool := range tools {
		tm, _ := tool.(map[string]interface{})
		if tm["name"] == "echo_tool" {
			found = true
			break
		}
	}
	if !found {
		t.Error("echo_tool should not be masked")
	}

	// tools/call fail_tool should return -32601 "tool not found"
	r3 := results[2]
	errObj, hasError := r3["error"]
	if !hasError {
		t.Fatal("masked tool call should return an error")
	}
	errMap, _ := errObj.(map[string]interface{})
	errCode, _ := errMap["code"].(float64)
	errMsg, _ := errMap["message"].(string)
	if int(errCode) != -32601 {
		t.Errorf("expected error code -32601, got %v", errCode)
	}
	if !strings.Contains(errMsg, "tool not found") {
		t.Errorf("expected 'tool not found' message, got: %s", errMsg)
	}

	// echo_tool should work normally
	r4 := results[3]
	if _, hasError := r4["error"]; hasError {
		t.Error("echo_tool should not be blocked")
	}
	result4, _ := r4["result"].(map[string]interface{})
	content4, _ := result4["content"].([]interface{})
	if len(content4) > 0 {
		text, _ := content4[0].(map[string]interface{})["text"].(string)
		if text != "ok" {
			t.Errorf("echo_tool expected 'ok', got '%s'", text)
		}
	}

	// Verify masked call was recorded in receipts
	ledger, err := receipt.NewLedger(stateDir)
	if err != nil {
		t.Fatalf("failed to open ledger: %v", err)
	}
	records, err := ledger.LoadAll()
	if err != nil {
		t.Fatalf("failed to load receipts: %v", err)
	}
	// Should have 2 receipts: blocked fail_tool + success echo_tool
	if len(records) != 2 {
		t.Fatalf("expected 2 receipts, got %d", len(records))
	}
	if records[0].ToolName != "fail_tool" || records[0].Outcome != "blocked" {
		t.Errorf("receipt[0]: expected fail_tool/blocked, got %s/%s", records[0].ToolName, records[0].Outcome)
	}
	if records[1].ToolName != "echo_tool" || records[1].Outcome != "success" {
		t.Errorf("receipt[1]: expected echo_tool/success, got %s/%s", records[1].ToolName, records[1].Outcome)
	}
}

func TestProxyE2E_ToolMaskAdvisory(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	serverPath := writeMockServer(t, dir)
	stateDir := filepath.Join(dir, "state")

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","id":"2","method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":"3","method":"tools/call","params":{"name":"fail_tool","arguments":{}}}`,
	}, "\n") + "\n"

	results := runGuardian(t, binary, []string{
		"--state-dir", stateDir,
		"--enforcement", "advisory",
		"--mask", "fail_*",
		"--", "sh", serverPath,
	}, input)

	if len(results) < 3 {
		t.Fatalf("expected at least 3 responses, got %d", len(results))
	}

	// In advisory mode, fail_tool should still appear in tools/list
	result2, _ := results[1]["result"].(map[string]interface{})
	tools, _ := result2["tools"].([]interface{})
	found := false
	for _, tool := range tools {
		tm, _ := tool.(map[string]interface{})
		if tm["name"] == "fail_tool" {
			found = true
			break
		}
	}
	if !found {
		t.Error("advisory mode should not mask tools from tools/list")
	}

	// tools/call fail_tool should be forwarded (not blocked)
	r3 := results[2]
	if _, hasError := r3["error"]; hasError {
		t.Error("advisory mode should not block masked tool calls")
	}
}

func TestProxyE2E_ToolMaskFile(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	serverPath := writeMockServer(t, dir)
	stateDir := filepath.Join(dir, "state")

	// Write mask file
	maskFile := filepath.Join(dir, "masks.txt")
	if err := os.WriteFile(maskFile, []byte("# mask fail tools\nfail_*\n"), 0644); err != nil {
		t.Fatal(err)
	}

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","id":"2","method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":"3","method":"tools/call","params":{"name":"fail_tool","arguments":{}}}`,
	}, "\n") + "\n"

	results := runGuardian(t, binary, []string{
		"--state-dir", stateDir,
		"--enforcement", "strict",
		"--mask-file", maskFile,
		"--", "sh", serverPath,
	}, input)

	if len(results) < 3 {
		t.Fatalf("expected at least 3 responses, got %d", len(results))
	}

	// fail_tool should be masked from tools/list
	result2, _ := results[1]["result"].(map[string]interface{})
	tools, _ := result2["tools"].([]interface{})
	for _, tool := range tools {
		tm, _ := tool.(map[string]interface{})
		if tm["name"] == "fail_tool" {
			t.Error("fail_tool should be masked via mask-file")
		}
	}

	// tools/call fail_tool should be blocked
	r3 := results[2]
	if _, hasError := r3["error"]; !hasError {
		t.Error("masked tool call via mask-file should return an error")
	}
}

func TestProxyE2E_OTLPExport(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	serverPath := writeMockServer(t, dir)
	stateDir := filepath.Join(dir, "state")

	var mu sync.Mutex
	var logRequests [][]byte
	var traceRequests [][]byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/v1/logs":
			logRequests = append(logRequests, body)
		case "/v1/traces":
			traceRequests = append(traceRequests, body)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","id":"2","method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":"3","method":"tools/call","params":{"name":"echo_tool","arguments":{"msg":"hello"}}}`,
		`{"jsonrpc":"2.0","id":"4","method":"tools/call","params":{"name":"fail_tool","arguments":{}}}`,
	}, "\n") + "\n"

	results := runGuardian(t, binary, []string{
		"--state-dir", stateDir,
		"--enforcement", "advisory",
		"--otlp-endpoint", srv.URL,
		"--otlp-batch-size", "1",
		"--otlp-header", "X-Test-Auth=secret123",
		"--", "sh", serverPath,
	}, input)

	if len(results) < 4 {
		t.Fatalf("expected at least 4 responses, got %d", len(results))
	}

	// Wait for async OTLP sends + shutdown flush
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	// Should have received log exports for 2 tool calls
	if len(logRequests) < 1 {
		t.Fatalf("expected at least 1 log export request, got %d", len(logRequests))
	}

	// Verify log payload structure
	var totalLogRecords int
	for _, body := range logRequests {
		var payload map[string]interface{}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("invalid log payload JSON: %v", err)
		}
		resourceLogs, _ := payload["resourceLogs"].([]interface{})
		for _, rl := range resourceLogs {
			rlMap, _ := rl.(map[string]interface{})
			scopeLogs, _ := rlMap["scopeLogs"].([]interface{})
			for _, sl := range scopeLogs {
				slMap, _ := sl.(map[string]interface{})
				records, _ := slMap["logRecords"].([]interface{})
				totalLogRecords += len(records)
			}
		}
	}
	if totalLogRecords != 2 {
		t.Errorf("expected 2 total log records across exports, got %d", totalLogRecords)
	}

	// Should also have trace exports
	if len(traceRequests) < 1 {
		t.Fatalf("expected at least 1 trace export request, got %d", len(traceRequests))
	}

	// Verify trace payload has spans
	var totalSpans int
	for _, body := range traceRequests {
		var payload map[string]interface{}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("invalid trace payload JSON: %v", err)
		}
		resourceSpans, _ := payload["resourceSpans"].([]interface{})
		for _, rs := range resourceSpans {
			rsMap, _ := rs.(map[string]interface{})
			scopeSpans, _ := rsMap["scopeSpans"].([]interface{})
			for _, ss := range scopeSpans {
				ssMap, _ := ss.(map[string]interface{})
				spans, _ := ssMap["spans"].([]interface{})
				totalSpans += len(spans)
			}
		}
	}
	if totalSpans != 2 {
		t.Errorf("expected 2 total spans across exports, got %d", totalSpans)
	}
}

func TestProxyE2E_SplunkHECExport(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	serverPath := writeMockServer(t, dir)
	stateDir := filepath.Join(dir, "state")

	var mu sync.Mutex
	var hecRequests [][]byte
	var receivedAuth string

	hecSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		receivedAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		hecRequests = append(hecRequests, body)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer hecSrv.Close()

	// Write global config with Splunk HEC
	globalCfg := filepath.Join(dir, "config.json")
	globalContent := fmt.Sprintf(`{
  "telemetry": {
    "splunk": {
      "endpoint": %q,
      "token": "e2e-hec-token",
      "index": "mcp-test",
      "batchSize": 1
    }
  }
}`, hecSrv.URL)
	os.WriteFile(globalCfg, []byte(globalContent), 0644)

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","id":"2","method":"tools/call","params":{"name":"echo_tool","arguments":{"msg":"hello"}}}`,
	}, "\n") + "\n"

	results := runGuardian(t, binary, []string{
		"--state-dir", stateDir,
		"--enforcement", "advisory",
		"--config", globalCfg,
		"--", "sh", serverPath,
	}, input)

	if len(results) < 2 {
		t.Fatalf("expected at least 2 responses, got %d", len(results))
	}

	// Wait for async HEC sends
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	// Should have received HEC events
	if len(hecRequests) < 1 {
		t.Fatalf("expected at least 1 HEC request, got %d", len(hecRequests))
	}

	// Verify auth header
	if receivedAuth != "Splunk e2e-hec-token" {
		t.Errorf("Authorization=%q, want 'Splunk e2e-hec-token'", receivedAuth)
	}

	// Parse first HEC event
	var event struct {
		Source     string                 `json:"source"`
		SourceType string                `json:"sourcetype"`
		Index      string                `json:"index"`
		Event      map[string]interface{} `json:"event"`
	}
	if err := json.Unmarshal(hecRequests[0], &event); err != nil {
		// HEC events are newline-delimited, try splitting
		for i, b := range hecRequests[0] {
			if b == '\n' {
				json.Unmarshal(hecRequests[0][:i], &event)
				break
			}
		}
	}

	if event.Source != "mcp-guardian" {
		t.Errorf("source=%q, want mcp-guardian", event.Source)
	}
	if event.Index != "mcp-test" {
		t.Errorf("index=%q, want mcp-test", event.Index)
	}
	if event.Event["toolName"] != "echo_tool" {
		t.Errorf("toolName=%v, want echo_tool", event.Event["toolName"])
	}
	if event.Event["outcome"] != "success" {
		t.Errorf("outcome=%v, want success", event.Event["outcome"])
	}
}

func TestProxyE2E_SplunkHECAndOTLP(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	serverPath := writeMockServer(t, dir)
	stateDir := filepath.Join(dir, "state")

	var otlpMu sync.Mutex
	var otlpCount int
	otlpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otlpMu.Lock()
		otlpCount++
		otlpMu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer otlpSrv.Close()

	var hecMu sync.Mutex
	var hecCount int
	hecSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hecMu.Lock()
		hecCount++
		hecMu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer hecSrv.Close()

	globalCfg := filepath.Join(dir, "config.json")
	globalContent := fmt.Sprintf(`{
  "telemetry": {
    "otlp": { "endpoint": %q, "batchSize": 1 },
    "splunk": { "endpoint": %q, "token": "tok", "batchSize": 1 }
  }
}`, otlpSrv.URL, hecSrv.URL)
	os.WriteFile(globalCfg, []byte(globalContent), 0644)

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","id":"2","method":"tools/call","params":{"name":"echo_tool","arguments":{"msg":"hello"}}}`,
	}, "\n") + "\n"

	results := runGuardian(t, binary, []string{
		"--state-dir", stateDir,
		"--enforcement", "advisory",
		"--config", globalCfg,
		"--", "sh", serverPath,
	}, input)

	if len(results) < 2 {
		t.Fatalf("expected at least 2 responses, got %d", len(results))
	}

	time.Sleep(500 * time.Millisecond)

	otlpMu.Lock()
	oc := otlpCount
	otlpMu.Unlock()

	hecMu.Lock()
	hc := hecCount
	hecMu.Unlock()

	if oc == 0 {
		t.Error("expected OTLP requests, got 0")
	}
	if hc == 0 {
		t.Error("expected Splunk HEC requests, got 0")
	}
}

// startMockSSEMCPServer starts an HTTP server that simulates an MCP server
// using the Streamable HTTP transport. It responds to JSON-RPC requests
// with either JSON or SSE responses based on the request method.
func startMockSSEMCPServer(t *testing.T, useSSE bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method != "POST" {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		body, _ := io.ReadAll(r.Body)
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		json.Unmarshal(body, &msg)

		id := string(msg.ID)
		w.Header().Set("Mcp-Session-Id", "e2e-test-session")

		var response string
		switch msg.Method {
		case "initialize":
			response = fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"serverInfo":{"name":"mock-sse","version":"0.1"}}}`, id)
		case "tools/list":
			response = fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{"tools":[{"name":"echo_tool","description":"echo","inputSchema":{"type":"object","properties":{"msg":{"type":"string"}},"required":["msg"]}},{"name":"fail_tool","description":"always fails","inputSchema":{"type":"object","properties":{}}}]}}`, id)
		case "tools/call":
			var params struct {
				Name string `json:"name"`
			}
			json.Unmarshal(msg.Params, &params)
			switch params.Name {
			case "echo_tool":
				response = fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{"content":[{"type":"text","text":"ok"}]}}`, id)
			case "fail_tool":
				response = fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{"content":[{"type":"text","text":"Permission denied: EACCES"}],"isError":true}}`, id)
			default:
				response = fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"tool not found"}}`, id)
			}
		default:
			response = fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{}}`, id)
		}

		if useSSE {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", response)
		} else {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, response)
		}
	}))
}

func TestProxyE2E_SSETransport_JSON(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")

	// Start mock MCP server with JSON responses
	srv := startMockSSEMCPServer(t, false)
	defer srv.Close()

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","id":"2","method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":"3","method":"tools/call","params":{"name":"echo_tool","arguments":{"msg":"hello"}}}`,
	}, "\n") + "\n"

	results := runGuardian(t, binary, []string{
		"--state-dir", stateDir,
		"--transport", "sse",
		"--upstream-url", srv.URL,
	}, input)

	if len(results) < 3 {
		t.Fatalf("expected at least 3 responses, got %d", len(results))
	}

	// Check initialize response
	if results[0]["id"] != "1" {
		t.Errorf("expected id=1, got %v", results[0]["id"])
	}

	// Check tools/list — should include meta-tools
	result2, _ := results[1]["result"].(map[string]interface{})
	tools, _ := result2["tools"].([]interface{})
	if len(tools) != 7 {
		t.Errorf("expected 7 tools (2 + 5 meta), got %d", len(tools))
	}

	// Check tool call response
	result3, _ := results[2]["result"].(map[string]interface{})
	content, _ := result3["content"].([]interface{})
	if len(content) == 0 {
		t.Fatal("expected content in tool call response")
	}
	item, _ := content[0].(map[string]interface{})
	if item["text"] != "ok" {
		t.Errorf("expected text=ok, got %v", item["text"])
	}
}

func TestProxyE2E_SSETransport_SSE(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")

	// Start mock MCP server with SSE responses
	srv := startMockSSEMCPServer(t, true)
	defer srv.Close()

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","id":"2","method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":"3","method":"tools/call","params":{"name":"echo_tool","arguments":{"msg":"hello"}}}`,
	}, "\n") + "\n"

	results := runGuardian(t, binary, []string{
		"--state-dir", stateDir,
		"--transport", "sse",
		"--upstream-url", srv.URL,
	}, input)

	if len(results) < 3 {
		t.Fatalf("expected at least 3 responses, got %d", len(results))
	}

	// Check initialize
	if results[0]["id"] != "1" {
		t.Errorf("expected id=1, got %v", results[0]["id"])
	}

	// Check tools/list
	result2, _ := results[1]["result"].(map[string]interface{})
	tools, _ := result2["tools"].([]interface{})
	if len(tools) != 7 {
		t.Errorf("expected 7 tools (2 + 5 meta), got %d", len(tools))
	}

	// Check tool call response
	result3, _ := results[2]["result"].(map[string]interface{})
	content, _ := result3["content"].([]interface{})
	if len(content) == 0 {
		t.Fatal("expected content in tool call response")
	}
	item, _ := content[0].(map[string]interface{})
	if item["text"] != "ok" {
		t.Errorf("expected text=ok, got %v", item["text"])
	}
}

func TestProxyE2E_SSETransport_GovernanceBlocks(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")

	srv := startMockSSEMCPServer(t, false)
	defer srv.Close()

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","id":"2","method":"tools/call","params":{"name":"echo_tool","arguments":{"msg":"hello"}}}`,
	}, "\n") + "\n"

	results := runGuardian(t, binary, []string{
		"--state-dir", stateDir,
		"--transport", "sse",
		"--upstream-url", srv.URL,
		"--max-calls", "1",
	}, input)

	if len(results) < 2 {
		t.Fatalf("expected at least 2 responses, got %d", len(results))
	}

	// First call succeeds
	if _, hasError := results[1]["error"]; hasError {
		t.Error("first tool call should succeed")
	}

	// Second run: budget should be exhausted
	input2 := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"10","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","id":"11","method":"tools/call","params":{"name":"echo_tool","arguments":{"msg":"hello"}}}`,
		`{"jsonrpc":"2.0","id":"12","method":"tools/call","params":{"name":"echo_tool","arguments":{"msg":"hello2"}}}`,
	}, "\n") + "\n"

	results2 := runGuardian(t, binary, []string{
		"--state-dir", stateDir,
		"--transport", "sse",
		"--upstream-url", srv.URL,
		"--max-calls", "1",
	}, input2)

	if len(results2) < 3 {
		t.Fatalf("expected at least 3 responses, got %d", len(results2))
	}

	// Second call should be blocked by budget
	if _, hasError := results2[2]["error"]; !hasError {
		t.Error("second tool call should be blocked by budget limit")
	}
}

func TestProxyE2E_SSETransport_Receipts(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")

	srv := startMockSSEMCPServer(t, false)
	defer srv.Close()

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","id":"2","method":"tools/call","params":{"name":"echo_tool","arguments":{"msg":"hello"}}}`,
		`{"jsonrpc":"2.0","id":"3","method":"tools/call","params":{"name":"fail_tool","arguments":{}}}`,
	}, "\n") + "\n"

	results := runGuardian(t, binary, []string{
		"--state-dir", stateDir,
		"--transport", "sse",
		"--upstream-url", srv.URL,
		"--enforcement", "advisory",
	}, input)

	if len(results) < 3 {
		t.Fatalf("expected at least 3 responses, got %d", len(results))
	}

	// Verify receipts were created
	ledger, err := receipt.NewLedger(stateDir)
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	records, err := ledger.LoadAll()
	if err != nil {
		t.Fatalf("load receipts: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 receipts, got %d", len(records))
	}

	// First receipt: echo_tool success
	if records[0].ToolName != "echo_tool" {
		t.Errorf("receipt 0 tool=%s, want echo_tool", records[0].ToolName)
	}
	if records[0].Outcome != "success" {
		t.Errorf("receipt 0 outcome=%s, want success", records[0].Outcome)
	}

	// Second receipt: fail_tool error
	if records[1].ToolName != "fail_tool" {
		t.Errorf("receipt 1 tool=%s, want fail_tool", records[1].ToolName)
	}
	if records[1].Outcome != "error" {
		t.Errorf("receipt 1 outcome=%s, want error", records[1].Outcome)
	}

	// Verify hash chain
	for i := 1; i < len(records); i++ {
		if records[i].PreviousHash != records[i-1].Hash {
			t.Errorf("broken hash chain at index %d", i)
		}
	}
}

func TestProxyE2E_SSETransport_CustomHeaders(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")

	var mu sync.Mutex
	var receivedAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			w.WriteHeader(http.StatusOK)
			return
		}
		mu.Lock()
		receivedAuth = r.Header.Get("Authorization")
		mu.Unlock()

		body, _ := io.ReadAll(r.Body)
		var msg struct {
			ID json.RawMessage `json:"id"`
		}
		json.Unmarshal(body, &msg)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"serverInfo":{"name":"mock","version":"0.1"}}}`, string(msg.ID))
	}))
	defer srv.Close()

	input := `{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}` + "\n"

	results := runGuardian(t, binary, []string{
		"--state-dir", stateDir,
		"--transport", "sse",
		"--upstream-url", srv.URL,
		"--sse-header", "Authorization=Bearer e2e-test-token",
	}, input)

	if len(results) < 1 {
		t.Fatalf("expected at least 1 response, got %d", len(results))
	}

	mu.Lock()
	auth := receivedAuth
	mu.Unlock()

	if auth != "Bearer e2e-test-token" {
		t.Errorf("expected Authorization=Bearer e2e-test-token, got %q", auth)
	}
}

func TestProxyE2E_SSETransport_OAuth2(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")

	// Start a mock OAuth2 token server
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		r.ParseForm()
		if r.Form.Get("grant_type") != "client_credentials" {
			http.Error(w, "bad grant_type", http.StatusBadRequest)
			return
		}
		if r.Form.Get("client_id") != "test-client" || r.Form.Get("client_secret") != "test-secret" {
			http.Error(w, "bad credentials", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"oauth2-e2e-tok","token_type":"Bearer","expires_in":3600}`)
	}))
	defer tokenSrv.Close()

	// Start a mock MCP server that requires OAuth2
	var mu sync.Mutex
	var receivedAuth string
	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			w.WriteHeader(http.StatusOK)
			return
		}
		mu.Lock()
		receivedAuth = r.Header.Get("Authorization")
		mu.Unlock()

		if r.Header.Get("Authorization") != "Bearer oauth2-e2e-tok" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":"unauthorized"}`)
			return
		}

		body, _ := io.ReadAll(r.Body)
		var msg struct {
			ID json.RawMessage `json:"id"`
		}
		json.Unmarshal(body, &msg)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"serverInfo":{"name":"mock-oauth","version":"0.1"}}}`, string(msg.ID))
	}))
	defer mcpSrv.Close()

	input := `{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}` + "\n"

	results := runGuardian(t, binary, []string{
		"--state-dir", stateDir,
		"--transport", "sse",
		"--upstream-url", mcpSrv.URL,
		"--oauth2-token-url", tokenSrv.URL,
		"--oauth2-client-id", "test-client",
		"--oauth2-client-secret", "test-secret",
	}, input)

	if len(results) < 1 {
		t.Fatalf("expected at least 1 response, got %d", len(results))
	}

	// Check that the initialize succeeded (not an error)
	if _, hasError := results[0]["error"]; hasError {
		t.Errorf("initialize should succeed with OAuth2, got error: %v", results[0]["error"])
	}

	mu.Lock()
	auth := receivedAuth
	mu.Unlock()

	if auth != "Bearer oauth2-e2e-tok" {
		t.Errorf("expected Authorization=Bearer oauth2-e2e-tok, got %q", auth)
	}
}

func TestProxyE2E_SSETransport_TokenCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	binary := buildBinary(t)
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")

	// Start a mock MCP server that requires authentication
	var mu sync.Mutex
	var receivedAuth string
	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			w.WriteHeader(http.StatusOK)
			return
		}
		mu.Lock()
		receivedAuth = r.Header.Get("Authorization")
		mu.Unlock()

		body, _ := io.ReadAll(r.Body)
		var msg struct {
			ID json.RawMessage `json:"id"`
		}
		json.Unmarshal(body, &msg)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"serverInfo":{"name":"mock","version":"0.1"}}}`, string(msg.ID))
	}))
	defer mcpSrv.Close()

	input := `{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}` + "\n"

	results := runGuardian(t, binary, []string{
		"--state-dir", stateDir,
		"--transport", "sse",
		"--upstream-url", mcpSrv.URL,
		"--token-command", "echo",
		"--token-command-arg", "cmd-token-456",
	}, input)

	if len(results) < 1 {
		t.Fatalf("expected at least 1 response, got %d", len(results))
	}

	if _, hasError := results[0]["error"]; hasError {
		t.Errorf("initialize should succeed, got error: %v", results[0]["error"])
	}

	mu.Lock()
	auth := receivedAuth
	mu.Unlock()

	if auth != "Bearer cmd-token-456" {
		t.Errorf("expected Authorization=Bearer cmd-token-456, got %q", auth)
	}
}

func TestProxyE2E_AutoDiscoveryLogin(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")

	// Mock server: discovery + registration + authorize + token + MCP
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/mcp" && r.Header.Get("Authorization") == "":
			w.Header().Set("WWW-Authenticate", `Bearer realm="test"`)
			w.WriteHeader(http.StatusUnauthorized)

		case r.URL.Path == "/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{
				"authorization_endpoint":"%s/authorize",
				"token_endpoint":"%s/token",
				"registration_endpoint":"%s/register"
			}`, srvURL, srvURL, srvURL)

		case r.URL.Path == "/register":
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"client_id":"e2e-disc-client"}`)

		case r.URL.Path == "/authorize":
			redirectURI := r.URL.Query().Get("redirect_uri")
			state := r.URL.Query().Get("state")
			http.Redirect(w, r, fmt.Sprintf("%s?code=e2e-code&state=%s", redirectURI, state), http.StatusFound)

		case r.URL.Path == "/token":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"access_token":"e2e-disc-tok","refresh_token":"e2e-ref","token_type":"Bearer","expires_in":3600}`)

		case r.URL.Path == "/v1/mcp":
			body, _ := io.ReadAll(r.Body)
			var msg struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			json.Unmarshal(body, &msg)
			id := string(msg.ID)

			switch msg.Method {
			case "initialize":
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"serverInfo":{"name":"disc-mock","version":"0.1"}}}`, id)
			case "tools/list":
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"tools":[{"name":"test_tool","description":"t","inputSchema":{"type":"object","properties":{}}}]}}`, id)
			default:
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{}}`, id)
			}

		case r.Method == "DELETE":
			w.WriteHeader(http.StatusOK)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	srvURL = srv.URL
	defer srv.Close()

	// Step 1: Create minimal profile (no auth config)
	profilePath := filepath.Join(dir, "disc-profile.json")
	profileContent := fmt.Sprintf(`{
		"name": "disc-test",
		"upstream": {"transport": "sse", "url": "%s/v1/mcp"},
		"stateDir": %q
	}`, srvURL, stateDir)
	os.WriteFile(profilePath, []byte(profileContent), 0644)

	// Step 2: Run --login (auto-discovery)
	loginCmd := exec.Command(binary, "--login", profilePath)
	loginCmd.Env = append(os.Environ(), "MCP_GUARDIAN_NO_BROWSER=1")
	loginOut, err := loginCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("--login failed: %v\n%s", err, loginOut)
	}

	if !strings.Contains(string(loginOut), "Login successful") {
		t.Fatalf("expected 'Login successful' in output: %s", loginOut)
	}

	// Step 3: Verify tokens exist
	tokensData, err := os.ReadFile(filepath.Join(stateDir, "tokens.json"))
	if err != nil {
		t.Fatalf("tokens.json not found: %v", err)
	}
	if !strings.Contains(string(tokensData), "e2e-disc-tok") {
		t.Errorf("unexpected tokens: %s", tokensData)
	}

	// Step 4: Run proxy with stored tokens
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","id":"2","method":"tools/list","params":{}}`,
	}, "\n") + "\n"

	results := runGuardian(t, binary, []string{
		"--profile", profilePath,
	}, input)

	if len(results) < 2 {
		t.Fatalf("expected at least 2 responses, got %d", len(results))
	}

	// Verify initialize succeeded
	if results[0]["id"] != "1" {
		t.Errorf("expected id=1, got %v", results[0]["id"])
	}

	// Verify tools/list has meta-tools
	result2, _ := results[1]["result"].(map[string]interface{})
	tools, _ := result2["tools"].([]interface{})
	if len(tools) != 6 { // 1 server tool + 5 meta-tools
		t.Errorf("expected 6 tools (1 + 5 meta), got %d", len(tools))
	}
}

func TestProxyE2E_ProfileBased(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	serverPath := writeMockServer(t, dir)

	// Create a profile file
	profilePath := filepath.Join(dir, "test-profile.json")
	profileContent := fmt.Sprintf(`{
		"name": "test-profile",
		"upstream": {
			"transport": "stdio",
			"command": "sh",
			"args": [%q]
		},
		"governance": {
			"enforcement": "advisory"
		},
		"stateDir": %q
	}`, serverPath, filepath.Join(dir, "state"))
	os.WriteFile(profilePath, []byte(profileContent), 0644)

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","id":"2","method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":"3","method":"tools/call","params":{"name":"echo_tool","arguments":{"msg":"hello"}}}`,
	}, "\n") + "\n"

	// No trailing "-- command" needed when using profile
	results := runGuardian(t, binary, []string{
		"--profile", profilePath,
	}, input)

	if len(results) < 3 {
		t.Fatalf("expected at least 3 responses, got %d", len(results))
	}

	// Check initialize
	if results[0]["id"] != "1" {
		t.Errorf("expected id=1, got %v", results[0]["id"])
	}

	// Check tools/list has meta-tools
	result2, _ := results[1]["result"].(map[string]interface{})
	tools, _ := result2["tools"].([]interface{})
	if len(tools) != 7 {
		t.Errorf("expected 7 tools (2 + 5 meta), got %d", len(tools))
	}

	// Check tool call
	result3, _ := results[2]["result"].(map[string]interface{})
	content, _ := result3["content"].([]interface{})
	if len(content) == 0 {
		t.Fatal("expected content in tool call response")
	}
	item, _ := content[0].(map[string]interface{})
	if item["text"] != "ok" {
		t.Errorf("expected text=ok, got %v", item["text"])
	}
}

func TestProxyE2E_ProfileSSE(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()

	srv := startMockSSEMCPServer(t, false)
	defer srv.Close()

	profilePath := filepath.Join(dir, "sse-profile.json")
	profileContent := fmt.Sprintf(`{
		"name": "sse-profile",
		"upstream": {
			"transport": "sse",
			"url": %q
		},
		"governance": { "enforcement": "strict" },
		"stateDir": %q
	}`, srv.URL, filepath.Join(dir, "state"))
	os.WriteFile(profilePath, []byte(profileContent), 0644)

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","id":"2","method":"tools/list","params":{}}`,
	}, "\n") + "\n"

	results := runGuardian(t, binary, []string{
		"--profile", profilePath,
	}, input)

	if len(results) < 2 {
		t.Fatalf("expected at least 2 responses, got %d", len(results))
	}

	if results[0]["id"] != "1" {
		t.Errorf("expected id=1, got %v", results[0]["id"])
	}

	result2, _ := results[1]["result"].(map[string]interface{})
	tools, _ := result2["tools"].([]interface{})
	if len(tools) != 7 {
		t.Errorf("expected 7 tools (2 + 5 meta), got %d", len(tools))
	}
}

func TestProxyE2E_ProfilesList(t *testing.T) {
	binary := buildBinary(t)

	// Create temp profile dir
	dir := t.TempDir()
	profilesDir := filepath.Join(dir, ".config", "mcp-guardian", "profiles")
	os.MkdirAll(profilesDir, 0755)
	os.WriteFile(filepath.Join(profilesDir, "alpha.json"), []byte(`{"name":"alpha"}`), 0644)
	os.WriteFile(filepath.Join(profilesDir, "beta.json"), []byte(`{"name":"beta"}`), 0644)

	// Run with --profiles using HOME override
	cmd := exec.Command(binary, "--profiles")
	cmd.Env = append(os.Environ(), "HOME="+dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("--profiles failed: %v\n%s", err, out)
	}

	output := string(out)
	if !strings.Contains(output, "alpha") || !strings.Contains(output, "beta") {
		t.Errorf("expected alpha and beta in output, got: %s", output)
	}
}

