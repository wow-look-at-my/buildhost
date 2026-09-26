package sites

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/binarchive"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/retention"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

// ConvertTarSites rewrites every site blob that is still a plain tar into an
// indexed archive. The serve path reads archives only, so the server runs this
// before it takes traffic. A site that fails stays unreadable and is named in
// the returned error.
func ConvertTarSites(ctx context.Context, database *db.DB, store storage.Storage, tmpDir string) error {
	sites, err := database.ListAllSites(ctx)
	if err != nil {
		return fmt.Errorf("list sites: %w", err)
	}

	var failed []string
	converted := 0
	for _, s := range sites {
		_, closer, err := openArchive(ctx, store, s.StorageKey)
		if err == nil {
			closer.Close()
			continue
		}
		if !errors.Is(err, errNotArchive) && !errors.Is(err, storage.ErrRandomUnsupported) {
			slog.Error("sites: cannot read site blob", "site_id", s.ID, "branch", s.Branch, "err", err)
			failed = append(failed, fmt.Sprintf("site %d (%s)", s.ID, s.Branch))
			continue
		}
		if err := convertSite(ctx, database, store, tmpDir, s); err != nil {
			slog.Error("sites: convert tar site", "site_id", s.ID, "branch", s.Branch, "err", err)
			failed = append(failed, fmt.Sprintf("site %d (%s)", s.ID, s.Branch))
			continue
		}
		converted++
	}

	if converted > 0 {
		slog.Info("sites: converted tar sites to archives", "count", converted)
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d site(s) could not be converted to archives: %s", len(failed), strings.Join(failed, ", "))
	}
	return nil
}

func convertSite(ctx context.Context, database *db.DB, store storage.Storage, tmpDir string, s db.Site) error {
	rc, _, err := store.Get(ctx, s.StorageKey)
	if err != nil {
		return fmt.Errorf("read blob: %w", err)
	}
	defer rc.Close()

	tmp, err := os.CreateTemp(tmpDir, "site-convert-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	defer func() {
		tmp.Close()
		os.Remove(tmp.Name())
	}()

	if _, err := binarchive.WriteFromTar(tmp, tar.NewReader(rc), binarchive.Limits{
		MaxEntries:   maxFileCount,
		MaxTotalSize: maxSiteDecompressedSize,
	}); err != nil {
		return fmt.Errorf("archive tar: %w", err)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind temp: %w", err)
	}

	newKey, size, err := putUncompressed(ctx, store, tmp)
	if err != nil {
		return fmt.Errorf("store archive: %w", err)
	}
	replaced, err := database.ReplaceSiteBlob(ctx, s.ID, s.StorageKey, newKey, size)
	if err != nil {
		return err
	}
	if !replaced {
		// A publish replaced the row earliest. The new archive may now be garbage.
		_, _ = retention.DeleteBlobIfUnreferenced(ctx, database, store, newKey, true)
		return nil
	}
	if _, err := retention.DeleteBlobIfUnreferenced(ctx, database, store, s.StorageKey, true); err != nil {
		slog.Error("sites: delete converted tar blob", "key", s.StorageKey, "err", err)
	}
	return nil
}
