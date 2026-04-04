package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Authority represents the epoch-based authority state.
type Authority struct {
	ControllerID       string `json:"controllerId"`
	Epoch              int    `json:"epoch"`
	LastBumpedAt       int64  `json:"lastBumpedAt"`
	ActiveSessionEpoch int    `json:"activeSessionEpoch"`
	SessionStartedAt   int64  `json:"sessionStartedAt"`
	GenesisHash        string `json:"genesisHash,omitempty"`
}

// LoadOrCreateAuthority loads authority.json or creates a new one.
func LoadOrCreateAuthority(dir string, controllerID string) (*Authority, error) {
	path := filepath.Join(dir, "authority.json")
	data, err := os.ReadFile(path)
	if err == nil {
		var a Authority
		if err := json.Unmarshal(data, &a); err != nil {
			return nil, fmt.Errorf("parse authority.json: %w", err)
		}
		return &a, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read authority.json: %w", err)
	}

	now := time.Now().UnixMilli()
	a := &Authority{
		ControllerID:       controllerID,
		Epoch:              1,
		LastBumpedAt:       now,
		ActiveSessionEpoch: 1,
		SessionStartedAt:   now,
	}
	if err := SaveAuthority(dir, a); err != nil {
		return nil, err
	}
	return a, nil
}

// SaveAuthority writes authority.json atomically.
func SaveAuthority(dir string, a *Authority) error {
	path := filepath.Join(dir, "authority.json")
	out, _ := json.MarshalIndent(a, "", "  ")
	return AtomicWrite(path, out, 0644)
}

// BumpEpoch advances the authority epoch and returns the new value.
func BumpEpoch(a *Authority) {
	a.Epoch++
	a.LastBumpedAt = time.Now().UnixMilli()
}

// SyncSession sets the active session epoch to match the authority epoch.
func SyncSession(a *Authority) {
	a.ActiveSessionEpoch = a.Epoch
	a.SessionStartedAt = time.Now().UnixMilli()
}
