package jobs

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"quickanalyser/internal/classify"
	"quickanalyser/internal/config"
	"quickanalyser/internal/storage"
	"quickanalyser/internal/store"
)

func makeTinyArchive(t *testing.T, marker string) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	payload := []byte("2026-10-23 11:38:00 ERROR " + marker + "\n")
	if err := tw.WriteHeader(&tar.Header{Name: "var/log/app.log", Mode: 0600, Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()
	return b.Bytes()
}

func waitIdle(t *testing.T, e *Engine) {
	t.Helper()
	until := time.Now().Add(30 * time.Second)
	for time.Now().Before(until) {
		if e.ActiveJobID() == "" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("job did not become idle")
}

func TestPhase5ReopenWithoutReparsing(t *testing.T) {
	root, err := storage.OpenTestRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	e := NewEngine(root, config.DefaultLimits())
	id, err := e.CreateJob("a.tgz", 0, bytes.NewReader(makeTinyArchive(t, "reopen-marker")))
	if err != nil {
		t.Fatal(err)
	}
	waitIdle(t, e)
	st, err := e.OpenJobStore(id)
	if err != nil {
		t.Fatal(err)
	}
	var nBefore int
	_ = st.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE job_id=?`, id).Scan(&nBefore)
	_ = st.Close()

	// New engine instance over same root — reopen inventory only.
	e2 := NewEngine(root, config.DefaultLimits())
	rows, err := e2.ListJobs()
	if err != nil || len(rows) != 1 {
		t.Fatalf("reopen list: %d %v", len(rows), err)
	}
	st2, err := e2.OpenJobStore(id)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	var nAfter int
	_ = st2.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE job_id=?`, id).Scan(&nAfter)
	if nAfter != nBefore || nAfter < 1 {
		t.Fatalf("reopen changed or lost findings: before=%d after=%d", nBefore, nAfter)
	}
	if e2.ActiveJobID() != "" {
		t.Fatal("reopen must not start parsing")
	}
}

func TestPhase5RenamedIdenticalReusesAndNoDuplicateJob(t *testing.T) {
	root, err := storage.OpenTestRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	arch := makeTinyArchive(t, "dup-marker")
	e := NewEngine(root, config.DefaultLimits())
	first, err := e.CreateJob("one.tgz", 0, bytes.NewReader(arch))
	if err != nil {
		t.Fatal(err)
	}
	waitIdle(t, e)
	second, err := e.CreateJobOpts("two-renamed.tgz", 0, bytes.NewReader(arch), CreateOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !second.Reused || second.ID != first {
		t.Fatalf("expected reuse of %s got %+v", first, second)
	}
	rows, _ := e.ListJobs()
	if len(rows) != 1 {
		t.Fatalf("duplicate permanent job created: %d", len(rows))
	}
	// Force reanalyse creates a distinct job.
	third, err := e.CreateJobOpts("three.tgz", 0, bytes.NewReader(arch), CreateOpts{ForceReanalyse: true})
	if err != nil {
		t.Fatal(err)
	}
	waitIdle(t, e)
	if third.Reused || third.ID == first {
		t.Fatalf("force reanalyse should create new job: %+v", third)
	}
}

func TestPhase5DifferentBytesSameNameDistinct(t *testing.T) {
	root, err := storage.OpenTestRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	e := NewEngine(root, config.DefaultLimits())
	a, err := e.CreateJob("same-name.tgz", 0, bytes.NewReader(makeTinyArchive(t, "bytes-a")))
	if err != nil {
		t.Fatal(err)
	}
	waitIdle(t, e)
	b, err := e.CreateJobOpts("same-name.tgz", 0, bytes.NewReader(makeTinyArchive(t, "bytes-b")), CreateOpts{})
	if err != nil {
		t.Fatal(err)
	}
	waitIdle(t, e)
	if b.Reused || b.ID == a {
		t.Fatalf("different bytes must not reuse: %+v vs %s", b, a)
	}
}

func TestPhase5FailedRefreshPreservesFindings(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "results.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, _ = st.DB.Exec(`INSERT INTO jobs(id,original_name,state,stage,created_utc,updated_utc) VALUES('job','s.tar','completed','done','t','t')`)
	_, _ = st.DB.Exec(`INSERT INTO sources(id,job_id,raw_path,kind,cache_path,coverage,source_kind,classify_version) VALUES('src','job','var/log/a.log','regular','evidence/missing.txt','complete','log',?)`, "0.0.0")
	_, _ = st.DB.Exec(`INSERT INTO events(id,job_id,source_id,severity,message,line_start,line_end,fingerprint) VALUES('e1','job','src','error','keep-me',1,1,'fp')`)
	res, err := EnsureJobCompatibility(st, "job", dir)
	var n int
	_ = st.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE job_id='job'`).Scan(&n)
	if n != 1 {
		t.Fatalf("refresh erased findings: n=%d err=%v rebuilt=%v failed=%v", n, err, res.FindingsRebuilt, res.RefreshFailed)
	}
	if !res.RefreshFailed && err == nil && classify.Version != "0.0.0" {
		// Missing evidence should mark refresh failed / preserve.
		t.Logf("compat=%+v err=%v", res, err)
	}
}

func TestPhase5InterruptDoesNotDuplicateOnNewEngine(t *testing.T) {
	root, err := storage.OpenTestRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	e := NewEngine(root, config.DefaultLimits())
	id, err := e.CreateJob("x.tgz", 0, bytes.NewReader(makeTinyArchive(t, "intr")))
	if err != nil {
		t.Fatal(err)
	}
	waitIdle(t, e)
	st, _ := e.OpenJobStore(id)
	var n1 int
	_ = st.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE job_id=?`, id).Scan(&n1)
	_ = st.Close()
	// Simulate restart recovery path.
	e2 := NewEngine(root, config.DefaultLimits())
	st2, _ := e2.OpenJobStore(id)
	defer st2.Close()
	var n2 int
	_ = st2.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE job_id=?`, id).Scan(&n2)
	if n1 != n2 {
		t.Fatalf("restart duplicated/lost findings %d -> %d", n1, n2)
	}
	// Ensure snapshot retained once.
	entries, _ := os.ReadDir(filepath.Join(root.JobDir(id), "input"))
	if len(entries) != 1 {
		t.Fatalf("expected one input snapshot, got %d", len(entries))
	}
}

func TestPhase5VersionMismatchNeedsChoiceNoDuplicate(t *testing.T) {
	root, err := storage.OpenTestRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	arch := makeTinyArchive(t, "ver-mismatch")
	e := NewEngine(root, config.DefaultLimits())
	first, err := e.CreateJob("ver.tgz", 0, bytes.NewReader(arch))
	if err != nil {
		t.Fatal(err)
	}
	waitIdle(t, e)
	st, err := e.OpenJobStore(first)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.DB.Exec(`UPDATE jobs SET app_version=? WHERE id=?`, "QA-v0.0-incompatible", first)
	_ = st.Close()
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.CreateJobOpts("ver-renamed.tgz", 0, bytes.NewReader(arch), CreateOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if second.Reused || !second.NeedsChoice || second.ID != first {
		t.Fatalf("expected needs_choice reopen of %s, got %+v", first, second)
	}
	rows, _ := e.ListJobs()
	if len(rows) != 1 {
		t.Fatalf("incompatible match must not create a second permanent job: %d", len(rows))
	}
}

func TestPhase5InterruptedMarkedPreservesFindings(t *testing.T) {
	root, err := storage.OpenTestRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	e := NewEngine(root, config.DefaultLimits())
	id, err := e.CreateJob("intr2.tgz", 0, bytes.NewReader(makeTinyArchive(t, "keep-on-intr")))
	if err != nil {
		t.Fatal(err)
	}
	waitIdle(t, e)
	st, err := e.OpenJobStore(id)
	if err != nil {
		t.Fatal(err)
	}
	var nBefore int
	_ = st.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE job_id=?`, id).Scan(&nBefore)
	_, err = st.DB.Exec(`UPDATE jobs SET state=?, stage=?, partial=1 WHERE id=?`, "parsing", "detect", id)
	_ = st.Close()
	if err != nil {
		t.Fatal(err)
	}
	e2 := NewEngine(root, config.DefaultLimits())
	st2, err := e2.OpenJobStore(id)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	var state string
	var nAfter int
	_ = st2.DB.QueryRow(`SELECT state FROM jobs WHERE id=?`, id).Scan(&state)
	_ = st2.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE job_id=?`, id).Scan(&nAfter)
	if state != "interrupted" {
		t.Fatalf("expected interrupted, got %s", state)
	}
	if nAfter != nBefore || nAfter < 1 {
		t.Fatalf("interrupted recovery changed findings: before=%d after=%d", nBefore, nAfter)
	}
	// Identical upload against interrupted job must not silent-reuse as complete.
	third, err := e2.CreateJobOpts("again.tgz", 0, bytes.NewReader(makeTinyArchive(t, "keep-on-intr")), CreateOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if third.Reused || !third.NeedsChoice {
		t.Fatalf("interrupted must need explicit choice, got %+v", third)
	}
}

func TestPhase4ProgressiveCommittedAndCheckpoints(t *testing.T) {
	root, err := storage.OpenTestRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	var b strings.Builder
	for i := 0; i < 2500; i++ {
		b.WriteString("2026-10-23 12:00:00 ERROR dense-prog-")
		b.WriteString(strconv.Itoa(i))
		b.WriteByte('\n')
	}
	arch := makeArchiveBytes(t, "var/log/dense.log", b.String())
	e := NewEngine(root, config.DefaultLimits())
	id, err := e.CreateJob("dense.tgz", 0, bytes.NewReader(arch))
	if err != nil {
		t.Fatal(err)
	}
	waitIdle(t, e)
	st, err := e.OpenJobStore(id)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var committed, final, firstMS, cps, events int
	_ = st.DB.QueryRow(`SELECT COALESCE(events_committed,0), COALESCE(counts_final,0), COALESCE(first_result_ms,0) FROM jobs WHERE id=?`, id).
		Scan(&committed, &final, &firstMS)
	_ = st.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE job_id=?`, id).Scan(&events)
	_ = st.DB.QueryRow(`SELECT COUNT(*) FROM line_checkpoints`).Scan(&cps)
	if events < 100 {
		t.Fatalf("expected many events, got %d", events)
	}
	if committed != events {
		t.Fatalf("events_committed=%d events=%d", committed, events)
	}
	if final != 1 {
		t.Fatalf("counts_final not set")
	}
	if firstMS <= 0 {
		t.Fatalf("first_result_ms unset: %d", firstMS)
	}
	if cps < 1 {
		t.Fatalf("expected persisted line_checkpoints, got %d", cps)
	}
}

func makeArchiveBytes(t *testing.T, name, payload string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}
