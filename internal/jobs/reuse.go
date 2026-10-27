package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"quickanalyser/internal/appinfo"
	"quickanalyser/internal/classify"
	"quickanalyser/internal/config"
	"quickanalyser/internal/detection"
	"quickanalyser/internal/evidence"
	"quickanalyser/internal/profile"
	"quickanalyser/internal/storage"
	"quickanalyser/internal/store"
)

// CompatibleReuse describes a previously retained analysis matching an upload hash.
type CompatibleReuse struct {
	JobID        string `json:"job_id"`
	OriginalName string `json:"original_name"`
	State        string `json:"state"`
	ProductLabel string `json:"product_label"`
	SHA256       string `json:"sha256"`
	Compatible   bool   `json:"compatible"`
	Reason       string `json:"reason,omitempty"`
}

// VerifyBounds limits evidence integrity work without claiming full success when incomplete.
type VerifyBounds struct {
	MaxBytes int64
	MaxTime  time.Duration
}

func defaultVerifyBounds() VerifyBounds {
	return VerifyBounds{
		MaxBytes: 256 << 20, // 256 MiB streaming verify budget
		MaxTime:  30 * time.Second,
	}
}

// FindCompatibleByHash looks up a retained analysis with the same SHA-256 and compatible versions.
// Searches all hash matches for a valid compatible candidate; first stale/partial match does not win.
func (e *Engine) FindCompatibleByHash(sum string) (*CompatibleReuse, error) {
	sum = strings.ToLower(strings.TrimSpace(sum))
	if len(sum) != 64 {
		return nil, nil
	}
	entries, err := os.ReadDir(e.Root.JobsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	wantApp := appinfo.Version()
	wantRule := detection.RuleBundleVersion
	var firstNonCompat *CompatibleReuse
	for _, ent := range entries {
		if !ent.IsDir() || !storage.ValidJobID(ent.Name()) {
			continue
		}
		dir, err := e.Root.SafeJobDir(ent.Name())
		if err != nil {
			continue
		}
		st, err := store.Open(filepath.Join(dir, "results.sqlite"))
		if err != nil {
			continue
		}
		var id, name, state, sha, product, appVer, ruleVer, flags string
		err = st.DB.QueryRow(`SELECT id, original_name, state, COALESCE(sha256,''), COALESCE(product_label,''),
			COALESCE(app_version,''), COALESCE(rule_version,''), COALESCE(schema_flags,'') FROM jobs WHERE id=?`, ent.Name()).
			Scan(&id, &name, &state, &sha, &product, &appVer, &ruleVer, &flags)
		_ = st.Close()
		if err != nil || !strings.EqualFold(sha, sum) {
			continue
		}
		out := &CompatibleReuse{
			JobID: id, OriginalName: name, State: state, ProductLabel: product, SHA256: sha,
		}
		if reason := evaluateCompatibility(dir, id, state, appVer, ruleVer, flags, wantApp, wantRule, e.Limits, defaultVerifyBounds()); reason != "" {
			out.Compatible = false
			out.Reason = reason
			if firstNonCompat == nil {
				firstNonCompat = out
			}
			continue
		}
		out.Compatible = true
		out.Reason = "identical archive bytes with compatible versions and verified evidence"
		return out, nil
	}
	return firstNonCompat, nil
}

func evaluateCompatibility(jobDir, jobID, state, appVer, ruleVer, flags, wantApp, wantRule string, limits config.Limits, bounds VerifyBounds) string {
	if state != "completed" {
		if state == "partial" {
			return "saved analysis is partial; open labelled evidence or reanalyse explicitly"
		}
		return "saved analysis is " + state + "; open as labelled evidence or reanalyse"
	}
	snap := filepath.Join(jobDir, "input", "snapshot.bin")
	if _, err := os.Stat(snap); err != nil {
		return "missing input snapshot"
	}
	if _, err := os.Stat(filepath.Join(jobDir, "results.sqlite")); err != nil {
		return "missing results database"
	}
	if strings.TrimSpace(appVer) == "" {
		return "app version unknown on saved analysis"
	}
	if appVer != wantApp {
		return "app version differs"
	}
	if strings.TrimSpace(ruleVer) == "" {
		return "rule bundle version unknown on saved analysis"
	}
	if ruleVer != wantRule {
		return "rule bundle differs"
	}
	if strings.TrimSpace(flags) == "" {
		return "compatibility metadata unknown on saved analysis"
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(flags), &meta); err != nil {
		return "compatibility metadata malformed"
	}
	cv, _ := meta["classifier_version"].(string)
	if strings.TrimSpace(cv) == "" {
		return "classifier version unknown"
	}
	if cv != classify.Version {
		return "classifier version differs"
	}
	pv, _ := meta["profile_version"].(string)
	if strings.TrimSpace(pv) == "" {
		return "profile extraction version unknown; reanalyse to refresh identity"
	}
	if pv != profile.ExtractionVersion {
		return "profile extraction version differs; reanalyse to refresh identity"
	}
	ev, _ := meta["engine_version"].(string)
	if strings.TrimSpace(ev) == "" {
		return "engine version unknown on saved analysis"
	}
	if ev != appinfo.Version() {
		return "engine version differs"
	}
	par, _ := meta["parser_version"].(string)
	if strings.TrimSpace(par) == "" {
		return "parser version unknown on saved analysis"
	}
	if par != ParserVersion {
		return "parser version differs"
	}
	if err := compareSchemaVersion(meta["schema_version"]); err != "" {
		return err
	}
	if err := compareAnalysisSettings(meta["settings"], limits); err != "" {
		return err
	}
	if err := evidenceUsable(jobDir, jobID, bounds); err != nil {
		return err.Error()
	}
	return ""
}

func compareSchemaVersion(raw any) string {
	want := store.SchemaVersion
	switch v := raw.(type) {
	case nil:
		return "storage schema version unknown on saved analysis"
	case float64:
		if v != float64(int(v)) {
			return "storage schema version malformed (fractional)"
		}
		if int(v) != want {
			return "storage schema version differs"
		}
	case json.Number:
		i, err := v.Int64()
		if err != nil {
			return "storage schema version malformed"
		}
		if int(i) != want {
			return "storage schema version differs"
		}
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return "storage schema version unknown on saved analysis"
		}
		if strings.ContainsAny(s, ".eE") {
			return "storage schema version malformed"
		}
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 {
			return "storage schema version malformed"
		}
		if n != want {
			return "storage schema version differs"
		}
	case int:
		if v != want {
			return "storage schema version differs"
		}
	case int64:
		if int(v) != want {
			return "storage schema version differs"
		}
	default:
		return "storage schema version malformed"
	}
	return ""
}

