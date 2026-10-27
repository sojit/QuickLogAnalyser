package jobs

import (
	"crypto/sha256"
	"encoding/hex"
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

func sha256Sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestReuseRejectsCorruptedEvidenceSameSize(t *testing.T) {
	root, err := storage.OpenTestRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	id := "cccccccccccccccccccccccccccccccc"
	dir := root.JobDir(id)
	_ = os.MkdirAll(filepath.Join(dir, "input"), 0o700)
	_ = os.MkdirAll(filepath.Join(dir, "evidence"), 0o700)
	_ = os.WriteFile(filepath.Join(dir, "input", "snapshot.bin"), []byte("z"), 0o600)
	good := []byte("good-evidence-bytes-aaaa")
	cacheRel := "evidence/src1.txt"
	_ = os.WriteFile(filepath.Join(dir, cacheRel), good, 0o600)
	st, err := store.Open(filepath.Join(dir, "results.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	sum := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	flags := compatibilityFlagsJSON(config.DefaultLimits())
	_, _ = st.DB.Exec(`INSERT INTO jobs(id,original_name,state,stage,sha256,app_version,rule_version,schema_flags,created_utc,updated_utc)
		VALUES(?,?,?,?,?,?,?,?,?,?)`, id, "z.tgz", "completed", "done", sum,
		appinfo.Version(), detection.RuleBundleVersion, flags, "t", "t")
	realHash := sha256Sum(good)
	_, _ = st.DB.Exec(`INSERT INTO sources(id,job_id,raw_path,kind,cache_path,coverage,source_kind,content_sha256,content_bytes) VALUES(?,?,?,?,?,?,?,?,?)`,
		"src1", id, "var/log/a.log", "regular", cacheRel, "complete", "log", realHash, int64(len(good)))
	_ = st.Close()

	// Corrupt same length — existence/size checks would pass; checksum must fail.
	bad := make([]byte, len(good))
	copy(bad, good)
	bad[0] ^= 0xff
	_ = os.WriteFile(filepath.Join(dir, cacheRel), bad, 0o600)

	e := NewEngine(root, config.DefaultLimits())
	match, err := e.FindCompatibleByHash(sum)
	if err != nil {
		t.Fatal(err)
	}
	if match == nil || match.Compatible {
		t.Fatalf("corrupted same-size cache must not be compatible: %+v", match)
	}
	if !strings.Contains(strings.ToLower(match.Reason), "integrity") && !strings.Contains(strings.ToLower(match.Reason), "checksum") {
		t.Fatalf("expected integrity reason, got %q", match.Reason)
	}
}

func TestReuseAcceptsVerifiedEvidence(t *testing.T) {
	root, err := storage.OpenTestRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	id := "dddddddddddddddddddddddddddddddd"
	dir := root.JobDir(id)
	_ = os.MkdirAll(filepath.Join(dir, "input"), 0o700)
	_ = os.MkdirAll(filepath.Join(dir, "evidence"), 0o700)
	_ = os.WriteFile(filepath.Join(dir, "input", "snapshot.bin"), []byte("ok"), 0o600)
	payload := []byte("verified-cache-content-line\n")
	cacheRel := "evidence/src1.txt"
	_ = os.WriteFile(filepath.Join(dir, cacheRel), payload, 0o600)

	// Hash file
	sumFile := sha256File(t, filepath.Join(dir, cacheRel))
	st, err := store.Open(filepath.Join(dir, "results.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	jobSum := "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	flags := compatibilityFlagsJSON(config.DefaultLimits())
	_, _ = st.DB.Exec(`INSERT INTO jobs(id,original_name,state,stage,sha256,app_version,rule_version,schema_flags,created_utc,updated_utc)
		VALUES(?,?,?,?,?,?,?,?,?,?)`, id, "ok.tgz", "completed", "done", jobSum,
		appinfo.Version(), detection.RuleBundleVersion, flags, "t", "t")
	_, _ = st.DB.Exec(`INSERT INTO sources(id,job_id,raw_path,kind,cache_path,coverage,source_kind,content_sha256,content_bytes) VALUES(?,?,?,?,?,?,?,?,?)`,
		"src1", id, "var/log/a.log", "regular", cacheRel, "complete", "log", sumFile, int64(len(payload)))
	_ = st.Close()

	e := NewEngine(root, config.DefaultLimits())
	match, err := e.FindCompatibleByHash(jobSum)
	if err != nil {
		t.Fatal(err)
	}
	if match == nil || !match.Compatible {
		t.Fatalf("verified evidence should be compatible: %+v", match)
	}
}

func sha256File(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256Sum(b)
	return h
}
