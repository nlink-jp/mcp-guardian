package governance

import (
	"testing"
	"time"

	"github.com/nlink-jp/mcp-guardian/internal/state"
)

func TestBudgetCheck(t *testing.T) {
	tests := []struct {
		name     string
		count    int
		max      int
		wantPass bool
	}{
		{"unlimited", 100, 0, true},
		{"under budget", 5, 10, true},
		{"at budget", 10, 10, false},
		{"over budget", 11, 10, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			passed, _ := BudgetCheck(tt.count, tt.max)
			if passed != tt.wantPass {
				t.Errorf("got %v, want %v", passed, tt.wantPass)
			}
		})
	}
}

func TestCheckConstraints(t *testing.T) {
	now := time.Now().UnixMilli()
	constraints := []state.Constraint{
		{
			ID:               "c1",
			ToolName:         "write_file",
			Target:           "/tmp/test.txt",
			FailureSignature: "permission_denied",
			CreatedAt:        now - 1000,
			TTLMs:            3600000,
		},
		{
			ID:               "c2",
			ToolName:         "delete_file",
			Target:           "/tmp/old.txt",
			FailureSignature: "not_found",
			CreatedAt:        now - 7200000, // expired
			TTLMs:            3600000,
		},
	}

	tests := []struct {
		name     string
		tool     string
		target   string
		wantPass bool
	}{
		{"blocked", "write_file", "/tmp/test.txt", false},
		{"different target", "write_file", "/tmp/other.txt", true},
		{"different tool", "read_file", "/tmp/test.txt", true},
		{"expired constraint", "delete_file", "/tmp/old.txt", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CheckConstraints(tt.tool, tt.target, constraints, now)
			if result.Passed != tt.wantPass {
				t.Errorf("got %v, want %v", result.Passed, tt.wantPass)
			}
		})
	}
}

func TestCheckAuthority(t *testing.T) {
	tests := []struct {
		name     string
		auth     *state.Authority
		wantPass bool
	}{
		{"nil authority", nil, true},
		{"synced", &state.Authority{Epoch: 3, ActiveSessionEpoch: 3}, true},
		{"stale", &state.Authority{Epoch: 5, ActiveSessionEpoch: 3}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CheckAuthority(tt.auth)
			if result.Passed != tt.wantPass {
				t.Errorf("got %v, want %v", result.Passed, tt.wantPass)
			}
		})
	}
}

func TestRunGatesBlocking(t *testing.T) {
	now := time.Now().UnixMilli()
	constraints := []state.Constraint{
		{
			ID:               "c1",
			ToolName:         "write_file",
			Target:           "/tmp/blocked.txt",
			FailureSignature: "permission_denied",
			CreatedAt:        now - 1000,
			TTLMs:            3600000,
		},
	}

	result := RunGates(GateInput{
		ToolName:    "write_file",
		Arguments:   map[string]interface{}{"path": "/tmp/blocked.txt"},
		Constraints: constraints,
		Authority:   &state.Authority{Epoch: 1, ActiveSessionEpoch: 1},
		Enforcement: "strict",
		Convergence: NewConvergenceTracker(),
	})

	if result.Forward {
		t.Fatal("expected blocked, got forward")
	}
}

func TestRunGatesAdvisory(t *testing.T) {
	now := time.Now().UnixMilli()
	constraints := []state.Constraint{
		{
			ID:               "c1",
			ToolName:         "write_file",
			Target:           "/tmp/blocked.txt",
			FailureSignature: "permission_denied",
			CreatedAt:        now - 1000,
			TTLMs:            3600000,
		},
	}

	result := RunGates(GateInput{
		ToolName:    "write_file",
		Arguments:   map[string]interface{}{"path": "/tmp/blocked.txt"},
		Constraints: constraints,
		Authority:   &state.Authority{Epoch: 1, ActiveSessionEpoch: 1},
		Enforcement: "advisory",
		Convergence: NewConvergenceTracker(),
	})

	if !result.Forward {
		t.Fatal("expected forward in advisory mode")
	}
}
