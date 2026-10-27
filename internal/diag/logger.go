// Package diag provides bounded, redacted operational logging for QuickAnalyser.
// These are application diagnostics, not uploaded device-log findings.
package diag

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"quickanalyser/internal/appinfo"
	"quickanalyser/internal/platform"
)

const (
	DefaultMaxFiles      = 5
	DefaultMaxFileBytes  = 10 << 20 // 10 MiB
	DefaultMaxTotalBytes = 50 << 20 // 50 MiB across JSONL files
	DefaultMaxEvent      = 8 << 10  // 8 KiB
	DefaultDebugTTL      = 15 * time.Minute
	DefaultMaxBundles    = 5
	SchemaVersion        = 1
	minMaxEvent          = 256 // enough for envelope + truncation summary
)

// Config controls rotation and DEBUG lifetime.
type Config struct {
	MaxFiles      int
	MaxFileBytes  int64
	MaxTotalBytes int64
	MaxEvent      int
	DebugTTL      time.Duration
	MaxBundles    int
}

func DefaultConfig() Config {
	return Config{
		MaxFiles:      DefaultMaxFiles,
		MaxFileBytes:  DefaultMaxFileBytes,
		MaxTotalBytes: DefaultMaxTotalBytes,
		MaxEvent:      DefaultMaxEvent,
		DebugTTL:      DefaultDebugTTL,
		MaxBundles:    DefaultMaxBundles,
	}
}

// Logger is the process-wide diagnostics sink.
type Logger struct {
	mu               sync.Mutex
	cfg              Config
	dir              string
	instanceID       string
	level            slog.Level
	debugUntil       time.Time
	file             *os.File
	filePath         string
	fileSize         int64
	seq              uint64
	health           string
	healthErr        string
	dropped          int64
	stderrFallback   bool
	lastStderrFail   time.Time
	stderrFailCount  int
	cachedBytes      int64
	cachedBytesAt    time.Time
	rotationFailures int64
}

var (
	globalMu sync.Mutex
	global   *Logger
)

// Instance returns the process logger (stderr-only until AttachRoot).
func Instance() *Logger {
	globalMu.Lock()
	defer globalMu.Unlock()
	if global == nil {
		global = NewWithConfig(DefaultConfig())
	}
	return global
}

// Configure replaces the process logger configuration before AttachRoot when possible.
func Configure(cfg Config) *Logger {
	globalMu.Lock()
	defer globalMu.Unlock()
	if global != nil && global.file != nil {
		global.cfg = normalizeConfig(cfg)
		return global
	}
	global = NewWithConfig(cfg)
	return global
}

func normalizeConfig(cfg Config) Config {
	if cfg.MaxFiles <= 0 {
		cfg.MaxFiles = DefaultMaxFiles
	}
	if cfg.MaxFileBytes <= 0 {
		cfg.MaxFileBytes = DefaultMaxFileBytes
	}
	if cfg.MaxTotalBytes <= 0 {
		cfg.MaxTotalBytes = DefaultMaxTotalBytes
	}
	if cfg.MaxEvent < minMaxEvent {
		cfg.MaxEvent = minMaxEvent
	}
	if int64(cfg.MaxEvent) > cfg.MaxFileBytes {
		cfg.MaxEvent = int(cfg.MaxFileBytes)
		if cfg.MaxEvent < minMaxEvent {
			cfg.MaxEvent = minMaxEvent
		}
	}
	if cfg.DebugTTL <= 0 {
		cfg.DebugTTL = DefaultDebugTTL
	}
	if cfg.MaxBundles <= 0 {
		cfg.MaxBundles = DefaultMaxBundles
	}
	return cfg
}

// NewWithConfig constructs a logger that starts on stderr until AttachRoot.
func NewWithConfig(cfg Config) *Logger {
	cfg = normalizeConfig(cfg)
	l := &Logger{
		cfg:        cfg,
		instanceID: newInstanceID(),
		level:      slog.LevelInfo,
		health:     "stderr_only",
	}
	_ = slog.New(slog.NewJSONHandler(io.Discard, nil))
	return l
}

