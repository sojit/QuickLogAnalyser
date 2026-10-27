//go:build !windows

package storage

import "syscall"

func DiskSpace(path string) (free, total uint64) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0
	}
	total = uint64(st.Blocks) * uint64(st.Bsize)
	free = uint64(st.Bavail) * uint64(st.Bsize)
	return free, total
}
