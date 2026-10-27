package export

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"quickanalyser/internal/appinfo"
	"quickanalyser/internal/masking"
	"quickanalyser/internal/store"
)

func WriteJSON(st *store.Store, jobID, outDir string) (string, error) {
	payload := map[string]any{
		"generated_by": appinfo.ProductName,
		"signature":    appinfo.DeveloperDisplay,
		"version":      appinfo.Version(),
		"exported_utc": time.Now().UTC().Format(time.RFC3339),
		"job_id":       jobID,
		"license":      appinfo.LicenseID,
		"copyright":    appinfo.CopyrightNotice,
		"note":         "Redacted projection for investigation; not a raw database dump.",
	}
	row := st.DB.QueryRow(`SELECT original_name, state, product_label, COALESCE(coverage_json,'') FROM jobs WHERE id=?`, jobID)
	var name, state, product, cov string
	_ = row.Scan(&name, &state, &product, &cov)
	payload["original_name"] = name
	payload["state"] = state
	payload["product_label"] = product
	payload["coverage_json"] = cov

	var attrs []map[string]any
	arows, _ := st.DB.Query(`SELECT field_key, raw_value, confidence, status FROM attributes WHERE job_id=? AND selected=1`, jobID)
	if arows != nil {
		defer arows.Close()
		for arows.Next() {
			var f, v, c, s string
			_ = arows.Scan(&f, &v, &c, &s)
			attrs = append(attrs, map[string]any{"field": f, "value": mask(v), "confidence": c, "status": s})
		}
	}
	payload["profile"] = attrs

	var eventTotal int
	_ = st.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE job_id=?`, jobID).Scan(&eventTotal)
	var events []map[string]any
	const exportLimit = 5000
	erows, _ := st.DB.Query(`SELECT e.id, s.id, s.raw_path, e.severity, e.categories, e.message, e.line_start, e.line_end, e.raw_time, COALESCE(e.event_time_utc,'') FROM events e JOIN sources s ON s.id=e.source_id WHERE e.job_id=? ORDER BY COALESCE(e.event_time_utc,'') DESC LIMIT ?`, jobID, exportLimit)
	if erows != nil {
		defer erows.Close()
		for erows.Next() {
			var eid, sid, path, sev, cats, msg, raw, evUTC string
			var ls, le int
			_ = erows.Scan(&eid, &sid, &path, &sev, &cats, &msg, &ls, &le, &raw, &evUTC)
			events = append(events, map[string]any{
				"event_id": eid, "source_id": sid, "path": path, "severity": sev, "categories": cats,
				"message": masking.Line(msg), "line_start": ls, "line_end": le,
				"raw_time": raw, "event_time_utc": evUTC,
			})
		}
	}
	payload["events"] = events
	payload["events_total"] = eventTotal
	payload["events_exported"] = len(events)
	payload["events_truncated"] = eventTotal > len(events)

	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(outDir, "export-"+jobID+".json")
	return path, os.WriteFile(path, b, 0o600)
}

func mask(s string) string { return masking.Line(s) }