func newInstanceID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// AttachRoot switches the sink to <root>/diagnostics with rotation.
func (l *Logger) AttachRoot(root string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	dir := filepath.Join(root, "diagnostics")
	if err := platform.EnsureDir(dir); err != nil {
		l.health = "degraded"
		l.healthErr = "ensure_dir"
		l.stderrFallback = true
		return err
	}
	_ = platform.EnsureDir(filepath.Join(dir, "bundles"))
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
		l.filePath = ""
	}
	l.dir = dir
	pruneFailed := false
	if err := l.pruneLocked(); err != nil {
		l.rotationFailures++
		pruneFailed = true
		l.healthErr = "prune_failed"
		// continue — still try to open a file
	}
	if err := l.openNewFileLocked(); err != nil {
		l.health = "degraded"
		l.healthErr = "open_failed"
		l.stderrFallback = true
		return err
	}
	// Enforce count/total immediately after creating the new active file (cold restart).
	if err := l.pruneLocked(); err != nil {
		l.rotationFailures++
		pruneFailed = true
		l.healthErr = "prune_failed"
	}
	if pruneFailed {
		l.health = "degraded"
	} else {
		l.health = "ok"
		l.healthErr = ""
	}
	l.stderrFallback = false
	l.invalidateBytesCacheLocked()
	return nil
}

func (l *Logger) openNewFileLocked() error {
	if l.dir == "" {
		return fmt.Errorf("diagnostics dir unset")
	}
	l.seq++
	// Collision-free even within the same second: nanos + instance + seq.
	name := fmt.Sprintf("qa-%s-%s-%04d.jsonl",
		time.Now().UTC().Format("20060102-150405.000000000"),
		l.instanceID[:8],
		l.seq%10000,
	)
	path := filepath.Join(l.dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		// Extremely unlikely collision: bump seq and retry once.
		l.seq++
		name = fmt.Sprintf("qa-%s-%s-%04d.jsonl",
			time.Now().UTC().Format("20060102-150405.000000000"),
			l.instanceID[:8],
			l.seq%10000,
		)
		path = filepath.Join(l.dir, name)
		f, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
	}
	l.file = f
	l.filePath = path
	l.fileSize = 0
	return nil
}

// SetLevel sets the base level (INFO or DEBUG). DEBUG starts a timed session.
func (l *Logger) SetLevel(level string) {
	if strings.ToLower(strings.TrimSpace(level)) == "debug" {
		l.EnableDebug(0)
		return
	}
	l.DisableDebug()
}

// EnableDebug turns on DEBUG for ttl. If ttl <= 0, cfg.DebugTTL is used.
func (l *Logger) EnableDebug(ttl time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if ttl <= 0 {
		ttl = l.cfg.DebugTTL
	}
	l.level = slog.LevelDebug
	l.debugUntil = time.Now().Add(ttl)
}

// DisableDebug forces INFO.
func (l *Logger) DisableDebug() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = slog.LevelInfo
	l.debugUntil = time.Time{}
}

func (l *Logger) effectiveLevel() slog.Level {
	if l.level == slog.LevelDebug {
		if time.Now().After(l.debugUntil) && !l.debugUntil.IsZero() {
			l.level = slog.LevelInfo
			l.debugUntil = time.Time{}
		}
	}
	return l.level
}

func (l *Logger) invalidateBytesCacheLocked() {
	l.cachedBytesAt = time.Time{}
}

// diskBytesCached returns approximate diagnostics bytes without holding lock during Walk.
func (l *Logger) diskBytesCached() int64 {
	l.mu.Lock()
	dir := l.dir
	cached := l.cachedBytes
	at := l.cachedBytesAt
	l.mu.Unlock()
	if dir == "" {
		return 0
	}
	if !at.IsZero() && time.Since(at) < 2*time.Second {
		return cached
	}
	var size int64
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	l.mu.Lock()
	l.cachedBytes = size
	l.cachedBytesAt = time.Now()
	l.mu.Unlock()
	return size
}

