package sites

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/wow-look-at-my/buildhost/internal/binarchive"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

// errNotArchive marks a site blob that is not a binpazer archive.
var errNotArchive = errors.New("site blob is not a binpazer archive")

// File is a single regular file in a deployed site.
type File struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// openArchive opens a site blob through its index. The blob must be an
// uncompressed binpazer archive in a store that reads at an offset.
func openArchive(ctx context.Context, store storage.Storage, key string) (*binarchive.Archive, io.Closer, error) {
	rg, ok := store.(storage.RandomGetter)
	if !ok {
		return nil, nil, fmt.Errorf("site blob %s: the store cannot read at an offset", key)
	}
	ra, size, err := rg.OpenReaderAt(ctx, key)
	if err != nil {
		return nil, nil, fmt.Errorf("site blob %s: %w", key, err)
	}
	head := make([]byte, len(binarchive.Magic))
	if _, err := ra.ReadAt(head, 0); err != nil || !binarchive.IsArchive(head) {
		ra.Close()
		return nil, nil, fmt.Errorf("site blob %s: %w", key, errNotArchive)
	}
	a, err := binarchive.Open(ra, size)
	if err != nil {
		ra.Close()
		return nil, nil, fmt.Errorf("site blob %s: %w", key, err)
	}
	return a, ra, nil
}

// ListFiles returns every file a site deployment serves, sorted by path.
func ListFiles(ctx context.Context, store storage.Storage, storageKey string) ([]File, error) {
	a, closer, err := openArchive(ctx, store, storageKey)
	if err != nil {
		return nil, err
	}
	defer closer.Close()

	entries := a.Entries()
	files := make([]File, len(entries))
	for i, e := range entries {
		files[i] = File{Path: e.Path, Size: e.Size}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}
