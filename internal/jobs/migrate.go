package jobs

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"quickanalyser/internal/classify"
	"quickanalyser/internal/detection"
	"quickanalyser/internal/store"
	"quickanalyser/internal/timeparse"
)

// JobCompatibility describes whether a saved analysis needs reanalysis or was refreshed.
type JobCompatibility struct {
	NeedsReanalysis       bool     `json:"needs_reanalysis"`
	Backfilled            bool     `json:"classification_backfilled"`
	ClassificationRefresh bool     `json:"classification_refreshed"`
	FindingsRebuilt       bool     `json:"findings_rebuilt"`
	FindingsPending       bool     `json:"findings_rebuild_pending"`
	FindingsGeneration    int      `json:"findings_generation,omitempty"`
	RefreshFailed         bool     `json:"refresh_failed"`
	Missing               []string `json:"missing,omitempty"`
	Message               string   `json:"message,omitempty"`
	ClassifierVer         string   `json:"classifier_version,omitempty"`
	StaleClassifier       bool     `json:"stale_classifier,omitempty"`
}

// jobFlags is the durable schema_flags payload for compatibility/pending markers.
type jobFlags struct {
	ClassifierVersion       string   `json:"classifier_version"`
	NeedsReanalysis         bool     `json:"needs_reanalysis"`
	Backfilled              bool     `json:"backfilled"`
	ClassificationRefreshed bool     `json:"classification_refreshed"`
	FindingsRebuilt         bool     `json:"findings_rebuilt"`
	FindingsRebuildPending  bool     `json:"findings_rebuild_pending"`
	FindingsGeneration      int      `json:"findings_generation"`
	RefreshFailed           bool     `json:"refresh_failed"`
	Missing                 []string `json:"missing,omitempty"`
	CheckedUTC              string   `json:"checked_utc"`
}

func readJobFlags(st *store.Store, jobID string) jobFlags {
	var raw string
	_ = st.DB.QueryRow(`SELECT COALESCE(schema_flags,'') FROM jobs WHERE id=?`, jobID).Scan(&raw)
	var f jobFlags
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &f)
	}
	return f
}

func applyPendingFromFlags(out *JobCompatibility, f jobFlags) {
	out.FindingsPending = f.FindingsRebuildPending
	out.FindingsGeneration = f.FindingsGeneration
}

// CheckJobCompatibility reports stale/missing state without mutating findings.
func CheckJobCompatibility(st *store.Store, jobID string) (JobCompatibility, error) {
	out := JobCompatibility{ClassifierVer: classify.Version}
	applyPendingFromFlags(&out, readJobFlags(st, jobID))
	need, err := st.NeedsClassification(jobID, classify.Version)
	if err != nil {
		return out, err
	}
	out.StaleClassifier = need
	if need {
		out.Message = "Classifier or source kinds are stale; schedule an explicit refresh/reanalysis. Ordinary reads do not rebuild findings."
	} else if out.FindingsPending {
		out.Message = "Findings rebuild is pending after classification refresh; use Refresh to rebuild Highlights for newly recognized logs."
	}
	var releaseCount, attrCount int
	_ = st.DB.QueryRow(`SELECT COUNT(*) FROM attributes WHERE job_id=?`, jobID).Scan(&attrCount)
	_ = st.DB.QueryRow(`SELECT COUNT(*) FROM attributes WHERE job_id=? AND field_key='product.release' AND selected=1 AND status!='historical'`, jobID).Scan(&releaseCount)
	var product string
	_ = st.DB.QueryRow(`SELECT COALESCE(product_label,'') FROM jobs WHERE id=?`, jobID).Scan(&product)
	looksProduct := strings.Contains(strings.ToLower(product), "netxplorer") || strings.Contains(strings.ToLower(product), "nx") ||
		strings.Contains(strings.ToLower(product), "sgve") || strings.Contains(strings.ToLower(product), "vsg")
	if looksProduct && releaseCount == 0 {
		out.NeedsReanalysis = true
		out.Missing = append(out.Missing, "product.release")
	}
	if attrCount == 0 {
		out.NeedsReanalysis = true
		out.Missing = append(out.Missing, "profile")
	}
	return out, nil
}

// EnsureJobCompatibility refreshes source_kind when missing or classify_version differs
// and rebuilds findings when kinds change. Prefer CheckJobCompatibility for ordinary reads;
// use EnsureJobClassificationOnly for non-blocking open-path kind/version refresh.
func EnsureJobCompatibility(st *store.Store, jobID, jobDir string) (JobCompatibility, error) {
	return ensureJobCompatibility(st, jobID, jobDir, true)
}

