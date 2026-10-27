package diag

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func verificationReadLogs(t *testing.T, root string) []byte {
	t.Helper()
	var out []byte
	dir := filepath.Join(root, "diagnostics")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, b...)
	}
	return out
}

func TestVerificationNewURLCredentialsAreRedacted(t *testing.T) {
	root := t.TempDir()
	l := NewWithConfig(DefaultConfig())
	if err := l.AttachRoot(root); err != nil {
		t.Fatal(err)
	}
	const dummy = "AUDIT_CRED_42"
	l.Info("audit.url", map[string]any{"url": "https://audit-user:" + dummy + "@example.invalid/service?mode=x"})
	_ = l.Close()
	data := verificationReadLogs(t, root)
	if strings.Contains(string(data), dummy) {
		t.Fatal("URL userinfo dummy credential persisted in diagnostics")
	}
	if strings.Contains(string(data), "audit-user:") {
		t.Fatal("URL userinfo username:password form persisted")
	}
}

func TestVerificationNewRestartKeepsFileCountBound(t *testing.T) {
	root := t.TempDir()
	cfg := Config{MaxFiles: 2, MaxFileBytes: 1024, MaxTotalBytes: 2048, MaxEvent: 512}
	for i := 0; i < 3; i++ {
		l := NewWithConfig(cfg)
		if err := l.AttachRoot(root); err != nil {
			t.Fatal(err)
		}
		_ = l.Close()
	}
	files, err := os.ReadDir(filepath.Join(root, "diagnostics"))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".jsonl") {
			count++
		}
	}
	if count > cfg.MaxFiles {
		t.Fatalf("restart exceeds log-file count: have=%d max=%d", count, cfg.MaxFiles)
	}
}

func TestURLUserinfoRedactedInBundleExport(t *testing.T) {
	root := t.TempDir()
	l := NewWithConfig(DefaultConfig())
	if err := l.AttachRoot(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	const dummy = "BUNDLE_CRED_99"
	// Write a legacy-looking line then re-export through bundle sanitizer path.
	l.Info("audit.url", map[string]any{"url": "https://u:" + dummy + "@example.invalid/x"})
	path, err := l.CreateBundle()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), dummy) {
		t.Fatal("URL credential leaked into support bundle")
	}
}
