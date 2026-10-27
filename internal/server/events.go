package server

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"quickanalyser/internal/jobs"
	"quickanalyser/internal/masking"
)

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request, id string) {
	st, err := s.Engine.OpenJobStore(id)
	if err != nil {
		http.Error(w, "job not found", 404)
		return
	}
	defer st.Close()
	_, _ = jobs.CheckJobCompatibility(st, id)

	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = s.Limits.PageDefault
	}
	if limit > s.Limits.PageMax {
		limit = s.Limits.PageMax
	}
	if offset < 0 {
		offset = 0
	}
	sortKey := r.URL.Query().Get("sort")
	if sortKey == "" {
		sortKey = "newest"
	}
	rangeKey := r.URL.Query().Get("range")
	if rangeKey == "" {
		rangeKey = "24h"
	}
	includeUndated := r.URL.Query().Get("include_undated") == "1" || r.URL.Query().Get("include_undated") == "true"
	refRaw := r.URL.Query().Get("ref_time")
	customFrom := r.URL.Query().Get("from")
	customTo := r.URL.Query().Get("to")

	refLabel := "unknown"
	refTime := time.Time{}
	if refRaw != "" {
		if t, err := time.Parse(time.RFC3339Nano, refRaw); err == nil {
			refTime = t
			refLabel = "explicit"
		} else if t, err := time.Parse(time.RFC3339, refRaw); err == nil {
			refTime = t
			refLabel = "explicit"
		}
	}
	if refTime.IsZero() {
		var coll string
		_ = st.DB.QueryRow(`SELECT COALESCE(collection_time_raw,'') FROM jobs WHERE id=?`, id).Scan(&coll)
		if coll != "" {
			refLabel = "collection_time"
			if t, err := time.Parse(time.RFC3339Nano, coll); err == nil {
				refTime = t
			}
		}
	}
	if refTime.IsZero() {
		var latest string
		_ = st.DB.QueryRow(`SELECT COALESCE(MAX(event_time_utc),'') FROM events WHERE job_id=? AND event_time_utc IS NOT NULL AND event_time_utc!=''`, id).Scan(&latest)
		if latest != "" {
			if t, err := time.Parse(time.RFC3339Nano, latest); err == nil {
				refTime = t
				refLabel = "latest_observed_event"
			}
		}
	}
	if refTime.IsZero() {
		rangeKey = "all"
		refLabel = "unavailable_showing_all"
	}

	where := []string{`e.job_id=?`, `s.source_kind='log'`}
	args := []any{id}
	if src := r.URL.Query().Get("source_id"); src != "" {
		where = append(where, `e.source_id=?`)
		args = append(args, src)
	}

	var fromUTC, toUTC string
	switch rangeKey {
	case "24h":
		fromUTC = refTime.Add(-24 * time.Hour).UTC().Format(time.RFC3339Nano)
		toUTC = refTime.UTC().Format(time.RFC3339Nano)
	case "7d":
		fromUTC = refTime.Add(-7 * 24 * time.Hour).UTC().Format(time.RFC3339Nano)
		toUTC = refTime.UTC().Format(time.RFC3339Nano)
	case "custom":
		fromUTC, toUTC = customFrom, customTo
	case "historical":
		fromUTC = ""
		toUTC = refTime.Add(-24 * time.Hour).UTC().Format(time.RFC3339Nano)
	case "all":
		// no time predicate
	default:
		rangeKey = "24h"
		fromUTC = refTime.Add(-24 * time.Hour).UTC().Format(time.RFC3339Nano)
		toUTC = refTime.UTC().Format(time.RFC3339Nano)
	}

	timePred := ""
	if rangeKey != "all" && (fromUTC != "" || toUTC != "") {
		parts := []string{}
		if fromUTC != "" && toUTC != "" {
			parts = append(parts, `(e.event_time_utc IS NOT NULL AND e.event_time_utc!='' AND e.event_time_utc>=? AND e.event_time_utc<=?)`)
			args = append(args, fromUTC, toUTC)
		} else if toUTC != "" && rangeKey == "historical" {
			parts = append(parts, `(e.event_time_utc IS NOT NULL AND e.event_time_utc!='' AND e.event_time_utc<?)`)
			args = append(args, toUTC)
		}
		if includeUndated {
			parts = append(parts, `(e.event_time_utc IS NULL OR e.event_time_utc='')`)
		}
		if len(parts) > 0 {
			timePred = `(` + strings.Join(parts, ` OR `) + `)`
			where = append(where, timePred)
		} else if !includeUndated {
			where = append(where, `(e.event_time_utc IS NOT NULL AND e.event_time_utc!='')`)
		}
	} else if !includeUndated && rangeKey != "all" {
		where = append(where, `(e.event_time_utc IS NOT NULL AND e.event_time_utc!='')`)
	}

	whereSQL := strings.Join(where, " AND ")
	countQ := `SELECT COUNT(*) FROM events e JOIN sources s ON s.id=e.source_id WHERE ` + whereSQL
	var total int
	_ = st.DB.QueryRow(countQ, args...).Scan(&total)

	var undated int
	_ = st.DB.QueryRow(`SELECT COUNT(*) FROM events e JOIN sources s ON s.id=e.source_id WHERE e.job_id=? AND s.source_kind='log' AND (e.event_time_utc IS NULL OR e.event_time_utc='')`, id).Scan(&undated)

	orderSQL := `CASE e.severity WHEN 'emergency' THEN 1 WHEN 'alert' THEN 2 WHEN 'critical' THEN 3 WHEN 'error' THEN 4 WHEN 'warning' THEN 5 ELSE 6 END, COALESCE(e.event_time_utc,'' ) DESC, s.raw_path, e.line_start`
	switch sortKey {
	case "oldest":
		orderSQL = `CASE WHEN e.event_time_utc IS NULL OR e.event_time_utc='' THEN 1 ELSE 0 END, e.event_time_utc ASC, s.raw_path, e.line_start`
	case "newest":
		orderSQL = `CASE WHEN e.event_time_utc IS NULL OR e.event_time_utc='' THEN 1 ELSE 0 END, e.event_time_utc DESC, s.raw_path, e.line_start`
	case "severity_newest":
		orderSQL = `CASE e.severity WHEN 'emergency' THEN 1 WHEN 'alert' THEN 2 WHEN 'critical' THEN 3 WHEN 'error' THEN 4 WHEN 'warning' THEN 5 ELSE 6 END, CASE WHEN e.event_time_utc IS NULL OR e.event_time_utc='' THEN 1 ELSE 0 END, e.event_time_utc DESC, s.raw_path, e.line_start`
	case "file_line":
		orderSQL = `s.raw_path ASC, e.line_start ASC`
	default:
		sortKey = "newest"
		orderSQL = `CASE WHEN e.event_time_utc IS NULL OR e.event_time_utc='' THEN 1 ELSE 0 END, e.event_time_utc DESC, s.raw_path, e.line_start`
	}

	q := `SELECT e.id, e.source_id, s.raw_path, e.severity, e.categories, e.message, e.line_start, e.line_end, e.raw_time, e.fingerprint, COALESCE(e.event_time_utc,''), COALESCE(e.time_confidence,'')
		FROM events e JOIN sources s ON s.id=e.source_id WHERE ` + whereSQL + ` ORDER BY ` + orderSQL + ` LIMIT ? OFFSET ?`
	qArgs := append(append([]any{}, args...), limit, offset)
	rows, err := st.DB.Query(q, qArgs...)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()

	type group struct {
		Fingerprint   string           `json:"fingerprint"`
		Count         int              `json:"count"`
		Severity      string           `json:"severity"`
		Path          string           `json:"path"`
		SourceID      string           `json:"source_id"`
		Categories    string           `json:"categories"`
		Message       string           `json:"message"`
		LineStart     int              `json:"line_start"`
		LineEnd       int              `json:"line_end"`
		RawTime       string           `json:"raw_time"`
		EventTimeUTC  string           `json:"event_time_utc"`
		Occurrences   []map[string]any `json:"occurrences"`
		LatestRawTime string           `json:"latest_raw_time,omitempty"`
	}
	groups := map[string]*group{}
	var order []string
	pageCount := 0
	for rows.Next() {
		var eid, sid, path, sev, cats, msg, raw, fp, evUTC, tconf string
		var ls, le int
		_ = rows.Scan(&eid, &sid, &path, &sev, &cats, &msg, &ls, &le, &raw, &fp, &evUTC, &tconf)
		pageCount++
		g, ok := groups[fp]
		if !ok {
			g = &group{Fingerprint: fp, Severity: sev, Path: path, SourceID: sid, Categories: cats,
				Message: masking.Line(msg), LineStart: ls, LineEnd: le, RawTime: raw, EventTimeUTC: evUTC, LatestRawTime: raw}
			groups[fp] = g
			order = append(order, fp)
		}
		g.Count++
		// Newest mode representative = latest occurrence on this page (query already newest-first).
		if sortKey == "newest" || sortKey == "severity_newest" {
			if g.Count == 1 || (evUTC != "" && (g.EventTimeUTC == "" || evUTC > g.EventTimeUTC)) {
				g.Message = masking.Line(msg)
				g.LineStart, g.LineEnd = ls, le
				g.RawTime, g.EventTimeUTC, g.LatestRawTime = raw, evUTC, raw
			}
		}
		g.Occurrences = append(g.Occurrences, map[string]any{
			"id": eid, "line_start": ls, "line_end": le, "message": masking.Line(msg),
			"raw_time": raw, "event_time_utc": evUTC, "time_confidence": tconf,
		})
	}
	var items []*group
	for _, fp := range order {
		items = append(items, groups[fp])
	}
	writeJSON(w, map[string]any{
		"items": items, "offset": offset, "limit": limit,
		"page_event_count": pageCount, "total_events": total,
		"truncated": offset+pageCount < total,
		"sort": sortKey, "range": rangeKey,
		"ref_time": refTime.UTC().Format(time.RFC3339Nano), "ref_label": refLabel,
		"undated_total": undated, "include_undated": includeUndated,
	})
}
