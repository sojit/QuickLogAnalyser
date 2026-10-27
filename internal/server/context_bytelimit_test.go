package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"quickanalyser/internal/config"
	"quickanalyser/internal/jobs"
	"quickanalyser/internal/storage"
	"quickanalyser/internal/viewerlogic"
)

func TestContextByteLimitReturnsPartialActualRange(t *testing.T) {
	root, err := storage.OpenTestRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	var b strings.Builder
	// Three huge lines then normal lines — byte budget should stop early.
	for i := 1; i <= 20; i++ {
		if i <= 3 {
			b.WriteString(strings.Repeat("X", 120000))
			b.WriteByte('\n')
		} else {
			b.WriteString("line-")
			b.WriteString(strings.Repeat("y", 10))
			b.WriteByte('\n')
		}
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	payload := []byte(b.String())
	if err := tw.WriteHeader(&tar.Header{Name: "var/log/huge.log", Mode: 0600, Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	_, _ = tw.Write(payload)
	_ = tw.Close()
	_ = gz.Close()

	limits := config.DefaultLimits()
	limits.ContextBytesMax = 200000 // enough for ~1–2 huge lines, not 100
	eng := jobs.NewEngine(root, limits)
	id, err := eng.CreateJob("huge.tar.gz", 0, bytes.NewReader(buf.Bytes()))
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
	if sourceID == "" || lineCount < 20 {
		t.Fatalf("setup failed id=%s lines=%d", sourceID, lineCount)
	}

	srv := New(root, false, limits)
	srv.Engine = eng
	req := httptest.NewRequest(http.MethodGet, "/api/jobs/"+id+"/sources/"+sourceID+"/context?start=1&end=100", nil)
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
	actualEnd := int(body["end"].(float64))
	if len(lines) == 0 || actualEnd >= 100 {
		t.Fatalf("expected byte-limited partial, got end=%d n=%d", actualEnd, len(lines))
	}
	next := viewerlogic.NextAfterPartial(1, actualEnd, lineCount)
	if next != actualEnd+1 {
		t.Fatalf("continuation want %d got %d", actualEnd+1, next)
	}
	if next == 101 {
		t.Fatal("must not skip to 101 after partial")
	}
}
