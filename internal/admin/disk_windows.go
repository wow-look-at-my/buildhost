//go:build windows

package admin

type diskUsage struct {
	Total uint64
	Free  uint64
	Used  uint64
}

func getDiskUsage(path string) (diskUsage, error) {
	return diskUsage{}, nil
}
