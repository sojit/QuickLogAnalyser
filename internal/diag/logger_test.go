package diag

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBurstRotationUniqueFilesAndSizeCap(t *testing.T) {
	dir := t.TempDir()
	l := NewWithConfig(Config{MaxFiles: 4, MaxFileBytes: 1024, MaxTotalBytes: 8 << 10, MaxEvent: 256, DebugTTL: time.Minute})
	if err := l.AttachRoot(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	payload := strings.Repeat("x", 180)
	for i := 0; i < 80; i++ {
		l.Info("test.burst", map[string]any{"job_id": "abc", "n": i, "reason_code": payload[:40]})
	}
	_ = l.Close()
	var files []string
	var maxSize int64
	_ = filepath.Walk(filepath.Join(dir, "diagnostics"), func(path string, info os.FileInfo, err error) error {
		if err == nil && info != nil && strings.HasSuffix(info.Name(), ".jsonl") {
			files = append(files, path)
			if info.Size() > maxSize {
				maxSize = info.Size()
			}
			if info.Size() > 1024+512 { // allow one event past soft edge before rotate
				t.Fatalf("file %s size %d exceeds cap", info.Name(), info.Size())
			}
		}
		return nil
	})
	if len(files) < 2 {
		t.Fatalf("expected multiple rotated files in burst, got %d", len(files))
	}
	if len(files) > 4 {
		t.Fatalf("max files exceeded: %d", len(files))
	}
	// Unique names
	seen := map[string]bool{}
	for _, f := range files {
		base := filepath.Base(f)
		if seen[base] {
			t.Fatalf("duplicate filename %s", base)
		}
		seen[base] = true
	}
}

func TestOversizedEventRemainsValidJSON(t *testing.T) {
	dir := t.TempDir()
	l := NewWithConfig(Config{MaxFiles: 2, MaxFileBytes: 1 << 20, MaxEvent: 512, DebugTTL: time.Minute})
	if err := l.AttachRoot(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	big := strings.Repeat("語", 400) // multibyte
	l.Info("test.oversize", map[string]any{
		"job_id":      "jid",
		"reason_code": "ok",
		"stage":       big, // will be sanitized/truncated before encode
		"n":           1,
	})
	_ = l.Close()
	var lines int
	_ = filepath.Walk(filepath.Join(dir, "diagnostics"), func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || !strings.HasSuffix(info.Name(), ".jsonl") {
			return nil
		}
		b, _ := os.ReadFile(path)
		for _, line := range bytes.Split(b, []byte("\n")) {
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			lines++
			var m map[string]any
			if err := json.Unmarshal(line, &m); err != nil {
				t.Fatalf("invalid JSONL (%d bytes): %v\n%s", len(line), err, line)
			}
			if len(line) > 512 {
				t.Fatalf("event longer than MaxEvent: %d", len(line))
			}
		}
		return nil
	})
	if lines < 1 {
		t.Fatal("expected at least one record")
	}
}

func TestNestedSecretsRedactedFromLogsAndBundle(t *testing.T) {
	dir := t.TempDir()
	l := NewWithConfig(DefaultConfig())
	if err := l.AttachRoot(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	l.Info("test.secret", map[string]any{
		"job_id": "j1",
		"details": map[string]any{ // nested — must not pass through allowlist
			"password": "dummy-password-value",
			"token":    "dummy-token",
		},
		"reason_code": "C:\\Users\\secret\\private\\path",
		"url":         "http://127.0.0.1:9/?s=secrettoken",
	})
	path, err := l.CreateBundle()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "diagnostics", listFirstJSONL(t, dir)))
	blob := string(raw)
	if strings.Contains(blob, "dummy-password-value") || strings.Contains(blob, "dummy-token") {
		t.Fatal("nested secret leaked into JSONL")
	}
	if strings.Contains(blob, "secrettoken") {
		t.Fatal("url token leaked")
	}
	if strings.Contains(blob, `C:\Users`) || strings.Contains(blob, "private\\path") {
		t.Fatal("private path leaked into reason_code")
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var statusJSON, logsJSON string
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		switch f.Name {
		case "diagnostics-status.json":
			statusJSON = string(b)
		case "app-logs-sanitized.jsonl":
			logsJSON = string(b)
		}
	}
	if strings.Contains(statusJSON, dir) || strings.Contains(statusJSON, "Users\\") {
		t.Fatal("export status contains local path")
	}
	if strings.Contains(logsJSON, "dummy-password-value") || strings.Contains(logsJSON, "dummy-token") {
		t.Fatal("bundle contains nested secret")
	}
	// Every log line valid JSON
	for _, line := range strings.Split(logsJSON, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bundle log invalid JSON: %v", err)
		}
	}
}

