//go:build windows

package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procCreateMutexW = kernel32.NewProc("CreateMutexW")
	procCloseHandle  = kernel32.NewProc("CloseHandle")
)

// InstanceLock prevents concurrent writers on the same storage root.
type InstanceLock struct {
	handle uintptr
	path   string
}

func AcquireInstanceLock(root string) (*InstanceLock, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	name, err := syscall.UTF16PtrFromString(`Local\QuickAnalyser-` + sanitizeMutexName(abs))
	if err != nil {
		return nil, err
	}
	r0, _, e1 := procCreateMutexW.Call(0, 1, uintptr(unsafe.Pointer(name)))
	if r0 == 0 {
		return nil, fmt.Errorf("create mutex: %w", e1)
	}
	if e1 == syscall.ERROR_ALREADY_EXISTS {
		procCloseHandle.Call(r0)
		return nil, fmt.Errorf("another QuickAnalyser instance owns this storage root")
	}
	marker := filepath.Join(abs, ".quickanalyser.lock")
	_ = os.WriteFile(marker, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600)
	return &InstanceLock{handle: r0, path: marker}, nil
}

func (l *InstanceLock) Release() {
	if l == nil {
		return
	}
	if l.handle != 0 {
		procCloseHandle.Call(l.handle)
		l.handle = 0
	}
	if l.path != "" {
		_ = os.Remove(l.path)
	}
}

func sanitizeMutexName(s string) string {
	b := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b = append(b, r)
		default:
			b = append(b, '_')
		}
	}
	if len(b) > 180 {
		b = b[len(b)-180:]
	}
	return string(b)
}