// Status reports UI/CLI diagnostics health.
func (l *Logger) Status() map[string]any {
	l.mu.Lock()
	_ = l.effectiveLevel()
	level := "INFO"
	if l.level == slog.LevelDebug {
		level = "DEBUG"
	}
	out := map[string]any{
		"level":             level,
		"directory":         l.dir,
		"health":            l.health,
		"health_error":      l.healthErr,
		"instance_id":       l.instanceID,
		"schema_version":    SchemaVersion,
		"dropped_events":    l.dropped,
		"max_files":         l.cfg.MaxFiles,
		"max_file_bytes":    l.cfg.MaxFileBytes,
		"max_total_bytes":   l.cfg.MaxTotalBytes,
		"max_event_bytes":   l.cfg.MaxEvent,
		"debug_ttl_sec":     int(l.cfg.DebugTTL.Seconds()),
		"stderr_fallback":   l.stderrFallback,
		"app_version":       appinfo.Version(),
		"rotation_failures": l.rotationFailures,
	}
	if l.level == slog.LevelDebug && !l.debugUntil.IsZero() {
		out["debug_expires_utc"] = l.debugUntil.UTC().Format(time.RFC3339)
	}
	l.mu.Unlock()
	out["bytes_on_disk"] = l.diskBytesCached()
	return out
}

// ExportSafeStatus is a redacted projection for support bundles (no local paths / raw errors).
func (l *Logger) ExportSafeStatus() map[string]any {
	st := l.Status()
	return map[string]any{
		"level":             st["level"],
		"health":            st["health"],
		"health_code":       st["health_error"], // code-like, already short
		"instance_id":       st["instance_id"],
		"schema_version":    st["schema_version"],
		"dropped_events":    st["dropped_events"],
		"bytes_on_disk":     st["bytes_on_disk"],
		"max_files":         st["max_files"],
		"max_file_bytes":    st["max_file_bytes"],
		"max_total_bytes":   st["max_total_bytes"],
		"max_event_bytes":   st["max_event_bytes"],
		"debug_ttl_sec":     st["debug_ttl_sec"],
		"stderr_fallback":   st["stderr_fallback"],
		"app_version":       st["app_version"],
		"rotation_failures": st["rotation_failures"],
		"debug_expires_utc": st["debug_expires_utc"],
	}
}

// Event writes one structured diagnostic record.
func (l *Logger) Event(level slog.Level, code string, attrs map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if level < l.effectiveLevel() {
		return
	}
	attrs, droppedAttrs := projectAttrs(attrs)
	rec := map[string]any{
		"ts":             time.Now().UTC().Format(time.RFC3339Nano),
		"level":          level.String(),
		"code":           sanitizeCode(code),
		"app_version":    appinfo.Version(),
		"instance_id":    l.instanceID,
		"schema_version": SchemaVersion,
	}
	for k, v := range attrs {
		rec[k] = v
	}
	if droppedAttrs > 0 {
		rec["dropped_fields"] = droppedAttrs
	}
	b, err := encodeBounded(rec, l.cfg.MaxEvent)
	if err != nil {
		l.dropped++
		return
	}
	b = append(b, '\n')
	if err := l.writeLocked(b); err != nil {
		l.dropped++
		l.health = "degraded"
		l.healthErr = "write_failed"
		l.reportStderrFailLocked(err)
	}
}

// encodeBounded marshals a valid JSON object that fits within max bytes.
func encodeBounded(rec map[string]any, max int) ([]byte, error) {
	if max < minMaxEvent {
		max = minMaxEvent
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return nil, err
	}
	if len(b) <= max {
		return b, nil
	}
	// Drop optional (non-envelope) fields deterministically by key name, longest first.
	optional := make([]string, 0, len(rec))
	for k := range rec {
		if _, env := envelopeKeys[k]; env {
			continue
		}
		if k == "truncated" || k == "dropped_fields" {
			continue
		}
		optional = append(optional, k)
	}
	sort.Slice(optional, func(i, j int) bool {
		return len(fmt.Sprint(rec[optional[i]])) > len(fmt.Sprint(rec[optional[j]]))
	})
	dropped := 0
	for _, k := range optional {
		delete(rec, k)
		dropped++
		rec["truncated"] = true
		rec["dropped_fields"] = dropped
		b, err = json.Marshal(rec)
		if err != nil {
			return nil, err
		}
		if len(b) <= max {
			return b, nil
		}
	}
	// Last resort: envelope-only truncation summary.
	summary := map[string]any{
		"ts":             rec["ts"],
		"level":          rec["level"],
		"code":           rec["code"],
		"app_version":    rec["app_version"],
		"instance_id":    rec["instance_id"],
		"schema_version": rec["schema_version"],
		"truncated":     true,
		"dropped_fields": dropped + len(optional),
		"event_bytes":    len(b),
	}
	b, err = json.Marshal(summary)
	if err != nil {
		return nil, err
	}
	if len(b) > max {
		// Should not happen with minMaxEvent; still ensure validity by shrinking code.
		summary["code"] = "truncated"
		b, err = json.Marshal(summary)
		if err != nil {
			return nil, err
		}
	}
	return b, nil
}

