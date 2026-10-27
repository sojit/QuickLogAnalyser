package jobs

import (
	"fmt"
	"sync"
)

// Op kinds for job/root lifecycle coordination.
const (
	OpAnalyse = "analyse"
	OpSearch  = "search"
	OpExport  = "export"
	OpRead    = "read"
	OpRefresh = "refresh"
	OpDelete  = "delete"
	OpClean   = "clean"
	OpStaging = "staging"
)

// LeaseTracker serializes destructive ops with active readers/writers per job and root.
// Staging is shared (reads allowed during upload hash/reuse). Exclusive root switch
// blocks all new root-bound readers and writers until released.
type LeaseTracker struct {
	mu        sync.Mutex
	jobs      map[string]map[string]int // jobID -> op -> count
	staging   int                       // shared upload/hash reservation
	exclusive int                       // exclusive root replacement
}

func NewLeaseTracker() *LeaseTracker {
	return &LeaseTracker{jobs: map[string]map[string]int{}}
}

func (l *LeaseTracker) Acquire(jobID, op string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.exclusive > 0 {
		return fmt.Errorf("storage root is switching")
	}
	// Staging blocks writers that mutate the root layout, but allows reads/search/export.
	if l.staging > 0 && op != OpRead && op != OpSearch && op != OpExport && op != OpStaging {
		// Analyse during staging is handled by atomic TransitionStagingToAnalyse.
		if op == OpAnalyse {
			return fmt.Errorf("storage root is busy staging an upload")
		}
	}
	if jobID == "" {
		return fmt.Errorf("job id required")
	}
	m := l.jobs[jobID]
	if m == nil {
		m = map[string]int{}
		l.jobs[jobID] = m
	}
	switch op {
	case OpDelete, OpClean:
		for other, n := range m {
			if n > 0 {
				return fmt.Errorf("job busy: %s in progress", other)
			}
		}
	case OpRefresh:
		if m[OpDelete] > 0 || m[OpClean] > 0 || m[OpAnalyse] > 0 {
			return fmt.Errorf("job busy")
		}
	case OpAnalyse:
		if m[OpDelete] > 0 || m[OpClean] > 0 {
			return fmt.Errorf("job busy")
		}
	case OpRead, OpSearch, OpExport, OpStaging:
		if m[OpDelete] > 0 || m[OpClean] > 0 {
			return fmt.Errorf("job is cleaning or deleting")
		}
	default:
		if m[OpDelete] > 0 || m[OpClean] > 0 {
			return fmt.Errorf("job is cleaning or deleting")
		}
	}
	m[op]++
	return nil
}

func (l *LeaseTracker) Release(jobID, op string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	m := l.jobs[jobID]
	if m == nil {
		return
	}
	if m[op] > 0 {
		m[op]--
	}
	if m[op] == 0 {
		delete(m, op)
	}
	if len(m) == 0 {
		delete(l.jobs, jobID)
	}
}

// AcquireRoot takes an exclusive root-switch lease. Blocks while any job op,
// staging reservation, or exclusive lease is active. New readers cannot start.
func (l *LeaseTracker) AcquireRoot() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.jobs) > 0 || l.staging > 0 || l.exclusive > 0 {
		return fmt.Errorf("storage root change requires idle jobs")
	}
	l.exclusive++
	return nil
}

func (l *LeaseTracker) ReleaseRoot() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.exclusive > 0 {
		l.exclusive--
	}
}

// AcquireStaging reserves shared upload/hash ownership. Reads remain allowed;
// exclusive root switch is blocked.
func (l *LeaseTracker) AcquireStaging() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.exclusive > 0 {
		return fmt.Errorf("storage root is switching")
	}
	l.staging++
	return nil
}

func (l *LeaseTracker) ReleaseStaging() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.staging > 0 {
		l.staging--
	}
}

// TransitionStagingToAnalyse atomically drops one staging reservation and
// acquires OpAnalyse for jobID without allowing an exclusive root switch to interleave.
func (l *LeaseTracker) TransitionStagingToAnalyse(jobID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.exclusive > 0 {
		return fmt.Errorf("storage root is switching")
	}
	if l.staging < 1 {
		return fmt.Errorf("no staging reservation to transition")
	}
	if jobID == "" {
		return fmt.Errorf("job id required")
	}
	m := l.jobs[jobID]
	if m == nil {
		m = map[string]int{}
		l.jobs[jobID] = m
	}
	if m[OpDelete] > 0 || m[OpClean] > 0 {
		return fmt.Errorf("job busy")
	}
	l.staging--
	m[OpAnalyse]++
	return nil
}

func (l *LeaseTracker) JobBusy(jobID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	m := l.jobs[jobID]
	if m == nil {
		return false
	}
	for _, n := range m {
		if n > 0 {
			return true
		}
	}
	return false
}

func (l *LeaseTracker) StagingCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.staging
}

func (l *LeaseTracker) ExclusiveCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.exclusive
}
