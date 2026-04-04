package classify

import "regexp"

var volatilePatterns = []*regexp.Regexp{
	// timestamps: ISO 8601, unix, etc.
	regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}[.\d]*[Z]?`),
	regexp.MustCompile(`\d{10,13}`), // unix ms/s
	// UUIDs
	regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`),
	// IPs
	regexp.MustCompile(`\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}`),
	// PIDs and ports (e.g., "pid 12345", ":8080")
	regexp.MustCompile(`(?i)pid\s*\d+`),
	regexp.MustCompile(`:\d{4,5}\b`),
	// hex hashes (32+ chars)
	regexp.MustCompile(`[0-9a-f]{32,}`),
}

// NormalizeError strips volatile components (timestamps, UUIDs, IPs, PIDs)
// from error text to produce a stable signature for constraint matching.
func NormalizeError(text string) string {
	result := text
	for _, pat := range volatilePatterns {
		result = pat.ReplaceAllString(result, "<X>")
	}
	return result
}