func compareAnalysisSettings(raw any, limits config.Limits) string {
	want := analysisSettingsFingerprint(limits)
	gotMap, ok := raw.(map[string]any)
	if !ok || gotMap == nil {
		return "analysis settings unknown on saved analysis"
	}
	got := normalizeSettingsMap(gotMap)
	for k, wv := range want {
		gv, ok := got[k]
		if !ok {
			return "analysis settings missing " + k
		}
		if gv != wv {
			return "analysis settings differ (" + k + ")"
		}
	}
	return ""
}

func analysisSettingsFingerprint(limits config.Limits) map[string]int64 {
	return map[string]int64{
		"context_lines_max":     int64(limits.ContextLinesMax),
		"context_bytes_max":     int64(limits.ContextBytesMax),
		"max_member_text_bytes": int64(limits.MaxMemberTextBytes),
		"max_evidence_bytes":    int64(limits.MaxEvidenceBytes),
	}
}

func normalizeSettingsMap(m map[string]any) map[string]int64 {
	out := map[string]int64{}
	for k, v := range m {
		switch n := v.(type) {
		case float64:
			out[k] = int64(n)
		case int64:
			out[k] = n
		case int:
			out[k] = int64(n)
		case json.Number:
			i, _ := n.Int64()
			out[k] = i
		}
	}
	return out
}

type evidenceManifestEntry struct {
	SourceID string
	Cache    string
	SHA      string
	Bytes    int64
}

