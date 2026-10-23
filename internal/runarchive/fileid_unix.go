//go:build unix

package runarchive

import (
	"io/fs"
	"syscall"
)

// fileID names the inode behind fi, so a second name of one file packs as a hardlink. ok is false when the host gives none.
func fileID(fi fs.FileInfo) (id string, nlink uint64, ok bool) {
	st, isStat := fi.Sys().(*syscall.Stat_t)
	if !isStat || st.Ino == 0 {
		return "", 0, false
	}
	return idString(uint64(st.Dev), uint64(st.Ino)), uint64(st.Nlink), true
}
