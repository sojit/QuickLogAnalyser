package jobs

import "testing"

func TestLeaseExclusiveCleanBlocksRead(t *testing.T) {
	l := NewLeaseTracker()
	if err := l.Acquire("job1", OpClean); err != nil {
		t.Fatal(err)
	}
	if err := l.Acquire("job1", OpRead); err == nil {
		t.Fatal("read must fail while clean held")
	}
	if err := l.Acquire("job1", OpDelete); err == nil {
		t.Fatal("delete must fail while clean held")
	}
	l.Release("job1", OpClean)
	if err := l.Acquire("job1", OpRead); err != nil {
		t.Fatal(err)
	}
	l.Release("job1", OpRead)
}

func TestLeaseStagingBlocksRootSwitch(t *testing.T) {
	l := NewLeaseTracker()
	if err := l.AcquireStaging(); err != nil {
		t.Fatal(err)
	}
	if err := l.AcquireRoot(); err == nil {
		t.Fatal("root switch must fail while staging")
	}
	l.ReleaseStaging()
	if err := l.AcquireRoot(); err != nil {
		t.Fatal(err)
	}
	l.ReleaseRoot()
}

func TestLeaseExclusiveRootBlocksNewReaders(t *testing.T) {
	l := NewLeaseTracker()
	if err := l.AcquireRoot(); err != nil {
		t.Fatal(err)
	}
	if err := l.Acquire("job1", OpRead); err == nil {
		t.Fatal("read must fail during exclusive root switch")
	}
	if err := l.Acquire("job1", OpSearch); err == nil {
		t.Fatal("search must fail during exclusive root switch")
	}
	l.ReleaseRoot()
	if err := l.Acquire("job1", OpRead); err != nil {
		t.Fatal(err)
	}
	l.Release("job1", OpRead)
}

func TestLeaseStagingAllowsReads(t *testing.T) {
	l := NewLeaseTracker()
	if err := l.AcquireStaging(); err != nil {
		t.Fatal(err)
	}
	if err := l.Acquire("job1", OpRead); err != nil {
		t.Fatal("reads must remain allowed during shared staging")
	}
	l.Release("job1", OpRead)
	l.ReleaseStaging()
}

func TestLeaseTransitionStagingToAnalyse(t *testing.T) {
	l := NewLeaseTracker()
	if err := l.AcquireStaging(); err != nil {
		t.Fatal(err)
	}
	if err := l.TransitionStagingToAnalyse("jobX"); err != nil {
		t.Fatal(err)
	}
	if l.StagingCount() != 0 {
		t.Fatalf("staging=%d", l.StagingCount())
	}
	if !l.JobBusy("jobX") {
		t.Fatal("analyse lease expected")
	}
	if err := l.AcquireRoot(); err == nil {
		t.Fatal("root switch blocked while analyse held")
	}
	l.Release("jobX", OpAnalyse)
}
