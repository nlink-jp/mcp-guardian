package state

import (
	"os"
)

// EnsureDir creates the state directory if it doesn't exist.
func EnsureDir(dir string) error {
	return os.MkdirAll(dir, 0755)
}