func evidenceUsable(jobDir, jobID string, bounds VerifyBounds) error {
	st, err := store.Open(filepath.Join(jobDir, "results.sqlite"))
	if err != nil {
		return err
	}
	defer st.Close()
	rows, err := st.DB.Query(`SELECT id, COALESCE(cache_path,''), COALESCE(coverage,''), COALESCE(content_sha256,''), COALESCE(content_bytes,0)
		FROM sources WHERE job_id=?`, jobID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var manifest []evidenceManifestEntry
	for rows.Next() {
		var sid, cache, cov, sha string
		var nbytes int64
		if rows.Scan(&sid, &cache, &cov, &sha, &nbytes) != nil {
			continue
		}
		if cache == "" || cov == "metadata_only" {
			continue
		}
		manifest = append(manifest, evidenceManifestEntry{SourceID: sid, Cache: cache, SHA: sha, Bytes: nbytes})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(manifest) == 0 {
		return nil
	}

	ctx := context.Background()
	if bounds.MaxTime > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, bounds.MaxTime)
		defer cancel()
	}

	var verified int
	var bytesChecked int64
	for _, ent := range manifest {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("evidence verification incomplete (%d/%d sources checked): open labelled saved results, continue verification later, or reanalyse: %w", verified, len(manifest), err)
		}
		remaining := int64(0)
		if bounds.MaxBytes > 0 {
			remaining = bounds.MaxBytes - bytesChecked
			if remaining <= 0 {
				return fmt.Errorf("evidence verification incomplete (%d/%d sources; byte budget reached); open labelled saved results, continue verification later, or reanalyse", verified, len(manifest))
			}
			if ent.Bytes > 0 && ent.Bytes > remaining {
				return fmt.Errorf("evidence verification incomplete (%d/%d sources; next source exceeds remaining byte budget); open labelled saved results, continue verification later, or reanalyse", verified, len(manifest))
			}
		}
		abs := ResolveCachePath(jobDir, ent.Cache)
		err := evidence.VerifyCacheFileBudget(ctx, abs, ent.Bytes, ent.SHA, remaining)
		if err != nil {
			if errors.Is(err, evidence.ErrVerifyIncomplete) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return fmt.Errorf("evidence verification incomplete (%d/%d sources): open labelled saved results, continue verification later, or reanalyse: %w", verified, len(manifest), err)
			}
			return fmt.Errorf("evidence integrity failure for source %s: %w", ent.SourceID, err)
		}
		verified++
		if ent.Bytes > 0 {
			bytesChecked += ent.Bytes
		} else {
			// Fall back to actual size after successful verify when declared bytes were unknown.
			if fi, serr := os.Stat(abs); serr == nil {
				bytesChecked += fi.Size()
			}
		}
	}
	if verified != len(manifest) {
		return fmt.Errorf("evidence verification incomplete (%d/%d sources); open labelled saved results, continue verification later, or reanalyse", verified, len(manifest))
	}
	return nil
}

// JobDiskUsage sums allocated bytes under a job directory.
func JobDiskUsage(jobDir string) (int64, error) {
	var total int64
	err := filepath.Walk(jobDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// DisposableSearchBytes estimates search/export/temporary reclaimable size.
func DisposableSearchBytes(jobDir string) int64 {
	var n int64
	for _, sub := range []string{"searches", "exports", "temporary"} {
		_ = filepath.Walk(filepath.Join(jobDir, sub), func(path string, info os.FileInfo, err error) error {
			if err == nil && info != nil && !info.IsDir() {
				n += info.Size()
			}
			return nil
		})
	}
	return n
}

// ClearDisposable removes searches/exports/temporary under a job; keeps input/evidence/results.
func ClearDisposable(jobDir string) error {
	var firstErr error
	for _, sub := range []string{"searches", "exports", "temporary"} {
		p := filepath.Join(jobDir, sub)
		if err := os.RemoveAll(p); err != nil && firstErr == nil {
			firstErr = err
		}
		if err := os.MkdirAll(p, 0o700); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	st, err := store.Open(filepath.Join(jobDir, "results.sqlite"))
	if err != nil {
		if firstErr == nil {
			firstErr = err
		}
		return firstErr
	}
	defer st.Close()
	if _, err := st.DB.Exec(`DELETE FROM search_hits`); err != nil && firstErr == nil {
		firstErr = err
	}
	if _, err := st.DB.Exec(`DELETE FROM searches`); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// LookupJobSHA returns sha for an open store.
func LookupJobSHA(db *sql.DB, jobID string) (string, error) {
	var sha string
	err := db.QueryRow(`SELECT COALESCE(sha256,'') FROM jobs WHERE id=?`, jobID).Scan(&sha)
	return sha, err
}
