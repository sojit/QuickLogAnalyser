package diag

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"quickanalyser/internal/appinfo"
)

const (
	defaultBundleLogBudget = 2 << 20 // total sanitized log bytes in bundle
	maxBundleLogFiles      = 3
)

// CreateBundle writes a redacted local support zip under diagnostics/bundles/.
// Excludes snapshots, evidence text, databases, session material and raw queries.
func (l *Logger) CreateBundle() (path string, err error) {
	status := l.ExportSafeStatus()
	l.mu.Lock()
	dir := l.dir
	maxBundles := l.cfg.MaxBundles
	l.mu.Unlock()
	if dir == "" {
		return "", fmt.Errorf("diagnostics root not attached")
	}
	bundleDir := filepath.Join(dir, "bundles")
	if err := os.MkdirAll(bundleDir, 0o700); err != nil {
		return "", err
	}
	pruneErr := pruneBundles(bundleDir, maxBundles)

	l.mu.Lock()
	inst := l.instanceID
	if len(inst) < 8 {
		inst = inst + "00000000"
	}
	l.mu.Unlock()
	stamp := time.Now().UTC().Format("20060102-150405.000000000")
	base := fmt.Sprintf("qa-support-%s-%s", stamp, inst[:8])
	tmpPath := filepath.Join(bundleDir, base+".zip.partial")
	finalPath := filepath.Join(bundleDir, base+".zip")

	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if pruneErr != nil {
			return "", fmt.Errorf("bundle create failed: %v (also prune: %w)", err, pruneErr)
		}
		return "", err
	}
	zw := zip.NewWriter(f)
	success := false
	defer func() {
		if !success {
			_ = zw.Close()
			_ = f.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	logPayload, sourceNames, truncated, logBytes, err := collectSanitizedLogs(dir, defaultBundleLogBudget, maxBundleLogFiles)
	if err != nil {
		return "", err
	}

	meta := map[string]any{
		"product":     appinfo.ProductName,
		"signature":   appinfo.DeveloperDisplay,
		"build":       appinfo.BuildIdentity(),
		"diagnostics": status,
		"included":    []string{"build.json", "diagnostics-status.json", "app-logs-sanitized.jsonl"},
		"excluded":    []string{"snapshots", "evidence", "results.sqlite", "session tokens", "search queries", "heap dumps", "raw local paths"},
		"created_utc": time.Now().UTC().Format(time.RFC3339),
		"note":        "Local redacted bundle only — no automatic upload",
		"log_files":   sourceNames,
		"log_bytes":   logBytes,
		"truncated":  truncated,
	}
	if pruneErr != nil {
		meta["prune_error"] = "bundle_retention_prune_failed"
		truncated = true
		meta["truncated"] = true
	}
	if err := writeZipJSON(zw, "bundle-manifest.json", meta); err != nil {
		return "", err
	}
	if err := writeZipJSON(zw, "build.json", appinfo.BuildIdentity()); err != nil {
		return "", err
	}
	if err := writeZipJSON(zw, "diagnostics-status.json", status); err != nil {
		return "", err
	}
	if err := writeZipBytes(zw, "app-logs-sanitized.jsonl", logPayload); err != nil {
		return "", err
	}

	if err := zw.Close(); err != nil {
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	success = true
	// Post-create prune so the new ZIP is counted; propagate failure.
	if err := pruneBundles(bundleDir, maxBundles); err != nil {
		return finalPath, fmt.Errorf("bundle written to %s but retention prune failed: %w", finalPath, err)
	}
	if pruneErr != nil {
		return finalPath, fmt.Errorf("bundle written to %s but prior retention prune failed: %w", finalPath, pruneErr)
	}
	return finalPath, nil
}

func pruneBundles(dir string, max int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	type item struct {
		name string
		mod  time.Time
	}
	var zips []item
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".zip") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		zips = append(zips, item{name: e.Name(), mod: info.ModTime()})
	}
	sort.Slice(zips, func(i, j int) bool { return zips[i].mod.Before(zips[j].mod) })
	var firstErr error
	for len(zips) >= max && len(zips) > 0 {
		if err := os.Remove(filepath.Join(dir, zips[0].name)); err != nil && firstErr == nil {
			firstErr = err
		}
		zips = zips[1:]
	}
	return firstErr
}

// collectSanitizedLogs returns complete sanitized JSONL lines within budget.
func collectSanitizedLogs(dir string, budget int64, maxFiles int) (payload []byte, names []string, truncated bool, n int64, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, false, 0, err
	}
	type item struct {
		name string
		mod  time.Time
	}
	var logs []item
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		logs = append(logs, item{name: e.Name(), mod: info.ModTime()})
	}
	sort.Slice(logs, func(i, j int) bool { return logs[i].mod.After(logs[j].mod) })
	if len(logs) > maxFiles {
		logs = logs[:maxFiles]
		truncated = true
	}

	var lines [][]byte
	var used int64
	for _, lg := range logs {
		recs, fileTrunc, err := readCompleteSanitizedRecords(filepath.Join(dir, lg.name), budget-used)
		if err != nil {
			truncated = true
			continue
		}
		if fileTrunc {
			truncated = true
		}
		if len(recs) > 0 {
			names = append(names, lg.name)
		}
		for _, r := range recs {
			lines = append(lines, r)
			used += int64(len(r) + 1)
			if used >= budget {
				truncated = true
				break
			}
		}
		if used >= budget {
			break
		}
	}
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	var buf bytes.Buffer
	for _, ln := range lines {
		buf.Write(ln)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), names, truncated, used, nil
}

func readCompleteSanitizedRecords(path string, budget int64) (recs [][]byte, truncated bool, err error) {
	if budget <= 0 {
		return nil, true, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	// Byte-bounded ring of newest complete records — never retain the whole file.
	var ring [][]byte
	var used int64
	var seen int
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		safe, ok := resanitizeRecord(line)
		if !ok {
			continue // exclude unsafe/unparseable legacy
		}
		seen++
		need := int64(len(safe) + 1)
		if need > budget {
			truncated = true
			continue // skip oversized single record rather than cutting JSON
		}
		ring = append(ring, safe)
		used += need
		for used > budget && len(ring) > 0 {
			used -= int64(len(ring[0]) + 1)
			ring = ring[1:]
			truncated = true
		}
	}
	if err := sc.Err(); err != nil {
		return nil, true, err
	}
	if seen > len(ring) {
		truncated = true
	}
	// Return newest-first for caller aggregation (ring is oldest→newest).
	for i := len(ring) - 1; i >= 0; i-- {
		recs = append(recs, ring[i])
	}
	return recs, truncated, nil
}

func resanitizeRecord(line []byte) ([]byte, bool) {
	var raw map[string]any
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil, false
	}
	code, _ := raw["code"].(string)
	level, _ := raw["level"].(string)
	ts, _ := raw["ts"].(string)
	out := map[string]any{
		"ts":             ts,
		"level":          level,
		"code":           sanitizeCode(code),
		"app_version":    raw["app_version"],
		"instance_id":    raw["instance_id"],
		"schema_version": raw["schema_version"],
	}
	attrs := map[string]any{}
	for k, v := range raw {
		lk := strings.ToLower(k)
		if _, env := envelopeKeys[lk]; env {
			continue
		}
		attrs[lk] = v
	}
	proj, dropped := projectAttrs(attrs)
	for k, v := range proj {
		out[k] = v
	}
	if dropped > 0 {
		out["dropped_fields"] = dropped
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, false
	}
	return b, true
}

func writeZipJSON(zw *zip.Writer, name string, v any) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func writeZipBytes(zw *zip.Writer, name string, data []byte) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}
