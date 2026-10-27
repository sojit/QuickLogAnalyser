package jobs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"quickanalyser/internal/appinfo"
	"quickanalyser/internal/config"
	"quickanalyser/internal/detection"
	"quickanalyser/internal/storage"
	"quickanalyser/internal/store"
)

func TestReuseRejectsMissingEvidenceBeyond64(t *testing.T) {
	root, err := storage.OpenTestRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	id := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	dir := root.JobDir(id)
	_ = os.MkdirAll(filepath.Join(dir, "input"), 0o700)
	_ = os.MkdirAll(filepath.Join(dir, "evidence"), 0o700)
	_ = os.WriteFile(filepath.Join(dir, "input", "snapshot.bin"), []byte("z"), 0o600)
	st, err := store.Open(filepath.Join(dir, "results.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	sum := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	flags := compatibilityFlagsJSON(config.DefaultLimits())
	_, _ = st.DB.Exec(`INSERT INTO jobs(id,original_name,state,stage,sha256,app_version,rule_version,schema_flags,created_utc,updated_utc)
		VALUES(?,?,?,?,?,?,?,?,?,?)`, id, "many.tgz", "completed", "done", sum,
		appinfo.Version(), detection.RuleBundleVersion, flags, "t", "t")
	for i := 1; i <= 65; i++ {
		payload := []byte(fmt.Sprintf("evidence-body-%d\n", i))
		rel := fmt.Sprintf("evidence/src%d.txt", i)
		if i < 65 {
			_ = os.WriteFile(filepath.Join(dir, rel), payload, 0o600)
		}
		// source 65: DB row present, file missing
		h := ""
		if i < 65 {
			h = sha256Sum(payload)
		} else {
			h = sha256Sum([]byte("missing-file-placeholder"))
		}
		_, _ = st.DB.Exec(`INSERT INTO sources(id,job_id,raw_path,kind,cache_path,coverage,source_kind,content_sha256,content_bytes) VALUES(?,?,?,?,?,?,?,?,?)`,
			fmt.Sprintf("src%d", i), id, fmt.Sprintf("var/log/a%d.log", i), "regular", rel, "complete", "log", h, int64(len(payload)))
	}
	_ = st.Close()

	e := NewEngine(root, config.DefaultLimits())
	match, err := e.FindCompatibleByHash(sum)
	if err != nil {
		t.Fatal(err)
	}
	if match == nil || match.Compatible {
		t.Fatalf("missing 65th evidence must not be compatible: %+v", match)
	}
	if !strings.Contains(strings.ToLower(match.Reason), "integrity") &&
		!strings.Contains(strings.ToLower(match.Reason), "missing") &&
		!strings.Contains(strings.ToLower(match.Reason), "evidence") {
		t.Fatalf("expected evidence failure reason, got %q", match.Reason)
	}
}

func TestReuseRejectsSchemaStringMismatch(t *testing.T) {
	root, err := storage.OpenTestRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	id := "ffffffffffffffffffffffffffffffff"
	dir := root.JobDir(id)
	_ = os.MkdirAll(filepath.Join(dir, "input"), 0o700)
	_ = os.MkdirAll(filepath.Join(dir, "evidence"), 0o700)
	_ = os.WriteFile(filepath.Join(dir, "input", "snapshot.bin"), []byte("z"), 0o600)
	payload := []byte("ok\n")
	rel := "evidence/s.txt"
	_ = os.WriteFile(filepath.Join(dir, rel), payload, 0o600)
	st, err := store.Open(filepath.Join(dir, "results.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	sum := "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	flagsMap := map[string]any{}
	_ = json.Unmarshal([]byte(compatibilityFlagsJSON(config.DefaultLimits())), &flagsMap)
	flagsMap["schema_version"] = "not-a-number"
	fb, _ := json.Marshal(flagsMap)
	_, _ = st.DB.Exec(`INSERT INTO jobs(id,original_name,state,stage,sha256,app_version,rule_version,schema_flags,created_utc,updated_utc)
		VALUES(?,?,?,?,?,?,?,?,?,?)`, id, "bad-schema.tgz", "completed", "done", sum,
		appinfo.Version(), detection.RuleBundleVersion, string(fb), "t", "t")
	_, _ = st.DB.Exec(`INSERT INTO sources(id,job_id,raw_path,kind,cache_path,coverage,source_kind,content_sha256,content_bytes) VALUES(?,?,?,?,?,?,?,?,?)`,
		"src1", id, "var/log/a.log", "regular", rel, "complete", "log", sha256Sum(payload), int64(len(payload)))
	_ = st.Close()

	e := NewEngine(root, config.DefaultLimits())
	match, err := e.FindCompatibleByHash(sum)
	if err != nil {
		t.Fatal(err)
	}
	if match == nil || match.Compatible {
		t.Fatalf("malformed schema string must reject: %+v", match)
	}
	if !strings.Contains(strings.ToLower(match.Reason), "schema") {
		t.Fatalf("expected schema reason, got %q", match.Reason)
	}
}

func TestReuseRejectsFractionalSchema(t *testing.T) {
	reason := compareSchemaVersion(6.5)
	if reason == "" {
		t.Fatal("fractional schema must fail")
	}
	reason = compareSchemaVersion("6.0")
	if reason == "" {
		t.Fatal("fractional schema string must fail")
	}
	reason = compareSchemaVersion(float64(store.SchemaVersion))
	if reason != "" {
		t.Fatalf("exact schema float should pass, got %q", reason)
	}
	reason = compareSchemaVersion(fmt.Sprintf("%d", store.SchemaVersion))
	if reason != "" {
		t.Fatalf("exact schema string int should pass, got %q", reason)
	}
}

func TestReuseRejectsSettingsMismatch(t *testing.T) {
	root, err := storage.OpenTestRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	id := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa01"
	dir := root.JobDir(id)
	_ = os.MkdirAll(filepath.Join(dir, "input"), 0o700)
	_ = os.MkdirAll(filepath.Join(dir, "evidence"), 0o700)
	_ = os.WriteFile(filepath.Join(dir, "input", "snapshot.bin"), []byte("z"), 0o600)
	payload := []byte("ok\n")
	rel := "evidence/s.txt"
	_ = os.WriteFile(filepath.Join(dir, rel), payload, 0o600)
	st, err := store.Open(filepath.Join(dir, "results.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	sum := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa01aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa01"
	flagsMap := map[string]any{}
	_ = json.Unmarshal([]byte(compatibilityFlagsJSON(config.DefaultLimits())), &flagsMap)
	settings := flagsMap["settings"].(map[string]any)
	settings["max_evidence_bytes"] = int64(1) // diverge from defaults
	flagsMap["settings"] = settings
	fb, _ := json.Marshal(flagsMap)
	_, _ = st.DB.Exec(`INSERT INTO jobs(id,original_name,state,stage,sha256,app_version,rule_version,schema_flags,created_utc,updated_utc)
		VALUES(?,?,?,?,?,?,?,?,?,?)`, id, "settings.tgz", "completed", "done", sum,
		appinfo.Version(), detection.RuleBundleVersion, string(fb), "t", "t")
	_, _ = st.DB.Exec(`INSERT INTO sources(id,job_id,raw_path,kind,cache_path,coverage,source_kind,content_sha256,content_bytes) VALUES(?,?,?,?,?,?,?,?,?)`,
		"src1", id, "var/log/a.log", "regular", rel, "complete", "log", sha256Sum(payload), int64(len(payload)))
	_ = st.Close()

	e := NewEngine(root, config.DefaultLimits())
	match, err := e.FindCompatibleByHash(sum)
	if err != nil {
		t.Fatal(err)
	}
	if match == nil || match.Compatible {
		t.Fatalf("settings mismatch must reject: %+v", match)
	}
	if !strings.Contains(strings.ToLower(match.Reason), "settings") {
		t.Fatalf("expected settings reason, got %q", match.Reason)
	}
}
