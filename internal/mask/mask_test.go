package mask

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMatch_Exact(t *testing.T) {
	if !Match("write_file", []string{"write_file"}) {
		t.Error("exact match should succeed")
	}
}

func TestMatch_Wildcard(t *testing.T) {
	patterns := []string{"write_*"}
	if !Match("write_file", patterns) {
		t.Error("write_file should match write_*")
	}
	if !Match("write_config", patterns) {
		t.Error("write_config should match write_*")
	}
	if Match("read_file", patterns) {
		t.Error("read_file should not match write_*")
	}
}

func TestMatch_QuestionMark(t *testing.T) {
	patterns := []string{"tool_?"}
	if !Match("tool_a", patterns) {
		t.Error("tool_a should match tool_?")
	}
	if Match("tool_ab", patterns) {
		t.Error("tool_ab should not match tool_?")
	}
}

func TestMatch_NoMatch(t *testing.T) {
	if Match("write_file", []string{"delete_*"}) {
		t.Error("write_file should not match delete_*")
	}
}

func TestMatch_MultiplePatterns(t *testing.T) {
	patterns := []string{"write_*", "delete_*"}
	if !Match("write_file", patterns) {
		t.Error("write_file should match")
	}
	if !Match("delete_dir", patterns) {
		t.Error("delete_dir should match")
	}
	if Match("read_file", patterns) {
		t.Error("read_file should not match")
	}
}

func TestMatch_EmptyPatterns(t *testing.T) {
	if Match("anything", nil) {
		t.Error("nil patterns should match nothing")
	}
	if Match("anything", []string{}) {
		t.Error("empty patterns should match nothing")
	}
}

func TestLoadPatterns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "masks.txt")

	content := `# Dangerous tools
write_*
delete_*

# Shell access
execute_command
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	patterns, err := LoadPatterns(path)
	if err != nil {
		t.Fatal(err)
	}

	expected := []string{"write_*", "delete_*", "execute_command"}
	if len(patterns) != len(expected) {
		t.Fatalf("expected %d patterns, got %d: %v", len(expected), len(patterns), patterns)
	}
	for i, p := range patterns {
		if p != expected[i] {
			t.Errorf("pattern[%d]: expected %q, got %q", i, expected[i], p)
		}
	}
}

func TestLoadPatterns_NotFound(t *testing.T) {
	_, err := LoadPatterns("/nonexistent/file.txt")
	if err == nil {
		t.Error("should return error for nonexistent file")
	}
}
