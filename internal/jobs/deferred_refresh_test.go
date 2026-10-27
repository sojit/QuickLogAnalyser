package jobs

import (
	"os"
	"path/filepath"
	"testing"

	"quickanalyser/internal/store"
)

func TestDeferredFindingsRefreshAfterClassificationOnlyOpen(t *testing.T) {
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

	opened, err := EnsureJobClassificationOnly(st, "job", dir)
	if err != nil {
		t.Fatal(err)
	}
	if opened.FindingsRebuilt {
		t.Fatal("open path must not rebuild findings")
	}
	if !opened.FindingsPending {
		t.Fatal("open path must persist findings_rebuild_pending after kind change")
	}
	var kind, ver string
	if err := st.DB.QueryRow(`SELECT source_kind, classify_version FROM sources WHERE id='src'`).Scan(&kind, &ver); err != nil {
		t.Fatal(err)
	}
	if kind != "log" {
		t.Fatalf("expected kind log after open, got %q", kind)
	}

	// Simulate restart: re-open store and check pending survives.
	st.Close()
	st, err = store.Open(filepath.Join(dir, "results.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	check, err := CheckJobCompatibility(st, "job")
	if err != nil {
		t.Fatal(err)
	}
	if !check.FindingsPending {
		t.Fatal("pending marker must survive restart/re-open")
	}

	refreshed, err := EnsureJobCompatibility(st, "job", dir)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err = st.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE job_id='job'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	t.Logf("open_rebuilt=%v pending_after_open=%v explicit_refresh_rebuilt=%v pending_after_refresh=%v events=%d gen=%d",
		opened.FindingsRebuilt, opened.FindingsPending, refreshed.FindingsRebuilt, refreshed.FindingsPending, n, refreshed.FindingsGeneration)
	if n == 0 || !refreshed.FindingsRebuilt {
		t.Fatal("explicit Refresh after open did not rebuild findings for newly recognized log")
	}
	if refreshed.FindingsPending {
		t.Fatal("pending must clear only after successful rebuild")
	}

	// Idempotent second refresh: no duplicate requirement.
	again, err := EnsureJobCompatibility(st, "job", dir)
	if err != nil {
		t.Fatal(err)
	}
	if again.FindingsRebuilt {
		t.Fatal("idempotent refresh should not rebuild again without pending/stale work")
	}
	var n2 int
	_ = st.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE job_id='job'`).Scan(&n2)
	if n2 != n {
		t.Fatalf("event count changed on idempotent refresh: %d -> %d", n, n2)
	}
}

func TestDeferredFindingsRefreshFailureRetainsPriorEvents(t *testing.T) {
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
	if _, err = st.DB.Exec(`INSERT INTO events(id,job_id,source_id,severity,message,line_start,line_end,fingerprint) VALUES('old','job','src','error','prior finding',1,1,'fp')`); err != nil {
		t.Fatal(err)
	}

	opened, err := EnsureJobClassificationOnly(st, "job", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !opened.FindingsPending {
		t.Fatal("expected pending after open")
	}

	// Break evidence so rebuild fails; prior events must remain and pending stay set.
	_ = os.Remove(filepath.Join(dir, "evidence/a.txt"))
	refreshed, err := EnsureJobCompatibility(st, "job", dir)
	if err == nil {
		t.Fatal("expected rebuild failure when evidence missing")
	}
	if !refreshed.FindingsPending {
		t.Fatal("pending must remain after failed rebuild")
	}
	var n int
	if e := st.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE job_id='job'`).Scan(&n); e != nil {
		t.Fatal(e)
	}
	if n != 1 {
		t.Fatalf("failed rebuild must retain prior findings; got %d", n)
	}

	// Restore evidence and retry.
	if err = os.WriteFile(filepath.Join(dir, "evidence/a.txt"), []byte("2026-10-27 10:00:00 ERROR connection refused\n"), 0600); err != nil {
		t.Fatal(err)
	}
	retry, err := EnsureJobCompatibility(st, "job", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !retry.FindingsRebuilt || retry.FindingsPending {
		t.Fatalf("retry should rebuild and clear pending: rebuilt=%v pending=%v", retry.FindingsRebuilt, retry.FindingsPending)
	}
	if e := st.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE job_id='job'`).Scan(&n); e != nil {
		t.Fatal(e)
	}
	if n < 1 {
		t.Fatal("expected findings after successful retry")
	}
}
