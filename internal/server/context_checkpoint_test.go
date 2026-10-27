package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"quickanalyser/internal/config"
	"quickanalyser/internal/jobs"
	"quickanalyser/internal/storage"
)

func TestContextAPIUsesPersistedCheckpoints(t *testing.T) {
	root, err := storage.OpenTestRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	var b strings.Builder
	for i := 1; i <= 3000; i++ {
		b.WriteString("2026-01-01 00:00:00 INFO filler ")
		b.WriteString(strings.Repeat("x", 40))
		b.WriteByte('\n')
	}
	b.WriteString("2026-10-23 11:38:00 ERROR late-context-marker\n")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	payload := []byte(b.String())
	if err := tw.WriteHeader(&tar.Header{Name: "var/log/big.log", Mode: 0600, Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	_, _ = tw.Write(payload)
	_ = tw.Close()
	_ = gz.Close()

	eng := jobs.NewEngine(root, config.DefaultLimits())
	id, err := eng.CreateJob("big.tar.gz", 0, bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(60 * time.Second)
	for time.Now().Before(until) {
		if eng.ActiveJobID() == "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if eng.ActiveJobID() != "" {
		t.Fatal("job still active")
	}
	st, err := eng.OpenJobStore(id)
	if err != nil {
		t.Fatal(err)
	}
	var state, errSum string
	_ = st.DB.QueryRow(`SELECT state, COALESCE(error_summary,'') FROM jobs WHERE id=?`, id).Scan(&state, &errSum)
	var sourceID string
	var lineCount, cps int
	_ = st.DB.QueryRow(`SELECT id, COALESCE(line_count,0) FROM sources WHERE job_id=? ORDER BY raw_path LIMIT 1`, id).Scan(&sourceID, &lineCount)
	if sourceID != "" {
		_ = st.DB.QueryRow(`SELECT COUNT(*) FROM line_checkpoints WHERE source_id=?`, sourceID).Scan(&cps)
	}
	_ = st.Close()
	if state != "completed" && state != "partial" {
		t.Fatalf("unexpected job state=%s err=%s", state, errSum)
	}
	if sourceID == "" || lineCount < 3000 || cps < 2 {
		t.Fatalf("source setup failed state=%s id=%s lines=%d cps=%d err=%s", state, sourceID, lineCount, cps, errSum)
	}

	srv := New(root, false, config.DefaultLimits())
	srv.Engine = eng
	q := strconv.Itoa(lineCount)
	req := httptest.NewRequest(http.MethodGet, "/api/jobs/"+id+"/sources/"+sourceID+"/context?start="+q+"&end="+q, nil)
	req.Header.Set("X-QA-Session", srv.Session)
	rr := httptest.NewRecorder()
	srv.handleContext(rr, req, id, sourceID)
	if rr.Code != 200 {
		t.Fatalf("status %d %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["checkpoint_used"] != true {
		t.Fatalf("expected checkpoint_used true: %v", body)
	}
	if int(body["checkpoints_available"].(float64)) < 2 {
		t.Fatalf("checkpoints_available=%v", body["checkpoints_available"])
	}
	lines, _ := body["lines"].([]any)
	if len(lines) < 1 || !strings.Contains(lines[0].(string), "late-context-marker") {
		t.Fatalf("late line missing: %v", body)
	}
	if int(body["start"].(float64)) != lineCount || int(body["end"].(float64)) != lineCount {
		t.Fatalf("actual range want %d-%d got start=%v end=%v", lineCount, lineCount, body["start"], body["end"])
	}
	if body["eof"] != true {
		t.Fatalf("expected eof true at last line: %v", body)
	}
}

func TestContextAPIReturnsActualRangeAndEOF(t *testing.T) {
	root, err := storage.OpenTestRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	var b strings.Builder
	for i := 1; i <= 250; i++ {
		b.WriteString("line-")
		b.WriteString(strconv.Itoa(i))
		b.WriteByte('\n')
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	payload := []byte(b.String())
	if err := tw.WriteHeader(&tar.Header{Name: "var/log/short.log", Mode: 0600, Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	_, _ = tw.Write(payload)
	_ = tw.Close()
	_ = gz.Close()

	eng := jobs.NewEngine(root, config.DefaultLimits())
	id, err := eng.CreateJob("short.tar.gz", 0, bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(30 * time.Second)
	for time.Now().Before(until) {
		if eng.ActiveJobID() == "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	st, err := eng.OpenJobStore(id)
	if err != nil {
		t.Fatal(err)
	}
	var sourceID string
	var lineCount int
	_ = st.DB.QueryRow(`SELECT id, COALESCE(line_count,0) FROM sources WHERE job_id=? ORDER BY raw_path LIMIT 1`, id).Scan(&sourceID, &lineCount)
	_ = st.Close()
	if sourceID == "" || lineCount < 250 {
		t.Fatalf("setup failed id=%s lines=%d", sourceID, lineCount)
	}

	srv := New(root, false, config.DefaultLimits())
	srv.Engine = eng
	req := httptest.NewRequest(http.MethodGet, "/api/jobs/"+id+"/sources/"+sourceID+"/context?start=201&end=400", nil)
	req.Header.Set("X-QA-Session", srv.Session)
	rr := httptest.NewRecorder()
	srv.handleContext(rr, req, id, sourceID)
	if rr.Code != 200 {
		t.Fatalf("status %d %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	lines, _ := body["lines"].([]any)
	if len(lines) != 50 {
		t.Fatalf("want 50 lines (201-250), got %d body=%v", len(lines), body)
	}
	if int(body["start"].(float64)) != 201 || int(body["end"].(float64)) != 250 {
		t.Fatalf("actual range 201-250 got start=%v end=%v", body["start"], body["end"])
	}
	if body["eof"] != true {
		t.Fatalf("expected eof true: %v", body)
	}
	if !strings.Contains(lines[len(lines)-1].(string), "line-250") {
		t.Fatalf("last line want line-250: %v", lines[len(lines)-1])
	}
}
