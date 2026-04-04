package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Intent represents a declared intent for containment attribution.
type Intent struct {
	Goal       string      `json:"goal"`
	Predicates []Predicate `json:"predicates"`
	DeclaredAt int64       `json:"declaredAt"`
	Version    int         `json:"version"`
}

// Predicate represents a single predicate in an intent declaration.
type Predicate struct {
	Type   string                 `json:"type"`
	Fields map[string]interface{} `json:"fields,omitempty"`
}

// LoadIntent loads intent.json if it exists.
func LoadIntent(dir string) (*Intent, error) {
	path := filepath.Join(dir, "intent.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read intent.json: %w", err)
	}
	var intent Intent
	if err := json.Unmarshal(data, &intent); err != nil {
		return nil, fmt.Errorf("parse intent.json: %w", err)
	}
	return &intent, nil
}

// SaveIntent writes intent.json atomically.
func SaveIntent(dir string, goal string, predicates []Predicate) (*Intent, error) {
	existing, _ := LoadIntent(dir)
	version := 1
	if existing != nil {
		version = existing.Version + 1
	}

	intent := &Intent{
		Goal:       goal,
		Predicates: predicates,
		DeclaredAt: time.Now().UnixMilli(),
		Version:    version,
	}

	path := filepath.Join(dir, "intent.json")
	out, _ := json.MarshalIndent(intent, "", "  ")
	if err := AtomicWrite(path, out, 0644); err != nil {
		return nil, err
	}
	return intent, nil
}

// ClearIntent removes intent.json.
func ClearIntent(dir string) error {
	path := filepath.Join(dir, "intent.json")
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
