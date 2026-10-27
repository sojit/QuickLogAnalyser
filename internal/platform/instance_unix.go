//go:build !windows

package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// InstanceLock prevents concurrent writers on the same storage root.
type InstanceLock struct {
	file *os.File
}

func AcquireInstanceLock(root string) (*InstanceLock, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(root, ".quickanalyser.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("another QuickAnalyser instance owns this storage root: %w", err)
	}
	_ = f.Truncate(0)
	_, _ = f.WriteString(fmt.Sprintf("%d\n", os.Getpid()))
	return &InstanceLock{file: f}, nil
}

func (l *InstanceLock) Release() {
	if l == nil || l.file == nil {
		return
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	name := l.file.Name()
	_ = l.file.Close()
	_ = os.Remove(name)
	l.file = nil
}
