//go:build !windows

package admin

import "syscall"

type diskUsage struct {
	Total uint64
	Free  uint64
	Used  uint64
}

func getDiskUsage(path string) (diskUsage, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return diskUsage{}, err
	}
	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bavail * uint64(stat.Bsize)
	return diskUsage{
		Total: total,
		Free:  free,
		Used:  total - free,
	}, nil
}
