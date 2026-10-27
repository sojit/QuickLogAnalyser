package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenTestRootDoesNotWriteUserPreference(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("LOCALAPPDATA", cfg)
	t.Setenv("HOME", cfg)
	t.Setenv("XDG_CONFIG_HOME", cfg)

	root, err := OpenTestRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	// Preference file under normal config dir should remain absent.
	matches, _ := filepath.Glob(filepath.Join(cfg, "**", "storage.json"))
	for _, m := range matches {
		t.Fatalf("unexpected preference write: %s", m)
	}
	// Also check common Windows layout if DefaultConfigDir uses LOCALAPPDATA\QuickAnalyser
	p := filepath.Join(cfg, "QuickAnalyser", "storage.json")
	if _, err := os.Stat(p); err == nil {
		t.Fatalf("wrote user preference at %s", p)
	}
}
