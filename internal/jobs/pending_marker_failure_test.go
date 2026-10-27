package jobs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"quickanalyser/internal/store"
)

func TestPendingMarkerWriteFailureDoesNotAdvanceClassification(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "results.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err = os.MkdirAll(filepath.Join(dir, "evidence"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "evidence/a.txt"), []byte("2026-10-27 10:00:00 ERROR connection refused\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB.Exec(`INSERT INTO jobs(id,original_name,state,stage,created_utc,updated_utc) VALUES('job','sample.tgz','completed','done','t','t')`); err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB.Exec(`INSERT INTO sources(id,job_id,raw_path,kind,cache_path,coverage,source_kind,classify_version) VALUES('src','job','linux/opt/allot/logs/example.log','regular','evidence/a.txt','complete','configuration','1.2.0')`); err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB.Exec(`CREATE TRIGGER audit_fail_flags BEFORE UPDATE OF schema_flags ON jobs BEGIN SELECT RAISE(ABORT, 'audit injected flags failure'); END`); err != nil {
		t.Fatal(err)
	}

	opened, err := EnsureJobClassificationOnly(st, "job", dir)
	if err == nil {
		t.Fatal("expected open to fail when pending marker cannot be persisted")
	}
	t.Logf("open_err=%v", err)
	if opened.FindingsRebuilt {
		t.Fatal("open must not rebuild findings")
	}
	var kind, ver, flags string
	if err := st.DB.QueryRow(`SELECT source_kind, classify_version FROM sources WHERE id='src'`).Scan(&kind, &ver); err != nil {
		t.Fatal(err)
	}
	if err := st.DB.QueryRow(`SELECT COALESCE(schema_flags,'') FROM jobs WHERE id='job'`).Scan(&flags); err != nil {
		t.Fatal(err)
	}
	if kind != "configuration" || ver != "1.2.0" {
		t.Fatalf("classification must not advance without durable pending marker; got kind=%s ver=%s", kind, ver)
	}
	if strings.Contains(flags, `"findings_rebuild_pending":true`) {
		t.Fatalf("pending must not appear durable after aborted flags write; flags=%s", flags)
	}

	// Remove injection and complete the normal open→refresh path.
	if _, err = st.DB.Exec(`DROP TRIGGER IF EXISTS audit_fail_flags`); err != nil {
		t.Fatal(err)
	}
	opened2, err := EnsureJobClassificationOnly(st, "job", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !opened2.FindingsPending {
		t.Fatal("open after recovery must persist pending")
	}
	refreshed, err := EnsureJobCompatibility(st, "job", dir)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err = st.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE job_id='job'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	t.Logf("after_recovery open_pending=%v refresh_rebuilt=%v events=%d", opened2.FindingsPending, refreshed.FindingsRebuilt, n)
	if n == 0 || !refreshed.FindingsRebuilt || refreshed.FindingsPending {
		t.Fatal("explicit Refresh after recovered open must rebuild findings and clear pending")
	}
}
