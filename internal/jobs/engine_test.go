package jobs

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"quickanalyser/internal/config"
	"quickanalyser/internal/storage"
)

func TestUnknownProductEndToEnd(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	repo := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	fixture := filepath.Join(repo, "testdata", "sanitized", "unknown_product.tar.gz")
	f, err := os.Open(fixture)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	rootPath := t.TempDir()
	root, err := storage.OpenTestRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	eng := NewEngine(root, config.DefaultLimits())
	id, err := eng.CreateJob("unknown_product.tar.gz", 0, f)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		st, err := eng.OpenJobStore(id)
		if err != nil {
			t.Fatal(err)
		}
		var state, product string
		var partial int
		err = st.DB.QueryRow(`SELECT state, COALESCE(product_label,''), partial FROM jobs WHERE id=?`, id).Scan(&state, &product, &partial)
		_ = st.Close()
		if err != nil {
			t.Fatal(err)
		}
		if state == "completed" || state == "partial" {
			if product == "" {
				t.Fatal("expected product label")
			}
			st2, _ := eng.OpenJobStore(id)
			var events int
			_ = st2.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE job_id=?`, id).Scan(&events)
			_ = st2.Close()
			if events == 0 {
				t.Fatal("expected generic detections without product adapter")
			}
			return
		}
		if state == "failed" || state == "cancelled" {
			t.Fatalf("unexpected state %s", state)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("timeout waiting for job")
}

func TestThirdProductFixture(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	repo := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	fixture := filepath.Join(repo, "testdata", "sanitized", "third_product.tar.gz")
	f, err := os.Open(fixture)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	root, err := storage.OpenTestRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	eng := NewEngine(root, config.DefaultLimits())
	id, err := eng.CreateJob("third_product.tar.gz", 0, f)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		st, err := eng.OpenJobStore(id)
		if err != nil {
			t.Fatal(err)
		}
		var state string
		var events int
		_ = st.DB.QueryRow(`SELECT state FROM jobs WHERE id=?`, id).Scan(&state)
		_ = st.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE job_id=?`, id).Scan(&events)
		_ = st.Close()
		if state == "completed" || state == "partial" {
			if events == 0 {
				t.Fatal("third product should still yield common detections")
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("timeout")
}
