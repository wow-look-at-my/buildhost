//go:build !windows

// Package diskusage reports the size and free space of a filesystem.
package diskusage

import "syscall"

type Usage struct {
	Total uint64
	Free  uint64
	Used  uint64
}

// Get reports the filesystem that holds path. Free counts what an
// unprivileged process can still write.
func Get(path string) (Usage, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return Usage{}, err
	}
	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bavail * uint64(stat.Bsize)
	return Usage{
		Total: total,
		Free:  free,
		Used:  total - free,
	}, nil
}
