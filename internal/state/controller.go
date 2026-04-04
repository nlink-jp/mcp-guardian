package state

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Controller represents the stable controller identity.
type Controller struct {
	ID            string `json:"id"`
	EstablishedAt int64  `json:"establishedAt"`
}

// GenerateUUID creates a UUID v4 using crypto/rand.
func GenerateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 2
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// LoadOrCreateController loads controller.json or creates a new one.
func LoadOrCreateController(dir string) (*Controller, error) {
	path := filepath.Join(dir, "controller.json")
	data, err := os.ReadFile(path)
	if err == nil {
		var c Controller
		if err := json.Unmarshal(data, &c); err != nil {
			return nil, fmt.Errorf("parse controller.json: %w", err)
		}
		return &c, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read controller.json: %w", err)
	}

	c := &Controller{
		ID:            GenerateUUID(),
		EstablishedAt: time.Now().UnixMilli(),
	}
	out, _ := json.MarshalIndent(c, "", "  ")
	if err := AtomicWrite(path, out, 0644); err != nil {
		return nil, fmt.Errorf("write controller.json: %w", err)
	}
	return c, nil
}
