package classify

import (
	"encoding/json"
	"testing"
)

func TestClassifyMutationByVerb(t *testing.T) {
	tests := []struct {
		tool string
		want string
	}{
		{"write_file", Mutating},
		{"read_file", ReadOnly},
		{"list_directory", ReadOnly},
		{"delete_file", Mutating},
		{"create_database", Mutating},
		{"get_status", ReadOnly},
		{"search_files", ReadOnly},
		{"execute_command", Mutating},
		{"unknown_thing", Mutating}, // default: mutating
	}
	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			got := ClassifyMutation(tt.tool, nil, nil)
			if got != tt.want {
				t.Errorf("ClassifyMutation(%q) = %q, want %q", tt.tool, got, tt.want)
			}
		})
	}
}

func TestClassifyMutationBySchema(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"content":{"type":"string"}}}`)
	got := ClassifyMutation("some_tool", nil, schema)
	if got != Mutating {
		t.Errorf("expected mutating for schema with 'content', got %q", got)
	}
}

func TestClassifyMutationByArgs(t *testing.T) {
	args := map[string]interface{}{"data": "some payload"}
	got := ClassifyMutation("unknown_tool", args, nil)
	if got != Mutating {
		t.Errorf("expected mutating for args with 'data', got %q", got)
	}
}

func TestClassifyMutationSQL(t *testing.T) {
	args := map[string]interface{}{"query": "DELETE FROM users WHERE id = 1"}
	got := ClassifyMutation("run_query", args, nil)
	if got != Mutating {
		t.Errorf("expected mutating for SQL DELETE, got %q", got)
	}
}
