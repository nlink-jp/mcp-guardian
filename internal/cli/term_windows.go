//go:build windows

package cli

func isTerminal(fd uintptr) bool {
	// On Windows, assume no color support for simplicity.
	// Could be extended with Windows Console API if needed.
	return false
}
