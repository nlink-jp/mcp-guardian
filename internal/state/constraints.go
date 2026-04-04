package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Constraint represents a learned failure constraint.
type Constraint struct {
	ID               string `json:"id"`
	ToolName         string `json:"toolName"`
	Target           string `json:"target"`
	FailureSignature string `json:"failureSignature"`
	ErrorSnippet     string `json:"errorSnippet,omitempty"`
	CreatedAt        int64  `json:"createdAt"`
	TTLMs            int64  `json:"ttlMs"`
}

// IsExpired returns true if the constraint has exceeded its TTL.
func (c *Constraint) IsExpired(now int64) bool {
	return now > c.CreatedAt+c.TTLMs
}

// LoadConstraints loads constraints.json.
func LoadConstraints(dir string) ([]Constraint, error) {
	path := filepath.Join(dir, "constraints.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read constraints.json: %w", err)
	}
	var constraints []Constraint
	if err := json.Unmarshal(data, &constraints); err != nil {
		return nil, fmt.Errorf("parse constraints.json: %w", err)
	}
	return constraints, nil
}

// SaveConstraints writes constraints.json atomically.
func SaveConstraints(dir string, constraints []Constraint) error {
	path := filepath.Join(dir, "constraints.json")
	out, _ := json.MarshalIndent(constraints, "", "  ")
	return AtomicWrite(path, out, 0644)
}

// AddConstraint creates and appends a new constraint, pruning expired ones.
func AddConstraint(dir string, toolName, target, signature, snippet string, ttlMs int64) error {
	now := time.Now().UnixMilli()
	constraints, _ := LoadConstraints(dir)

	// prune expired
	active := make([]Constraint, 0, len(constraints))
	for _, c := range constraints {
		if !c.IsExpired(now) {
			active = append(active, c)
		}
	}

	id := fmt.Sprintf("c_%d_%s", now, GenerateUUID()[:6])
	active = append(active, Constraint{
		ID:               id,
		ToolName:         toolName,
		Target:           target,
		FailureSignature: signature,
		ErrorSnippet:     snippet,
		CreatedAt:        now,
		TTLMs:            ttlMs,
	})

	return SaveConstraints(dir, active)
}
