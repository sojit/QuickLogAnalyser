package jobs

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"quickanalyser/internal/config"
	"quickanalyser/internal/storage"
)

func TestVerificationNewByteBudgetCannotBeExceededByOneSource(t *testing.T) {
	root, err := storage.OpenTestRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	e := NewEngine(root, config.DefaultLimits())
	id, err := e.CreateJob("one.tgz", 0, bytes.NewReader(makeTinyArchive(t, "budget-marker")))
	if err != nil {
		t.Fatal(err)
	}
	waitIdle(t, e)
	err = evidenceUsable(root.JobDir(id), id, VerifyBounds{MaxBytes: 1, MaxTime: time.Second})
	if err == nil {
		t.Fatal("one-byte verification budget accepted an entire larger source as fully verified")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "incomplete") {
		t.Fatalf("expected incomplete verification reason, got %v", err)
	}
}