// EnsureJobClassificationOnly advances source_kind/classify_version without findings rebuild.
func EnsureJobClassificationOnly(st *store.Store, jobID, jobDir string) (JobCompatibility, error) {
	return ensureJobCompatibility(st, jobID, jobDir, false)
}

func ensureJobCompatibility(st *store.Store, jobID, jobDir string, rebuildFindings bool) (JobCompatibility, error) {
	out := JobCompatibility{ClassifierVer: classify.Version}
	prior := readJobFlags(st, jobID)
	applyPendingFromFlags(&out, prior)
	pending := prior.FindingsRebuildPending
	generation := prior.FindingsGeneration

	rows, err := st.DB.Query(`SELECT id, raw_path, COALESCE(cache_path,''), coverage,
		COALESCE(source_kind,''), COALESCE(classify_version,'') FROM sources WHERE job_id=?`, jobID)
	if err != nil {
		return out, err
	}
	defer rows.Close()

	type srcRow struct {
		id, raw, cache, cov, kind, ver string
	}
	var all []srcRow
	for rows.Next() {
		var r srcRow
		if err := rows.Scan(&r.id, &r.raw, &r.cache, &r.cov, &r.kind, &r.ver); err != nil {
			return out, err
		}
		all = append(all, r)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}

	var staged []stagedClass
	needRebuild := pending
	insufficient := 0
	for _, r := range all {
		stale := r.kind == "" || r.ver == "" || r.ver != classify.Version
		if !stale {
			continue
		}
		out.StaleClassifier = out.StaleClassifier || (r.ver != "" && r.ver != classify.Version)
		var sample []byte
		abs := ""
		if strings.TrimSpace(r.cache) != "" {
			abs = ResolveCachePath(jobDir, r.cache)
			if abs == "" || !fileExists(abs) {
				insufficient++
				// Do not advance classify_version when claimed evidence is missing — keep retryable.
				out.RefreshFailed = true
				out.Missing = append(out.Missing, "evidence_unreadable:"+r.raw)
				continue
			}
			var serr error
			sample, serr = readPrefix(abs, 4096)
			if serr != nil {
				insufficient++
			}
		}
		cls := classify.ClassifyMember(r.raw, sample, strings.HasSuffix(strings.ToLower(r.raw), ".gz"))
		reason := cls.Reason
		conf := cls.Confidence
		if abs == "" && len(sample) == 0 {
			reason = cls.Reason + "_path_only"
			if conf == "high" {
				conf = "medium"
			}
		}
		kindChanged := r.kind != string(cls.Kind)
		versionChanged := r.ver != classify.Version
		sc := stagedClass{
			id: r.id, kind: string(cls.Kind), reason: reason, conf: conf, ver: classify.Version,
			changed: kindChanged || versionChanged,
		}
		staged = append(staged, sc)
		// Kind change or same-kind classifier semantic version advance both require findings rebuild.
		if kindChanged || versionChanged {
			needRebuild = true
		}
		if r.kind == "" {
			out.Backfilled = true
		} else {
			out.ClassificationRefresh = true
		}
	}

	if out.RefreshFailed && len(staged) == 0 {
		out.Message = "Classification refresh deferred: evidence missing; existing findings preserved."
		out.FindingsPending = pending
		out.FindingsGeneration = generation
		if err := writeFlags(st, jobID, out); err != nil {
			return out, fmt.Errorf("refresh deferred: missing evidence (also failed to persist flags: %w)", err)
		}
		return out, fmt.Errorf("refresh deferred: missing evidence")
	}

	if needRebuild && !pending {
		generation++
		if generation < 1 {
			generation = 1
		}
		pending = true
	}
	out.FindingsPending = pending
	out.FindingsGeneration = generation
	if insufficient > 0 {
		out.Missing = append(out.Missing, "evidence_sample")
	}

	var releaseCount, attrCount int
	_ = st.DB.QueryRow(`SELECT COUNT(*) FROM attributes WHERE job_id=?`, jobID).Scan(&attrCount)
	_ = st.DB.QueryRow(`SELECT COUNT(*) FROM attributes WHERE job_id=? AND field_key='product.release' AND selected=1 AND status!='historical'`, jobID).Scan(&releaseCount)
	var product string
	_ = st.DB.QueryRow(`SELECT COALESCE(product_label,'') FROM jobs WHERE id=?`, jobID).Scan(&product)
	looksProduct := strings.Contains(strings.ToLower(product), "netxplorer") || strings.Contains(strings.ToLower(product), "nx") ||
		strings.Contains(strings.ToLower(product), "sgve") || strings.Contains(strings.ToLower(product), "vsg")
	if looksProduct && releaseCount == 0 {
		out.NeedsReanalysis = true
		out.Missing = append(out.Missing, "product.release")
	}
	if attrCount == 0 {
		out.NeedsReanalysis = true
		out.Missing = append(out.Missing, "profile")
	}

	if needRebuild && !rebuildFindings {
		out.Message = "Classifier " + classify.Version + " refreshed source kinds. Findings rebuild is pending; use Refresh or reanalyse if Highlights look incomplete."
	} else if looksProduct && releaseCount == 0 && out.Message == "" {
		out.Message = "This saved analysis predates curated version extraction. Reanalyse the original snapshot to populate release/build evidence."
	}

	// Atomic: source kind/version updates and pending-generation flags commit together.
	// A failed flags write rolls back classification advances so Refresh can still see stale kinds.
	if len(staged) > 0 || (needRebuild && !rebuildFindings) || (needRebuild && rebuildFindings) {
		persistOut := out
		if needRebuild {
			persistOut.FindingsPending = true
			persistOut.FindingsGeneration = generation
			persistOut.FindingsRebuilt = false
		}
		if err := applyClassificationAndFlags(st, jobID, staged, persistOut); err != nil {
			out.RefreshFailed = true
			out.Missing = append(out.Missing, "compatibility_persist:"+err.Error())
			out.Message = "Could not persist classification/pending findings state; prior source kinds and findings retained. Retry Refresh."
			out.FindingsPending = prior.FindingsRebuildPending
			out.FindingsGeneration = prior.FindingsGeneration
			return out, err
		}
		out.FindingsPending = persistOut.FindingsPending
		out.FindingsGeneration = persistOut.FindingsGeneration
	}

	if needRebuild && rebuildFindings {
		if err := rebuildFindingsTransactional(st, jobID, jobDir); err != nil {
			out.RefreshFailed = true
			out.FindingsPending = true
			out.FindingsGeneration = generation
			out.Missing = append(out.Missing, "findings_rebuild:"+err.Error())
			out.Message = "Findings refresh failed; previous findings retained where possible. Retry after restoring evidence or reanalyse."
			if ferr := writeFlags(st, jobID, out); ferr != nil {
				return out, fmt.Errorf("%v (also failed to persist pending flags: %w)", err, ferr)
			}
			return out, err
		}
		out.FindingsRebuilt = true
		out.FindingsPending = false
		if err := writeFlags(st, jobID, out); err != nil {
			out.FindingsPending = true
			out.RefreshFailed = true
			out.Missing = append(out.Missing, "pending_clear:"+err.Error())
			out.Message = "Findings rebuilt but pending marker could not be cleared; retry Refresh to finalize compatibility state."
			return out, err
		}
	}

	if !out.RefreshFailed {
		need, _ := st.NeedsClassification(jobID, classify.Version)
		out.StaleClassifier = need
	}
	if out.FindingsRebuilt && (out.Message == "" || strings.Contains(out.Message, "pending")) {
		out.Message = "Classifier " + classify.Version + " refreshed source kinds and rebuilt log findings."
	} else if out.FindingsPending && out.Message == "" {
		out.Message = "Findings rebuild is pending; use Refresh to update Highlights."
	} else if out.ClassificationRefresh && out.Message == "" {
		out.Message = "Classifier " + classify.Version + " refreshed source kinds."
	}
	if len(staged) == 0 && !needRebuild {
		if err := writeFlags(st, jobID, out); err != nil {
			return out, err
		}
	}
	return out, nil
}

