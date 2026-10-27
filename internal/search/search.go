package search

import (
	"bufio"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"quickanalyser/internal/masking"
	"quickanalyser/internal/store"
)

type Result struct {
	ID            string `json:"id"`
	State         string `json:"state"`
	MatchingLines int    `json:"matching_lines_found"`
	Occurrences   int    `json:"keyword_occurrences_found"`
	CountIsFinal  bool   `json:"count_is_final"`
	ScannedBytes  int64  `json:"scanned_bytes"`
	ScopeComplete bool   `json:"scope_complete"`
	HitsComplete  bool   `json:"hits_complete"`
	Error         string `json:"error,omitempty"`
}

type Hit struct {
	LineNo      int    `json:"line"`
	Occurrences int    `json:"occurrences"`
	Text        string `json:"text"`
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Start scans the retained source, writing every matching line to SQLite in batches
// so pagination can retrieve the full match set without a silent in-memory cap.
func Start(ctx context.Context, st *store.Store, jobID, sourceID, cachePath, query string, caseSensitive bool, sourceCoverage string) (string, error) {
	if strings.TrimSpace(query) == "" {
		return "", ErrEmptyQuery
	}
	id := newID()
	_, err := st.DB.Exec(`INSERT INTO searches(id, job_id, source_id, query, case_sensitive, state) VALUES(?,?,?,?,?,?)`,
		id, jobID, sourceID, query, boolInt(caseSensitive), "scanning")
	if err != nil {
		return "", err
	}
	res := runScanPersist(ctx, st, id, cachePath, query, caseSensitive)
	sourceComplete := sourceCoverage == "" || sourceCoverage == "complete" || sourceCoverage == "empty"
	final := 1
	complete := 1
	hitsComplete := 1
	state := "completed"
	note := ""
	if res.err != nil && ctx.Err() != nil {
		state = "cancelled"
		final = 0
		complete = 0
		hitsComplete = 0
		note = errString(res.err)
	} else if res.err != nil {
		state = "partial"
		final = 0
		complete = 0
		hitsComplete = 0
		note = errString(res.err)
	} else if !sourceComplete {
		state = "partial"
		final = 0
		complete = 0
		note = "source coverage is " + sourceCoverage + "; matching counts cover retained bytes only"
	}
	if !res.hitsComplete {
		hitsComplete = 0
		state = "partial"
		note = joinNote(note, "hit persistence incomplete")
	}
	if _, err := st.DB.Exec(`UPDATE searches SET state=?, matching_lines=?, occurrences=?, count_final=?, scanned_bytes=?, scope_complete=?, error=? WHERE id=?`,
		state, res.lines, res.occ, final, res.bytes, complete, note, id); err != nil {
		return id, fmt.Errorf("persist search result: %w", err)
	}
	_ = hitsComplete
	return id, nil
}

type scanResult struct {
	lines, occ  int
	bytes       int64
	hitsComplete bool
	err         error
}

func runScanPersist(ctx context.Context, st *store.Store, searchID, cachePath, query string, caseSensitive bool) scanResult {
	f, err := os.Open(cachePath)
	if err != nil {
		return scanResult{err: err}
	}
	defer f.Close()
	needle := query
	if !caseSensitive {
		needle = strings.ToLower(query)
	}
	sc := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1<<20)
	var res scanResult
	res.hitsComplete = true
	lineNo := 0
	const batch = 500
	type row struct {
		line, occ int
		text      string
	}
	pending := make([]row, 0, batch)
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		tx, err := st.DB.Begin()
		if err != nil {
			return err
		}
		for _, h := range pending {
			if _, err := tx.Exec(`INSERT OR REPLACE INTO search_hits(search_id, line_no, occurrences, line_text) VALUES(?,?,?,?)`,
				searchID, h.line, h.occ, h.text); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		pending = pending[:0]
		return nil
	}
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			_ = flush()
			res.err = err
			res.hitsComplete = false
			return res
		}
		lineNo++
		text := sc.Text()
		res.bytes += int64(len(text) + 1)
		hay := text
		if !caseSensitive {
			hay = strings.ToLower(text)
		}
		count := strings.Count(hay, needle)
		if count > 0 {
			res.lines++
			res.occ += count
			pending = append(pending, row{line: lineNo, occ: count, text: sanitizeLine(masking.Line(text))})
			if len(pending) >= batch {
				if err := flush(); err != nil {
					res.err = err
					res.hitsComplete = false
					return res
				}
			}
		}
	}
	if err := flush(); err != nil {
		res.err = err
		res.hitsComplete = false
		return res
	}
	if err := sc.Err(); err != nil {
		res.err = err
		res.hitsComplete = false
	}
	return res
}

// ErrNotFound is returned when no search row exists for the id (and optional job).
var ErrNotFound = errors.New("search not found")

func Get(st *store.Store, searchID string) (*Result, error) {
	return GetForJob(st, "", searchID)
}

// GetForJob loads a search row, optionally scoped to jobID to prevent cross-job reads.
func GetForJob(st *store.Store, jobID, searchID string) (*Result, error) {
	var row *sql.Row
	if jobID != "" {
		row = st.DB.QueryRow(`SELECT id, state, matching_lines, occurrences, count_final, scanned_bytes, scope_complete, COALESCE(error,'') FROM searches WHERE id=? AND job_id=?`, searchID, jobID)
	} else {
		row = st.DB.QueryRow(`SELECT id, state, matching_lines, occurrences, count_final, scanned_bytes, scope_complete, COALESCE(error,'') FROM searches WHERE id=?`, searchID)
	}
	var r Result
	var final, complete int
	if err := row.Scan(&r.ID, &r.State, &r.MatchingLines, &r.Occurrences, &final, &r.ScannedBytes, &complete, &r.Error); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("load search %s: %w", searchID, err)
	}
	r.CountIsFinal = final == 1
	r.ScopeComplete = complete == 1
	r.HitsComplete = r.Error == "" || !strings.Contains(r.Error, "hit persistence")
	return &r, nil
}

func Page(st *store.Store, searchID string, offset, limit int) ([]Hit, error) {
	rows, err := st.DB.Query(`SELECT line_no, occurrences, line_text FROM search_hits WHERE search_id=? ORDER BY line_no LIMIT ? OFFSET ?`,
		searchID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Hit
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.LineNo, &h.Occurrences, &h.Text); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func joinNote(a, b string) string {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + "; " + b
}

func sanitizeLine(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	if len(s) > 8000 {
		return s[:8000] + "…"
	}
	return s
}

var ErrEmptyQuery = errEmpty("empty query")

type errEmpty string

func (e errEmpty) Error() string { return string(e) }
