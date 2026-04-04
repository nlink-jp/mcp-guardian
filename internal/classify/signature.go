package classify

import (
	"regexp"
	"strings"
)

// Signature patterns ordered by specificity.
var signaturePatterns = []struct {
	re   *regexp.Regexp
	name string
}{
	{regexp.MustCompile(`(?i)permission\s+denied`), "permission_denied"},
	{regexp.MustCompile(`(?i)access\s+denied`), "access_denied"},
	{regexp.MustCompile(`(?i)not\s+found`), "not_found"},
	{regexp.MustCompile(`(?i)no\s+such\s+file`), "no_such_file"},
	{regexp.MustCompile(`(?i)ENOENT`), "no_such_file"},
	{regexp.MustCompile(`(?i)EACCES`), "permission_denied"},
	{regexp.MustCompile(`(?i)EPERM`), "permission_denied"},
	{regexp.MustCompile(`(?i)timed?\s*out`), "timeout"},
	{regexp.MustCompile(`(?i)ETIMEDOUT`), "timeout"},
	{regexp.MustCompile(`(?i)ECONNREFUSED`), "connection_refused"},
	{regexp.MustCompile(`(?i)connection\s+refused`), "connection_refused"},
	{regexp.MustCompile(`(?i)syntax\s*error`), "syntax_error"},
	{regexp.MustCompile(`(?i)parse\s*error`), "parse_error"},
	{regexp.MustCompile(`(?i)invalid\s+argument`), "invalid_argument"},
	{regexp.MustCompile(`(?i)already\s+exists`), "already_exists"},
	{regexp.MustCompile(`(?i)EEXIST`), "already_exists"},
	{regexp.MustCompile(`(?i)disk\s+full`), "disk_full"},
	{regexp.MustCompile(`(?i)ENOSPC`), "disk_full"},
	{regexp.MustCompile(`(?i)read.only`), "read_only"},
	{regexp.MustCompile(`(?i)EROFS`), "read_only"},
}

// ExtractSignature extracts a stable failure signature from error text.
// First tries known patterns, then falls back to the normalized first line.
func ExtractSignature(errorText string) string {
	normalized := NormalizeError(errorText)
	for _, p := range signaturePatterns {
		if p.re.MatchString(normalized) {
			return p.name
		}
	}
	// fallback: first line, trimmed
	lines := strings.SplitN(normalized, "\n", 2)
	first := strings.TrimSpace(lines[0])
	if len(first) > 100 {
		first = first[:100]
	}
	return first
}
