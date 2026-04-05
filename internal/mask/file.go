package mask

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// LoadPatterns reads mask patterns from a file, one pattern per line.
// Empty lines and lines starting with # are ignored.
func LoadPatterns(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open mask file: %w", err)
	}
	defer f.Close()

	var patterns []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read mask file: %w", err)
	}
	return patterns, nil
}
