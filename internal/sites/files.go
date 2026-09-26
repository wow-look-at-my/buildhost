package sites

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"

	"github.com/wow-look-at-my/buildhost/internal/binarchive"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

// File is a single regular file in a deployed site, as the serve path resolves it.
type File struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// ListFiles returns every file a site deployment serves, sorted by path.
func ListFiles(ctx context.Context, store storage.Storage, storageKey string) ([]File, error) {
	if files, ok, err := listArchiveFiles(ctx, store, storageKey); ok {
		return files, err
	}

	rc, _, err := store.Get(ctx, storageKey)
	if err != nil {
		return nil, fmt.Errorf("opening site blob %s: %w", storageKey, err)
	}
	defer rc.Close()

	files := []File{}
	tr := tar.NewReader(rc)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading site tar %s: %w", storageKey, err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		files = append(files, File{Path: path.Clean(hdr.Name), Size: hdr.Size})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

// listArchiveFiles reports ok=false when the blob is not a binpazer archive,
// so the caller falls back to the tar scan.
func listArchiveFiles(ctx context.Context, store storage.Storage, storageKey string) ([]File, bool, error) {
	rg, ok := store.(storage.RandomGetter)
	if !ok {
		return nil, false, nil
	}
	ra, size, err := rg.OpenReaderAt(ctx, storageKey)
	if err != nil {
		return nil, false, nil
	}
	defer ra.Close()

	head := make([]byte, len(binarchive.Magic))
	if _, err := ra.ReadAt(head, 0); err != nil || !binarchive.IsArchive(head) {
		return nil, false, nil
	}
	a, err := binarchive.Open(ra, size)
	if err != nil {
		return nil, true, fmt.Errorf("opening site archive %s: %w", storageKey, err)
	}

	entries := a.Entries()
	files := make([]File, len(entries))
	for i, e := range entries {
		files[i] = File{Path: e.Path, Size: e.Size}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, true, nil
}