func listFirstJSONL(t *testing.T, root string) string {
	t.Helper()
	var name string
	_ = filepath.Walk(filepath.Join(root, "diagnostics"), func(path string, info os.FileInfo, err error) error {
		if err == nil && info != nil && strings.HasSuffix(info.Name(), ".jsonl") && name == "" {
			name = info.Name()
		}
		return nil
	})
	if name == "" {
		t.Fatal("no jsonl")
	}
	return name
}

func TestRotationAndRedaction(t *testing.T) {
	dir := t.TempDir()
	l := NewWithConfig(Config{MaxFiles: 3, MaxFileBytes: 400, MaxEvent: 256, DebugTTL: time.Minute})
	if err := l.AttachRoot(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	for i := 0; i < 40; i++ {
		l.Info("test.event", map[string]any{
			"job_id": "abc",
			"url":    "http://127.0.0.1:9/?s=secrettoken",
			"n":      i,
		})
	}
	_ = l.Close()
	var count int
	_ = filepath.Walk(filepath.Join(dir, "diagnostics"), func(path string, info os.FileInfo, err error) error {
		if err == nil && info != nil && strings.HasSuffix(info.Name(), ".jsonl") {
			count++
			b, _ := os.ReadFile(path)
			if strings.Contains(string(b), "secrettoken") {
				t.Fatalf("session token leaked in %s", path)
			}
		}
		return nil
	})
	if count < 1 {
		t.Fatal("expected rotated jsonl files")
	}
	if count > 3 {
		t.Fatalf("max files exceeded: %d", count)
	}
}

func TestDebugExpiry(t *testing.T) {
	l := NewWithConfig(Config{DebugTTL: 20 * time.Millisecond, MaxFiles: 2, MaxFileBytes: 1 << 20, MaxEvent: 1024})
	dir := t.TempDir()
	if err := l.AttachRoot(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	l.SetLevel("debug")
	l.Debug("dbg.a", map[string]any{"job_id": "x"})
	time.Sleep(40 * time.Millisecond)
	st := l.Status()
	if st["level"] != "INFO" {
		t.Fatalf("expected DEBUG to expire to INFO, got %v", st["level"])
	}
}

func TestDenySensitiveKeys(t *testing.T) {
	l := NewWithConfig(DefaultConfig())
	_ = l.AttachRoot(t.TempDir())
	t.Cleanup(func() { _ = l.Close() })
	l.Info("x", map[string]any{"session": "nope", "job_id": "ok"})
}

func TestCreateBundleNoDeadlock(t *testing.T) {
	l := NewWithConfig(DefaultConfig())
	dir := t.TempDir()
	if err := l.AttachRoot(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	l.Info("bundle.prep", map[string]any{"job_id": "j1"})
	path, err := l.CreateBundle()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestWriteFailureDegradesWithoutPanic(t *testing.T) {
	dir := t.TempDir()
	l := NewWithConfig(DefaultConfig())
	if err := l.AttachRoot(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	l.mu.Lock()
	path := l.filePath
	_ = l.file.Close()
	ro, err := os.Open(path)
	if err != nil {
		l.mu.Unlock()
		t.Fatal(err)
	}
	l.file = ro
	l.mu.Unlock()
	l.Info("disk.full.sim", map[string]any{"job_id": "j"})
	st := l.Status()
	if st["health"] != "degraded" {
		t.Fatalf("expected degraded health, got %v err=%v", st["health"], st["health_error"])
	}
	dropped, _ := st["dropped_events"].(int64)
	if dropped < 1 {
		t.Fatalf("expected dropped events, got %v", st["dropped_events"])
	}
	l.Info("after.failure", map[string]any{"job_id": "j"})
}

func TestEnableDebugCustomTTL(t *testing.T) {
	l := NewWithConfig(Config{DebugTTL: time.Hour, MaxFiles: 2, MaxFileBytes: 1 << 20, MaxEvent: 1024})
	_ = l.AttachRoot(t.TempDir())
	t.Cleanup(func() { _ = l.Close() })
	l.EnableDebug(30 * time.Millisecond)
	st := l.Status()
	if st["level"] != "DEBUG" {
		t.Fatalf("expected DEBUG, got %v", st["level"])
	}
	time.Sleep(50 * time.Millisecond)
	st = l.Status()
	if st["level"] != "INFO" {
		t.Fatalf("expected custom TTL expiry to INFO, got %v", st["level"])
	}
}

func TestEncodeBoundedNeverSlicesJSON(t *testing.T) {
	rec := map[string]any{
		"ts": "t", "level": "INFO", "code": "c", "app_version": "v", "instance_id": "i", "schema_version": 1,
		"job_id": "j", "reason_code": strings.Repeat("a", 2000),
	}
	b, err := encodeBounded(rec, 400)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("invalid: %v %s", err, b)
	}
	if m["truncated"] != true {
		t.Fatal("expected truncated flag")
	}
}
