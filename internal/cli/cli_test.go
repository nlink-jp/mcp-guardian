package cli_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nlink-jp/mcp-guardian/internal/receipt"
)

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

func buildBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "mcp-guardian")
	cmd := exec.Command("go", "build", "-o", binary, ".")
	cmd.Dir = projectRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return binary
}

func createTestReceipts(t *testing.T, dir string, count int) {
	t.Helper()
	ledger, err := receipt.NewLedger(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < count; i++ {
		r := receipt.Record{
			ToolName:     "test_tool",
			Target:       "/tmp/test",
			MutationType: "readonly",
			Outcome:      "success",
			DurationMs:   1,
		}
		if err := ledger.Append(&r); err != nil {
			t.Fatal(err)
		}
	}
}

func TestVerifyIntactChain(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	createTestReceipts(t, dir, 3)

	out, err := exec.Command(binary, "--state-dir", dir, "--verify").CombinedOutput()
	if err != nil {
		t.Fatalf("verify failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "intact") {
		t.Errorf("expected 'intact' in output, got: %s", out)
	}
	if !strings.Contains(string(out), "3 receipts") {
		t.Errorf("expected '3 receipts' in output, got: %s", out)
	}
}

func TestVerifyEmptyLedger(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()

	out, err := exec.Command(binary, "--state-dir", dir, "--verify").CombinedOutput()
	if err != nil {
		t.Fatalf("verify failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "No receipts") {
		t.Errorf("expected 'No receipts' in output, got: %s", out)
	}
}

func TestViewReceipts(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	createTestReceipts(t, dir, 2)

	out, err := exec.Command(binary, "--state-dir", dir, "--view").CombinedOutput()
	if err != nil {
		t.Fatalf("view failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "test_tool") {
		t.Errorf("expected 'test_tool' in output, got: %s", out)
	}
	if !strings.Contains(string(out), "2 receipt(s)") {
		t.Errorf("expected '2 receipt(s)' in output, got: %s", out)
	}
}

func TestViewFilterByTool(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()

	ledger, _ := receipt.NewLedger(dir)
	ledger.Append(&receipt.Record{ToolName: "tool_a", Outcome: "success", DurationMs: 1})
	ledger.Append(&receipt.Record{ToolName: "tool_b", Outcome: "error", DurationMs: 1})

	out, _ := exec.Command(binary, "--state-dir", dir, "--view", "--tool", "tool_a").CombinedOutput()
	if strings.Contains(string(out), "tool_b") {
		t.Error("tool_b should be filtered out")
	}
	if !strings.Contains(string(out), "tool_a") {
		t.Error("tool_a should be present")
	}
}

func TestViewEmpty(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	// Create empty receipts file so view doesn't error on missing file
	os.WriteFile(filepath.Join(dir, "receipts.jsonl"), nil, 0644)

	out, _ := exec.Command(binary, "--state-dir", dir, "--view").CombinedOutput()
	if !strings.Contains(string(out), "No receipts") {
		t.Errorf("expected 'No receipts' for empty ledger, got: %s", out)
	}
}

func TestReceipts(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	createTestReceipts(t, dir, 5)

	out, err := exec.Command(binary, "--state-dir", dir, "--receipts").CombinedOutput()
	if err != nil {
		t.Fatalf("receipts failed: %v\n%s", err, out)
	}
	output := string(out)
	if !strings.Contains(output, "5 total") {
		t.Errorf("expected '5 total' in receipts summary, got: %s", output)
	}
	if !strings.Contains(output, "5 success") {
		t.Errorf("expected '5 success' in receipts summary, got: %s", output)
	}
}
