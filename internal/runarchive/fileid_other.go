//go:build !unix

package runarchive

import "io/fs"

// fileID reports no identity off a unix build, so each name of a hardlink packs as its own file.
func fileID(fs.FileInfo) (id string, nlink uint64, ok bool) { return "", 0, false }
