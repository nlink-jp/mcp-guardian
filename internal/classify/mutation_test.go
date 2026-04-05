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
		// snake_case
		{"write_file", Mutating},
		{"read_file", ReadOnly},
		{"list_directory", ReadOnly},
		{"delete_file", Mutating},
		{"create_database", Mutating},
		{"get_status", ReadOnly},
		{"search_files", ReadOnly},
		{"execute_command", Mutating},
		{"unknown_thing", Mutating}, // default: mutating
		// camelCase
		{"getConfluenceSpaces", ReadOnly},
		{"getPagesInConfluenceSpace", ReadOnly},
		{"getAccessibleAtlassianResources", ReadOnly},
		{"deleteResource", Mutating},
		{"createNewItem", Mutating},
		{"updateUserProfile", Mutating},
		{"listAllUsers", ReadOnly},
		{"fetchDataFromAPI", ReadOnly},
		// camelCase with acronyms
		{"getHTTPResponse", ReadOnly},
		{"searchDNSRecords", ReadOnly},
		// mixed: camelCase + suffix with info/status (read verbs)
		{"atlassianUserInfo", ReadOnly},
		{"serverStatus", ReadOnly},
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

func TestTokenize(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"get_status", []string{"get", "status"}},
		{"delete-file", []string{"delete", "file"}},
		{"getConfluenceSpaces", []string{"get", "confluence", "spaces"}},
		{"atlassianUserInfo", []string{"atlassian", "user", "info"}},
		{"getHTTPResponse", []string{"get", "http", "response"}},
		{"getPagesInConfluenceSpace", []string{"get", "pages", "in", "confluence", "space"}},
		{"simple", []string{"simple"}},
		{"ABC", []string{"abc"}},
		{"getA", []string{"get", "a"}},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := tokenize(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("tokenize(%q) = %v, want %v", tt.input, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("tokenize(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
				}
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
