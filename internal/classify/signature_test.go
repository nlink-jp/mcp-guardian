package classify

import "testing"

func TestExtractSignature(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Permission denied: /tmp/foo", "permission_denied"},
		{"ENOENT: no such file or directory", "no_such_file"},
		{"EACCES: permission denied", "permission_denied"},
		{"Connection refused at 192.168.1.1:8080", "connection_refused"},
		{"SyntaxError: Unexpected token '}'", "syntax_error"},
		{"ETIMEDOUT: request timed out", "timeout"},
		{"File already exists: /tmp/test.txt", "already_exists"},
		{"unknown error occurred", "unknown error occurred"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := ExtractSignature(tt.input)
			if got != tt.want {
				t.Errorf("ExtractSignature(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNormalizeError(t *testing.T) {
	input := "Error at 2024-01-15T12:00:00Z from 192.168.1.100:8080 pid 12345"
	result := NormalizeError(input)
	if result == input {
		t.Error("expected normalization to strip volatile components")
	}
}
