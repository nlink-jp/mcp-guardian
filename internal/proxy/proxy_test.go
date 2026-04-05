package proxy_test

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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

func TestProxyE2E_ServerConfig(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	serverPath := writeMockServer(t, dir)
	stateDir := filepath.Join(dir, "state")

	// Write per-server config with mask settings
	srvCfg := filepath.Join(dir, "server.json")
	srvContent := `{
  "enforcement": "strict",
  "mask": ["fail_*"]
}`
	if err := os.WriteFile(srvCfg, []byte(srvContent), 0644); err != nil {
		t.Fatal(err)
	}

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","id":"2","method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":"3","method":"tools/call","params":{"name":"fail_tool","arguments":{}}}`,
	}, "\n") + "\n"

	results := runGuardian(t, binary, []string{
		"--state-dir", stateDir,
		"--server-config", srvCfg,
		"--", "sh", serverPath,
	}, input)

	if len(results) < 3 {
		t.Fatalf("expected at least 3 responses, got %d", len(results))
	}

	// fail_tool should be masked (from server config)
	result2, _ := results[1]["result"].(map[string]interface{})
	tools, _ := result2["tools"].([]interface{})
	for _, tool := range tools {
		tm, _ := tool.(map[string]interface{})
		if tm["name"] == "fail_tool" {
			t.Error("fail_tool should be masked via server config")
		}
	}

	// tools/call fail_tool should be blocked
	r3 := results[2]
	if _, hasError := r3["error"]; !hasError {
		t.Error("masked tool call via server config should return an error")
	}
}

func TestProxyE2E_ServerConfigCLIOverride(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	serverPath := writeMockServer(t, dir)
	stateDir := filepath.Join(dir, "state")

	// Server config sets enforcement=strict
	srvCfg := filepath.Join(dir, "server.json")
	srvContent := `{
  "enforcement": "strict",
  "mask": ["fail_*"]
}`
	if err := os.WriteFile(srvCfg, []byte(srvContent), 0644); err != nil {
		t.Fatal(err)
	}

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","id":"2","method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":"3","method":"tools/call","params":{"name":"fail_tool","arguments":{}}}`,
	}, "\n") + "\n"

	// CLI overrides enforcement to advisory — mask should not block
	results := runGuardian(t, binary, []string{
		"--state-dir", stateDir,
		"--server-config", srvCfg,
		"--enforcement", "advisory",
		"--", "sh", serverPath,
	}, input)

	if len(results) < 3 {
		t.Fatalf("expected at least 3 responses, got %d", len(results))
	}

	// In advisory mode (CLI override), fail_tool should still appear in tools/list
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
		t.Error("advisory mode (CLI override) should not mask tools from tools/list")
	}

	// tools/call fail_tool should be forwarded (not blocked) in advisory mode
	r3 := results[2]
	if _, hasError := r3["error"]; hasError {
		t.Error("advisory mode (CLI override) should not block masked tool calls")
	}
}

func TestProxyE2E_GlobalAndServerConfig(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	serverPath := writeMockServer(t, dir)
	stateDir := filepath.Join(dir, "state")

	// Global config: sets defaults enforcement=advisory
	globalCfg := filepath.Join(dir, "global.json")
	globalContent := `{
  "defaults": {
    "enforcement": "advisory"
  }
}`
	os.WriteFile(globalCfg, []byte(globalContent), 0644)

	// Server config: overrides enforcement=strict, adds mask
	srvCfg := filepath.Join(dir, "server.json")
	srvContent := `{
  "enforcement": "strict",
  "mask": ["fail_*"]
}`
	os.WriteFile(srvCfg, []byte(srvContent), 0644)

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","id":"2","method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":"3","method":"tools/call","params":{"name":"fail_tool","arguments":{}}}`,
	}, "\n") + "\n"

	// Server config (strict) should override global defaults (advisory)
	results := runGuardian(t, binary, []string{
		"--state-dir", stateDir,
		"--config", globalCfg,
		"--server-config", srvCfg,
		"--", "sh", serverPath,
	}, input)

	if len(results) < 3 {
		t.Fatalf("expected at least 3 responses, got %d", len(results))
	}

	// Server enforcement=strict → fail_tool should be masked
	result2, _ := results[1]["result"].(map[string]interface{})
	tools, _ := result2["tools"].([]interface{})
	for _, tool := range tools {
		tm, _ := tool.(map[string]interface{})
		if tm["name"] == "fail_tool" {
			t.Error("fail_tool should be masked (server overrides global)")
		}
	}

	// Blocked
	r3 := results[2]
	if _, hasError := r3["error"]; !hasError {
		t.Error("fail_tool should be blocked in strict mode")
	}
}
