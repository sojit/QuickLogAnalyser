package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"quickanalyser/internal/config"
	"quickanalyser/internal/jobs"
	"quickanalyser/internal/storage"
)

func TestFilesAPIFilenameFilterAndPagination(t *testing.T) {
	root, err := storage.OpenTestRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	eng := jobs.NewEngine(root, config.DefaultLimits())
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, name := range []string{"var/log/messages", "var/log/keycloak/server.log", "var/log/app.log", "conf/app.conf"} {
		payload := []byte("2026-10-23 11:38:00 ERROR marker-" + filepath.Base(name) + "\n")
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(payload))}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write(payload)
	}
	_ = tw.Close()
	_ = gz.Close()
	id, err := eng.CreateJob("filter.tar.gz", 0, bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(30 * time.Second)
	for eng.ActiveJobID() != "" && time.Now().Before(until) {
		time.Sleep(20 * time.Millisecond)
	}
	srv := New(root, false, config.DefaultLimits())
	srv.Engine = eng
	req := httptest.NewRequest(http.MethodGet, "/api/jobs/"+id+"/files?kind=log&q=keycloak&limit=50&offset=0", nil)
	req.Header.Set("X-QA-Session", srv.Session)
	rr := httptest.NewRecorder()
	srv.handleFiles(rr, req, id)
	if rr.Code != 200 {
		t.Fatalf("status %d %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if int(body["matching_files"].(float64)) != 1 {
		t.Fatalf("matching_files=%v body=%v", body["matching_files"], body)
	}
	if int(body["total_files"].(float64)) < 2 {
		t.Fatalf("total_files=%v", body["total_files"])
	}
	items := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("page items=%d", len(items))
	}
}

func TestFilesAPIListsCoreParentAndMibNamedLogs(t *testing.T) {
	root, err := storage.OpenTestRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	eng := jobs.NewEngine(root, config.DefaultLimits())
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, name := range []string{
		"snapshot/core.collector/service.log",
		"snapshot/logs/device-mib.log",
		"snapshot/logs/.hidden.log",
		"snapshot/var/log/APP.LOG",
	} {
		payload := []byte("2026-10-27 10:00:00 INFO " + filepath.Base(name) + "\n")
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(payload))}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write(payload)
	}
	_ = tw.Close()
	_ = gz.Close()
	id, err := eng.CreateJob("named-boundary.tar.gz", 0, bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(30 * time.Second)
	for eng.ActiveJobID() != "" && time.Now().Before(until) {
		time.Sleep(20 * time.Millisecond)
	}
	srv := New(root, false, config.DefaultLimits())
	srv.Engine = eng
	req := httptest.NewRequest(http.MethodGet, "/api/jobs/"+id+"/files?kind=log&limit=50&offset=0", nil)
	req.Header.Set("X-QA-Session", srv.Session)
	rr := httptest.NewRecorder()
	srv.handleFiles(rr, req, id)
	if rr.Code != 200 {
		t.Fatalf("status %d %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if int(body["total_files"].(float64)) < 4 {
		t.Fatalf("expected >=4 named logs, got %v body=%v", body["total_files"], body)
	}
	want := map[string]bool{
		"service.log":    false,
		"device-mib.log": false,
		".hidden.log":    false,
		"APP.LOG":        false,
	}
	for _, raw := range body["items"].([]any) {
		it := raw.(map[string]any)
		base := filepath.Base(strings.ReplaceAll(it["path"].(string), `\`, `/`))
		if _, ok := want[base]; ok {
			want[base] = true
			if it["source_kind"] != "log" {
				t.Fatalf("%s kind=%v", base, it["source_kind"])
			}
		}
	}
	for name, found := range want {
		if !found {
			t.Fatalf("missing named log %s in %v", name, body["items"])
		}
	}
}

func TestFilesAPIListsMetadataOnlyNamedLogs(t *testing.T) {
	root, err := storage.OpenTestRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	eng := jobs.NewEngine(root, config.DefaultLimits())
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	text := []byte("2026-10-23 11:38:00 ERROR ok\n")
	if err := tw.WriteHeader(&tar.Header{Name: "var/log/ok.log", Mode: 0600, Size: int64(len(text))}); err != nil {
		t.Fatal(err)
	}
	_, _ = tw.Write(text)
	bin := []byte{0x00, 0x01, 0xff, 0xfe, 0x00, 0x10, 0x20, 0x30}
	if err := tw.WriteHeader(&tar.Header{Name: "var/log/blob.log", Mode: 0600, Size: int64(len(bin))}); err != nil {
		t.Fatal(err)
	}
	_, _ = tw.Write(bin)
	kv := []byte("timeout=30\nmode=safe\n")
	if err := tw.WriteHeader(&tar.Header{Name: "conf/custom/agent.log", Mode: 0600, Size: int64(len(kv))}); err != nil {
		t.Fatal(err)
	}
	_, _ = tw.Write(kv)
	_ = tw.Close()
	_ = gz.Close()
	id, err := eng.CreateJob("named-logs.tar.gz", 0, bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(30 * time.Second)
	for eng.ActiveJobID() != "" && time.Now().Before(until) {
		time.Sleep(20 * time.Millisecond)
	}
	srv := New(root, false, config.DefaultLimits())
	srv.Engine = eng
	req := httptest.NewRequest(http.MethodGet, "/api/jobs/"+id+"/files?kind=log&limit=50&offset=0", nil)
	req.Header.Set("X-QA-Session", srv.Session)
	rr := httptest.NewRecorder()
	srv.handleFiles(rr, req, id)
	if rr.Code != 200 {
		t.Fatalf("status %d %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if int(body["total_files"].(float64)) < 3 {
		t.Fatalf("expected >=3 named logs, got total_files=%v body=%v", body["total_files"], body)
	}
	foundBlob := false
	foundKV := false
	for _, raw := range body["items"].([]any) {
		it := raw.(map[string]any)
		p := it["path"].(string)
		if strings.HasSuffix(p, "blob.log") {
			foundBlob = true
			if it["coverage"] != "metadata_only" {
				t.Fatalf("blob.log coverage=%v", it["coverage"])
			}
			if it["openable"] != false {
				t.Fatalf("blob.log should not be openable: %v", it)
			}
		}
		if strings.HasSuffix(p, "agent.log") {
			foundKV = true
			if it["source_kind"] != "log" {
				t.Fatalf("agent.log kind=%v", it["source_kind"])
			}
		}
	}
	if !foundBlob || !foundKV {
		t.Fatalf("missing expected logs blob=%v kv=%v items=%v", foundBlob, foundKV, body["items"])
	}
}
