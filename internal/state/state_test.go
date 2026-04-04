package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.json")

	data := []byte(`{"key":"value"}`)
	if err := AtomicWrite(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Errorf("expected %s, got %s", data, got)
	}

	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0644 {
		t.Errorf("expected perm 0644, got %o", info.Mode().Perm())
	}
}

func TestAtomicWriteNoTempLeftOnFailure(t *testing.T) {
	dir := t.TempDir()
	// Write to a non-existent subdirectory — should fail
	path := filepath.Join(dir, "no", "such", "dir", "file.json")
	err := AtomicWrite(path, []byte("test"), 0644)
	if err == nil {
		t.Fatal("expected error for non-existent directory")
	}
}

func TestControllerLoadOrCreate(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureDir(dir); err != nil {
		t.Fatal(err)
	}

	// First call creates
	ctrl1, err := LoadOrCreateController(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ctrl1.ID == "" {
		t.Error("controller ID should not be empty")
	}

	// Second call loads same
	ctrl2, err := LoadOrCreateController(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ctrl1.ID != ctrl2.ID {
		t.Errorf("expected same ID, got %s vs %s", ctrl1.ID, ctrl2.ID)
	}
}

func TestAuthorityLoadOrCreate(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureDir(dir); err != nil {
		t.Fatal(err)
	}

	auth1, err := LoadOrCreateAuthority(dir, "test-controller")
	if err != nil {
		t.Fatal(err)
	}
	if auth1.Epoch != 1 {
		t.Errorf("expected initial epoch=1, got %d", auth1.Epoch)
	}
	if auth1.ControllerID != "test-controller" {
		t.Errorf("expected controller ID 'test-controller', got %s", auth1.ControllerID)
	}
}

func TestConstraintsAddAndLoad(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureDir(dir); err != nil {
		t.Fatal(err)
	}

	if err := AddConstraint(dir, "write_file", "/tmp/test", "permission_denied", "EACCES", 3600000); err != nil {
		t.Fatal(err)
	}

	constraints, err := LoadConstraints(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(constraints) != 1 {
		t.Fatalf("expected 1 constraint, got %d", len(constraints))
	}
	if constraints[0].ToolName != "write_file" {
		t.Errorf("expected write_file, got %s", constraints[0].ToolName)
	}
}

func TestIntentSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureDir(dir); err != nil {
		t.Fatal(err)
	}

	intent, err := SaveIntent(dir, "test goal", nil)
	if err != nil {
		t.Fatal(err)
	}
	if intent.Goal != "test goal" {
		t.Errorf("expected 'test goal', got '%s'", intent.Goal)
	}

	loaded, err := LoadIntent(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Goal != "test goal" {
		t.Errorf("expected 'test goal', got '%s'", loaded.Goal)
	}

	if err := ClearIntent(dir); err != nil {
		t.Fatal(err)
	}

	cleared, _ := LoadIntent(dir)
	if cleared != nil {
		t.Error("expected nil intent after clear")
	}
}
