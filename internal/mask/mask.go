package mask

import "path"

// Match reports whether name matches any of the given patterns.
// Patterns use path.Match semantics: * matches any sequence of non-/ characters,
// ? matches any single non-/ character.
func Match(name string, patterns []string) bool {
	for _, p := range patterns {
		if matched, _ := path.Match(p, name); matched {
			return true
		}
	}
	return false
}