// stagedClass is a pending source classification update.
type stagedClass struct {
	id, kind, reason, conf, ver string
	changed                     bool
}

// applyClassificationAndFlags updates source classifications and job schema_flags
// in one transaction so a failed pending marker cannot leave advanced kinds without
// a durable rebuild requirement.
func applyClassificationAndFlags(st *store.Store, jobID string, staged []stagedClass, out JobCompatibility) error {
	tx, err := st.DB.Begin()
	if err != nil {
		return err
	}
	for _, sc := range staged {
		if _, err := tx.Exec(`UPDATE sources SET source_kind=?, classify_reason=?, classify_confidence=?, classify_version=? WHERE id=?`,
			sc.kind, sc.reason, sc.conf, sc.ver, sc.id); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	payload, err := json.Marshal(jobFlags{
		ClassifierVersion:       classify.Version,
		NeedsReanalysis:         out.NeedsReanalysis,
		Backfilled:              out.Backfilled,
		ClassificationRefreshed: out.ClassificationRefresh,
		FindingsRebuilt:         out.FindingsRebuilt,
		FindingsRebuildPending:  out.FindingsPending,
		FindingsGeneration:      out.FindingsGeneration,
		RefreshFailed:           out.RefreshFailed,
		Missing:                 out.Missing,
		CheckedUTC:              time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`UPDATE jobs SET schema_flags=? WHERE id=?`, string(payload), jobID); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func rebuildFindingsTransactional(st *store.Store, jobID, jobDir string) error {
	rows, err := st.DB.Query(`SELECT id, raw_path, COALESCE(cache_path,''), COALESCE(source_kind,'') FROM sources WHERE job_id=? AND source_kind=? AND cache_path IS NOT NULL AND cache_path!=''`,
		jobID, string(classify.KindLog))
	if err != nil {
		return err
	}
	defer rows.Close()

	type pending struct {
		sid, raw string
		findings []detection.Finding
		partial  string
	}
	var batch []pending
	for rows.Next() {
		var sid, raw, cache, kind string
		if err := rows.Scan(&sid, &raw, &cache, &kind); err != nil {
			return err
		}
		abs := ResolveCachePath(jobDir, cache)
		f, err := os.Open(abs)
		if err != nil {
			return fmt.Errorf("open evidence %s: %w", raw, err)
		}
		res, perr := detection.Scan(f)
		_ = f.Close()
		if perr != nil && len(res.Findings) == 0 {
			return fmt.Errorf("scan %s: %w", raw, perr)
		}
		p := pending{sid: sid, raw: raw, findings: res.Findings}
		if res.Partial {
			p.partial = res.Reason
		}
		batch = append(batch, p)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	tx, err := st.DB.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM events WHERE job_id=?`, jobID); err != nil {
		_ = tx.Rollback()
		return err
	}
	for _, p := range batch {
		for _, fnd := range p.findings {
			catsList := fnd.Categories
			if len(catsList) == 0 {
				catsList = []string{"severity"}
			}
			cats, _ := json.Marshal(catsList)
			rules, _ := json.Marshal(fnd.RuleIDs)
			fp := detection.Fingerprint(p.raw, fnd.Severity, catsList, fnd.Message)
			tp := timeparse.Parse(fnd.RawTime, 0)
			evUTC := ""
			zone := 0
			if tp.UTC != nil {
				evUTC = tp.UTC.UTC().Format(time.RFC3339Nano)
			}
			if tp.ZoneKnown {
				zone = 1
			}
			if _, err := tx.Exec(`INSERT INTO events(id, job_id, source_id, severity, categories, message, line_start, line_end, raw_time, rule_ids, fingerprint, event_time_utc, time_precision, time_confidence, time_zone_known)
				VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, newID(), jobID, p.sid, fnd.Severity, string(cats), fnd.Message, fnd.LineStart, fnd.LineEnd, fnd.RawTime, string(rules), fp, evUTC, tp.Precision, tp.Confidence, zone); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
		if p.partial != "" {
			_, _ = tx.Exec(`INSERT INTO coverage_issues(id, job_id, source_id, code, severity, reason) VALUES(?,?,?,?,?,?)`,
				newID(), jobID, p.sid, "detection_partial", "warning", p.partial)
		}
	}
	return tx.Commit()
}

func writeFlags(st *store.Store, jobID string, out JobCompatibility) error {
	payload, _ := json.Marshal(jobFlags{
		ClassifierVersion:       classify.Version,
		NeedsReanalysis:         out.NeedsReanalysis,
		Backfilled:              out.Backfilled,
		ClassificationRefreshed: out.ClassificationRefresh,
		FindingsRebuilt:         out.FindingsRebuilt,
		FindingsRebuildPending:  out.FindingsPending,
		FindingsGeneration:      out.FindingsGeneration,
		RefreshFailed:           out.RefreshFailed,
		Missing:                 out.Missing,
		CheckedUTC:              time.Now().UTC().Format(time.RFC3339),
	})
	_, err := st.DB.Exec(`UPDATE jobs SET schema_flags=? WHERE id=?`, string(payload), jobID)
	return err
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func readPrefix(path string, n int) ([]byte, error) {
	if path == "" || n <= 0 {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, n)
	got, err := io.ReadFull(f, buf)
	if err == io.ErrUnexpectedEOF || err == io.EOF {
		return buf[:got], nil
	}
	if err != nil {
		return buf[:got], err
	}
	return buf[:got], nil
}

// ResolveCachePath turns a stored absolute or job-relative evidence path into an absolute path.
func ResolveCachePath(jobDir, stored string) string {
	if stored == "" {
		return ""
	}
	if filepath.IsAbs(stored) {
		return stored
	}
	return filepath.Join(jobDir, filepath.FromSlash(stored))
}
