package search

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"quickanalyser/internal/store"
)

func TestMatchingLineVersusOccurrences(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "src.txt")
	content := "alpha\nduplicate keyword keyword on one line\nbeta keyword\n"
	if err := os.WriteFile(cache, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "t.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, err = st.DB.Exec(`INSERT INTO jobs(id, original_name, state, stage, created_utc, updated_utc) VALUES('j','t','completed','done','t','t')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.DB.Exec(`INSERT INTO sources(id, job_id, raw_path, kind, cache_path, coverage) VALUES('s','j','messages','regular',?,'complete')`, cache)
	if err != nil {
		t.Fatal(err)
	}
	id, err := Start(context.Background(), st, "j", "s", cache, "keyword", false, "complete")
	if err != nil {
		t.Fatal(err)
	}
	var res *Result
	for i := 0; i < 50; i++ {
		res, err = Get(st, id)
		if err != nil {
			t.Fatal(err)
		}
		if res.CountIsFinal {
			break
		}
	}
	if res == nil || !res.CountIsFinal {
		t.Fatal("search did not finalize")
	}
	if res.MatchingLines != 2 {
		t.Fatalf("matching lines want 2 got %d", res.MatchingLines)
	}
	if res.Occurrences != 3 {
		t.Fatalf("occurrences want 3 got %d", res.Occurrences)
	}
}

func TestPartialSourceCoverageNotClaimedComplete(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "src.txt")
	if err := os.WriteFile(cache, []byte("hello keyword\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "t.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, _ = st.DB.Exec(`INSERT INTO jobs(id, original_name, state, stage, created_utc, updated_utc) VALUES('j','t','completed','done','t','t')`)
	_, _ = st.DB.Exec(`INSERT INTO sources(id, job_id, raw_path, kind, cache_path, coverage) VALUES('s','j','messages','regular',?,'partial')`, cache)
	id, err := Start(context.Background(), st, "j", "s", cache, "keyword", false, "partial")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Get(st, id)
	if err != nil {
		t.Fatal(err)
	}
	if res.ScopeComplete || res.CountIsFinal {
		t.Fatalf("partial source must not claim complete counts: %+v", res)
	}
	if res.MatchingLines != 1 {
		t.Fatalf("matching=%d", res.MatchingLines)
	}
}
