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
)

func TestUnsupportedSourceSearchSubrouteIs404(t *testing.T) {
	root, err := storage.OpenTestRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	eng := jobs.NewEngine(root, config.DefaultLimits())
	srv := New(root, false, config.DefaultLimits())
	srv.Engine = eng

	req := httptest.NewRequest(http.MethodPost, "/api/jobs/deadbeefdeadbeefdeadbeefdeadbeef/sources/abc/search", strings.NewReader(`{"query":"x"}`))
	req.Header.Set("X-QA-Session", srv.Session)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.handleJobSub(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404 for /sources/.../search, got %d body=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "total_files") || strings.Contains(rr.Body.String(), `"items"`) {
		t.Fatalf("must not fall through to source inventory: %s", rr.Body.String())
	}
}

func TestViewerSearchContractViaSearchesAPI(t *testing.T) {
	root, err := storage.OpenTestRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	var b strings.Builder
	for i := 1; i <= 300; i++ {
		if i == 150 {
			b.WriteString("SCROLL-AUDIT-MARKER unique-hit-line\n")
		} else {
			b.WriteString("filler line\n")
		}
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	payload := []byte(b.String())
	if err := tw.WriteHeader(&tar.Header{Name: "var/log/app.log", Mode: 0600, Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	_, _ = tw.Write(payload)
	_ = tw.Close()
	_ = gz.Close()

	eng := jobs.NewEngine(root, config.DefaultLimits())
	id, err := eng.CreateJob("app.tar.gz", 0, bytes.NewReader(buf.Bytes()))
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
	_ = st.DB.QueryRow(`SELECT id FROM sources WHERE job_id=? ORDER BY raw_path LIMIT 1`, id).Scan(&sourceID)
	_ = st.Close()
	if sourceID == "" {
		t.Fatal("no source")
	}

	srv := New(root, false, config.DefaultLimits())
	srv.Engine = eng

	body := `{"source_id":"` + sourceID + `","query":"SCROLL-AUDIT-MARKER","case_sensitive":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/jobs/"+id+"/searches", strings.NewReader(body))
	req.Header.Set("X-QA-Session", srv.Session)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.handleJobSub(rr, req)
	if rr.Code != 200 {
		t.Fatalf("start search status %d %s", rr.Code, rr.Body.String())
	}
	var started map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	sid, _ := started["id"].(string)
	if sid == "" {
		t.Fatalf("missing search id: %v", started)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/jobs/"+id+"/searches/"+sid+"?offset=0&limit=50", nil)
	req2.Header.Set("X-QA-Session", srv.Session)
	rr2 := httptest.NewRecorder()
	srv.handleJobSub(rr2, req2)
	if rr2.Code != 200 {
		t.Fatalf("get search status %d %s", rr2.Code, rr2.Body.String())
	}
	var page map[string]any
	if err := json.Unmarshal(rr2.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	searchObj, _ := page["search"].(map[string]any)
	items, _ := page["items"].([]any)
	if searchObj == nil || items == nil {
		t.Fatalf("contract wants search+items, got %v", page)
	}
	if int(searchObj["matching_lines_found"].(float64)) < 1 {
		t.Fatalf("expected matching lines: %v", searchObj)
	}
	hit := items[0].(map[string]any)
	if int(hit["line"].(float64)) != 150 {
		t.Fatalf("expected line 150, got %v", hit)
	}
	if !strings.Contains(hit["text"].(string), "SCROLL-AUDIT-MARKER") {
		t.Fatalf("hit text: %v", hit)
	}
	if started["matching_lines_found"] == nil {
		t.Fatalf("start response should include matching_lines_found, got %v", started)
	}
}

func TestLargeSourceSearchStartGetPage(t *testing.T) {
	root, err := storage.OpenTestRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	const lines = 200000
	var b strings.Builder
	b.Grow(lines * 40)
	for i := 1; i <= lines; i++ {
		if i == 123456 {
			b.WriteString("LARGE-SEARCH-MARKER unique-hit\n")
		} else {
			b.WriteString("filler line for large search\n")
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

	eng := jobs.NewEngine(root, config.DefaultLimits())
	id, err := eng.CreateJob("huge.tar.gz", 0, bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(120 * time.Second)
	for time.Now().Before(until) {
		if eng.ActiveJobID() == "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	st, err := eng.OpenJobStore(id)
	if err != nil {
		t.Fatal(err)
	}
	var sourceID string
	var lineCount int
	_ = st.DB.QueryRow(`SELECT id, COALESCE(line_count,0) FROM sources WHERE job_id=? ORDER BY raw_path LIMIT 1`, id).Scan(&sourceID, &lineCount)
	_ = st.Close()
	if sourceID == "" || lineCount < lines {
		t.Fatalf("source not ready id=%s lines=%d", sourceID, lineCount)
	}

	srv := New(root, false, config.DefaultLimits())
	srv.Engine = eng

	body := `{"source_id":"` + sourceID + `","query":"LARGE-SEARCH-MARKER","case_sensitive":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/jobs/"+id+"/searches", strings.NewReader(body))
	req.Header.Set("X-QA-Session", srv.Session)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.handleJobSub(rr, req)
	if rr.Code != 200 {
		t.Fatalf("start search status %d %s", rr.Code, rr.Body.String())
	}
	var started map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	sid, _ := started["id"].(string)
	if sid == "" {
		t.Fatalf("missing search id: %v", started)
	}
	if int(started["matching_lines_found"].(float64)) != 1 {
		t.Fatalf("expected 1 matching line on start, got %v", started)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/jobs/"+id+"/searches/"+sid+"?offset=0&limit=50", nil)
	req2.Header.Set("X-QA-Session", srv.Session)
	rr2 := httptest.NewRecorder()
	srv.handleJobSub(rr2, req2)
	if rr2.Code != 200 {
		t.Fatalf("get search status %d %s", rr2.Code, rr2.Body.String())
	}
	var page map[string]any
	if err := json.Unmarshal(rr2.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	searchObj := page["search"].(map[string]any)
	items := page["items"].([]any)
	if int(searchObj["matching_lines_found"].(float64)) != 1 {
		t.Fatalf("exact matching_lines_found want 1 got %v", searchObj)
	}
	if int(searchObj["keyword_occurrences_found"].(float64)) != 1 {
		t.Fatalf("exact occurrences want 1 got %v", searchObj)
	}
	if len(items) != 1 {
		t.Fatalf("page length must not be treated as total; want 1 item got %d (total still in search object)", len(items))
	}
	hit := items[0].(map[string]any)
	if int(hit["line"].(float64)) != 123456 {
		t.Fatalf("expected line 123456, got %v", hit)
	}

	// Wrong job must not find the search id.
	req3 := httptest.NewRequest(http.MethodGet, "/api/jobs/deadbeefdeadbeefdeadbeefdeadbeef/searches/"+sid, nil)
	req3.Header.Set("X-QA-Session", srv.Session)
	rr3 := httptest.NewRecorder()
	srv.handleJobSub(rr3, req3)
	if rr3.Code != 404 {
		t.Fatalf("cross-job search get want 404, got %d %s", rr3.Code, rr3.Body.String())
	}
}
