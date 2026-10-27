package jobs

import (
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

func TestReuseRejectsMissingEvidenceAndUnknownVersions(t *testing.T) {
	root, err := storage.OpenTestRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	id := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	dir := root.JobDir(id)
	_ = os.MkdirAll(filepath.Join(dir, "input"), 0o700)
	_ = os.MkdirAll(filepath.Join(dir, "evidence"), 0o700)
	_ = os.WriteFile(filepath.Join(dir, "input", "snapshot.bin"), []byte("x"), 0o600)
	st, err := store.Open(filepath.Join(dir, "results.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = st.DB.Exec(`INSERT INTO jobs(id,original_name,state,stage,sha256,app_version,rule_version,schema_flags,created_utc,updated_utc)
		VALUES(?,?,?,?,?,?,?,?,?,?)`, id, "x.tgz", "completed", "done",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		appinfo.Version(), detection.RuleBundleVersion,
		compatibilityFlagsJSON(config.DefaultLimits()),
		"t", "t")
	_, _ = st.DB.Exec(`INSERT INTO sources(id,job_id,raw_path,kind,cache_path,coverage,source_kind,content_sha256,content_bytes) VALUES(?,?,?,?,?,?,?,?,?)`,
		"src1", id, "var/log/a.log", "regular", "evidence/missing.txt", "complete", "log", strings.Repeat("a", 64), 10)
	_ = st.Close()

	e := NewEngine(root, config.DefaultLimits())
	match, err := e.FindCompatibleByHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	if match == nil || match.Compatible {
		t.Fatalf("expected incompatible due to missing evidence: %+v", match)
	}
	if match.Reason == "" {
		t.Fatal("expected reason")
	}
}

func TestReuseRejectsUnknownAppVersion(t *testing.T) {
	root, err := storage.OpenTestRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	id := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	dir := root.JobDir(id)
	_ = os.MkdirAll(filepath.Join(dir, "input"), 0o700)
	_ = os.WriteFile(filepath.Join(dir, "input", "snapshot.bin"), []byte("y"), 0o600)
	st, err := store.Open(filepath.Join(dir, "results.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	sum := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	_, _ = st.DB.Exec(`INSERT INTO jobs(id,original_name,state,stage,sha256,app_version,rule_version,schema_flags,created_utc,updated_utc)
		VALUES(?,?,?,?,?,?,?,?,?,?)`, id, "y.tgz", "completed", "done", sum, "", detection.RuleBundleVersion, "", "t", "t")
	_ = st.Close()
	e := NewEngine(root, config.DefaultLimits())
	match, _ := e.FindCompatibleByHash(sum)
	if match == nil || match.Compatible {
		t.Fatalf("empty app version must not be compatible: %+v", match)
	}
}
