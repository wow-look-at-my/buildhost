//go:build windows

// Package diskusage reports the size and free space of a filesystem.
package diskusage

import "errors"

type Usage struct {
	Total uint64
	Free  uint64
	Used  uint64
}

// Get is not implemented on Windows.
func Get(path string) (Usage, error) {
	return Usage{}, errors.New("diskusage: not implemented on windows")
}
