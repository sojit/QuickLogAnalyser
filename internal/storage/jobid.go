package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ValidJobID reports whether id matches the current 32-character lowercase-hex job ID form.
func ValidJobID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') {
			continue
		}
		return false
	}
	return true
}

// RejectedJobDirName is joined under jobs/ when an invalid ID is supplied to JobDir.
// It never resolves outside the jobs directory.
const RejectedJobDirName = ".rejected-job-id"

// SafeJobDir returns the owned job directory for a validated ID, or an error without
// constructing a traversable path.
func (m *Manager) SafeJobDir(id string) (string, error) {
	if m == nil {
		return "", fmt.Errorf("storage root unavailable")
	}
	if !ValidJobID(id) {
		return "", fmt.Errorf("invalid job id")
	}
	jobs := filepath.Clean(m.JobsDir())
	target := filepath.Clean(filepath.Join(jobs, id))
	rel, err := filepath.Rel(jobs, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("job path escapes jobs root")
	}
	if strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return "", fmt.Errorf("invalid job id")
	}
	// Resolve symlinks/junctions when the path exists; require containment.
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		jobsResolved, jerr := filepath.EvalSymlinks(jobs)
		if jerr != nil {
			jobsResolved = jobs
		}
		rel2, rerr := filepath.Rel(jobsResolved, resolved)
		if rerr != nil || rel2 == ".." || strings.HasPrefix(rel2, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("job path escapes jobs root via link")
		}
		return resolved, nil
	}
	return target, nil
}

// JobExists reports whether a validated owned job directory exists.
func (m *Manager) JobExists(id string) bool {
	dir, err := m.SafeJobDir(id)
	if err != nil {
		return false
	}
	st, err := os.Stat(dir)
	return err == nil && st.IsDir()
}
