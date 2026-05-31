package config

import (
	"os"
	"testing"
)

func isolateConfigDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
}

func TestSaveLoad(t *testing.T) {
	isolateConfigDir(t)

	want := &Config{ClientID: "1234567890.9876543210"}
	if err := Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.ClientID != want.ClientID {
		t.Errorf("ClientID: got %q, want %q", got.ClientID, want.ClientID)
	}
}

func TestLoad_Missing(t *testing.T) {
	isolateConfigDir(t)

	c, err := Load()
	if err != nil {
		t.Fatalf("Load on missing file should not error: %v", err)
	}
	if c.ClientID != "" {
		t.Errorf("ClientID should be empty, got %q", c.ClientID)
	}
}

func TestSave_FilePermissions(t *testing.T) {
	isolateConfigDir(t)

	if err := Save(&Config{ClientID: "test"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	p, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("file permissions: got %04o, want 0600", perm)
	}
}