func (l *Logger) reportStderrFailLocked(err error) {
	now := time.Now()
	if !l.lastStderrFail.IsZero() && now.Sub(l.lastStderrFail) < 5*time.Second {
		l.stderrFailCount++
		return
	}
	extra := ""
	if l.stderrFailCount > 0 {
		extra = fmt.Sprintf(" (+%d suppressed)", l.stderrFailCount)
		l.stderrFailCount = 0
	}
	l.lastStderrFail = now
	fmt.Fprintf(os.Stderr, "quickanalyser diagnostics: %v%s\n", err, extra)
}

func (l *Logger) Info(code string, attrs map[string]any)  { l.Event(slog.LevelInfo, code, attrs) }
func (l *Logger) Warn(code string, attrs map[string]any)  { l.Event(slog.LevelWarn, code, attrs) }
func (l *Logger) Error(code string, attrs map[string]any) { l.Event(slog.LevelError, code, attrs) }
func (l *Logger) Debug(code string, attrs map[string]any) { l.Event(slog.LevelDebug, code, attrs) }

func (l *Logger) writeLocked(b []byte) error {
	if l.file == nil || l.dir == "" {
		_, err := os.Stderr.Write(b)
		return err
	}
	if l.fileSize+int64(len(b)) > l.cfg.MaxFileBytes {
		old := l.file
		oldPath := l.filePath
		l.file = nil
		l.filePath = ""
		if old != nil {
			if err := old.Close(); err != nil {
				l.rotationFailures++
				l.health = "degraded"
				l.healthErr = "rotate_close_failed"
			}
		}
		if err := l.openNewFileLocked(); err != nil {
			l.rotationFailures++
			_, _ = os.Stderr.Write(b)
			return err
		}
		// Never prune the newly opened active file.
		_ = oldPath
		if err := l.pruneLocked(); err != nil {
			l.rotationFailures++
			l.health = "degraded"
			l.healthErr = "prune_failed"
		}
		l.invalidateBytesCacheLocked()
	}
	n, err := l.file.Write(b)
	l.fileSize += int64(n)
	return err
}

type logFileInfo struct {
	name string
	path string
	mod  time.Time
	size int64
}

func (l *Logger) listJSONLLocked() ([]logFileInfo, error) {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return nil, err
	}
	var files []logFileInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, logFileInfo{
			name: e.Name(),
			path: filepath.Join(l.dir, e.Name()),
			mod:  info.ModTime(),
			size: info.Size(),
		})
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].mod.Before(files[j].mod)
	})
	return files, nil
}

func (l *Logger) pruneLocked() error {
	files, err := l.listJSONLLocked()
	if err != nil {
		return err
	}
	var firstErr error
	removeOldest := func() {
		for i := 0; i < len(files); i++ {
			if files[i].path == l.filePath {
				continue // never prune active
			}
			if err := os.Remove(files[i].path); err != nil && firstErr == nil {
				firstErr = err
			}
			files = append(files[:i], files[i+1:]...)
			return
		}
	}
	for len(files) > l.cfg.MaxFiles {
		before := len(files)
		removeOldest()
		if len(files) == before {
			break
		}
	}
	var total int64
	for _, f := range files {
		total += f.size
	}
	for total > l.cfg.MaxTotalBytes && len(files) > 1 {
		var removed int64
		for i := 0; i < len(files); i++ {
			if files[i].path == l.filePath {
				continue
			}
			removed = files[i].size
			if err := os.Remove(files[i].path); err != nil && firstErr == nil {
				firstErr = err
			}
			files = append(files[:i], files[i+1:]...)
			break
		}
		if removed == 0 {
			break
		}
		total -= removed
	}
	return firstErr
}

// Dir returns the diagnostics directory (may be empty before AttachRoot).
func (l *Logger) Dir() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.dir
}

// Close flushes the active file.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		err := l.file.Close()
		l.file = nil
		return err
	}
	return nil
}
