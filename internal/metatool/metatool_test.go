package metatool

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nlink-jp/mcp-guardian/internal/governance"
	"github.com/nlink-jp/mcp-guardian/internal/jsonrpc"
	"github.com/nlink-jp/mcp-guardian/internal/receipt"
	"github.com/nlink-jp/mcp-guardian/internal/state"
)

func setupContext(t *testing.T) *Context {
	t.Helper()
	dir := t.TempDir()
	state.EnsureDir(dir)
	ctrl, _ := state.LoadOrCreateController(dir)
	auth, _ := state.LoadOrCreateAuthority(dir, ctrl.ID)
	ledger, _ := receipt.NewLedger(dir)
	return &Context{
		StateDir:    dir,
		Controller:  ctrl,
		Authority:   auth,
		Ledger:      ledger,
		Convergence: governance.NewConvergenceTracker(),
	}
}

func TestIsMetaTool(t *testing.T) {
	if !IsMetaTool(GovernanceStatus) {
		t.Error("governance_status should be a meta-tool")
	}
	if !IsMetaTool(GovernanceBumpAuthority) {
		t.Error("governance_bump_authority should be a meta-tool")
	}
	if IsMetaTool("write_file") {
		t.Error("write_file should not be a meta-tool")
	}
}

func TestDefinitions(t *testing.T) {
	defs := Definitions()
	if len(defs) != 5 {
		t.Fatalf("expected 5 definitions, got %d", len(defs))
	}
	names := map[string]bool{}
	for _, d := range defs {
		names[d.Name] = true
		if d.InputSchema == nil {
			t.Errorf("%s has nil InputSchema", d.Name)
		}
	}
	for _, expected := range []string{
		GovernanceStatus, GovernanceBumpAuthority,
		GovernanceDeclareIntent, GovernanceClearIntent,
		GovernanceConvergenceStatus,
	} {
		if !names[expected] {
			t.Errorf("missing definition for %s", expected)
		}
	}
}

func TestHandleStatus(t *testing.T) {
	ctx := setupContext(t)
	result, err := Handle(ctx, GovernanceStatus, nil)
	if err != nil {
		t.Fatal(err)
	}
	tr, ok := result.(jsonrpc.ToolResult)
	if !ok {
		t.Fatal("expected ToolResult")
	}
	if len(tr.Content) == 0 {
		t.Fatal("expected content")
	}
	text := tr.Content[0].Text
	if !strings.Contains(text, "controllerId") {
		t.Errorf("expected controllerId in response, got: %.100s", text)
	}
	if !strings.Contains(text, "epoch") {
		t.Errorf("expected epoch in response, got: %.100s", text)
	}
}

func TestHandleBumpAuthority(t *testing.T) {
	ctx := setupContext(t)
	originalEpoch := ctx.Authority.Epoch

	result, err := Handle(ctx, GovernanceBumpAuthority, nil)
	if err != nil {
		t.Fatal(err)
	}
	tr := result.(jsonrpc.ToolResult)
	if tr.IsError {
		t.Error("bump should not return error")
	}
	if ctx.Authority.Epoch != originalEpoch+1 {
		t.Errorf("expected epoch %d, got %d", originalEpoch+1, ctx.Authority.Epoch)
	}
}

func TestHandleDeclareIntent(t *testing.T) {
	ctx := setupContext(t)

	// Missing goal
	result, _ := Handle(ctx, GovernanceDeclareIntent, map[string]interface{}{})
	tr := result.(jsonrpc.ToolResult)
	if !tr.IsError {
		t.Error("expected error for missing goal")
	}

	// Valid goal
	result, _ = Handle(ctx, GovernanceDeclareIntent, map[string]interface{}{
		"goal": "test task",
	})
	tr = result.(jsonrpc.ToolResult)
	if tr.IsError {
		t.Errorf("unexpected error: %s", tr.Content[0].Text)
	}
	if !strings.Contains(tr.Content[0].Text, "test task") {
		t.Error("expected goal in response")
	}
}

func TestHandleClearIntent(t *testing.T) {
	ctx := setupContext(t)

	// Declare first
	Handle(ctx, GovernanceDeclareIntent, map[string]interface{}{"goal": "test"})

	// Clear
	result, _ := Handle(ctx, GovernanceClearIntent, nil)
	tr := result.(jsonrpc.ToolResult)
	if tr.IsError {
		t.Error("clear should not return error")
	}
	if !strings.Contains(tr.Content[0].Text, "cleared") {
		t.Error("expected cleared in response")
	}
}

func TestHandleConvergenceStatus(t *testing.T) {
	ctx := setupContext(t)
	result, err := Handle(ctx, GovernanceConvergenceStatus, nil)
	if err != nil {
		t.Fatal(err)
	}
	tr := result.(jsonrpc.ToolResult)
	if len(tr.Content) == 0 {
		t.Fatal("expected content")
	}
	// Should be valid JSON
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(tr.Content[0].Text), &parsed); err != nil {
		t.Errorf("convergence status should be valid JSON: %v", err)
	}
}

func TestHandleUnknown(t *testing.T) {
	ctx := setupContext(t)
	result, err := Handle(ctx, "unknown_tool", nil)
	if err != nil {
		t.Error("unknown tool should not return error")
	}
	if result != nil {
		t.Error("unknown tool should return nil")
	}
}
