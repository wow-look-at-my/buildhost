package goproxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"golang.org/x/mod/module"
)

// sourceName is what a module's content is fetched from, recorded on the cache
// row and shown on the dashboard.
func (s *Service) sourceName(modPath string) string {
	if s.isPrivate(modPath) {
		return "github"
	}
	return "upstream"
}

// versionInfo is the @latest / .info payload.
type versionInfo struct {
	Version string    `json:"Version"`
	Time    time.Time `json:"Time"`
}

// cachedOrFetch returns the cached record for modPath@version, fetching and
// caching it on a miss. needZip asks for the module zip to be materialized too;
// an .info or .mod request resolves the version without ever downloading a
// tarball.
func (s *Service) cachedOrFetch(ctx context.Context, modPath, version string, needZip bool) (*db.GoproxyCached, bool, error) {
	if c, err := s.db.GetGoproxyCached(ctx, modPath, version); err == nil {
		if !needZip || c.ZipKey != "" {
			return c, true, nil
		}
	} else if !errors.Is(err, db.ErrNotFound) {
		return nil, false, err
	}

	key := modPath + "@" + version
	if needZip {
		key += "+zip"
	}
	var (
		out *db.GoproxyCached
		hit bool
	)
	err := s.inflight.do(key, func() error {
		// Re-check inside the flight: the request we waited behind may have just
		// filled this in.
		if c, err := s.db.GetGoproxyCached(ctx, modPath, version); err == nil {
			if !needZip || c.ZipKey != "" {
				out, hit = c, true
				return nil
			}
		}
		c, err := s.fetch(ctx, modPath, version, needZip)
		if err != nil {
			return err
		}
		out = c
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return out, hit, nil
}

// fetch pulls a version from its source and records it. Failures are recorded
// against the module too, so the dashboard shows a module that is failing
// without anyone having to read a log.
func (s *Service) fetch(ctx context.Context, modPath, version string, needZip bool) (*db.GoproxyCached, error) {
	moduleID, idErr := s.db.GoproxyModuleID(ctx, modPath, s.sourceName(modPath))
	if idErr != nil {
		return nil, idErr
	}

	c, err := s.fetchFrom(ctx, modPath, version, needZip)
	if err != nil {
		e := asError(modPath, version, err)
		if markErr := s.db.MarkGoproxyFailure(ctx, moduleID, e.Kind.String(), e.Error()); markErr != nil {
			slog.Error("goproxy: recording module failure", "err", markErr, "module", modPath)
		}
		return nil, e
	}

	if err := s.db.PutGoproxyCached(ctx, moduleID, c); err != nil {
		return nil, err
	}
	if err := s.db.MarkGoproxySuccess(ctx, moduleID); err != nil {
		slog.Error("goproxy: recording module success", "err", err, "module", modPath)
	}
	return c, nil
}

func (s *Service) fetchFrom(ctx context.Context, modPath, version string, needZip bool) (*db.GoproxyCached, error) {
	if s.isPrivate(modPath) {
		return s.fetchGitHub(ctx, modPath, version, needZip)
	}
	return s.fetchUpstream(ctx, modPath, version, needZip)
}

func (s *Service) fetchGitHub(ctx context.Context, modPath, version string, needZip bool) (*db.GoproxyCached, error) {
	res, err := s.resolveVersion(ctx, modPath, version)
	if err != nil {
		return nil, err
	}
	c := &db.GoproxyCached{
		Version:     res.Version,
		CommitSHA:   res.Commit,
		CommittedAt: res.Time,
		GoMod:       res.GoMod,
	}
	if !needZip {
		return c, nil
	}

	mv := module.Version{Path: modPath, Version: res.Version}
	zipPath, _, err := s.github.buildZip(ctx, res.Ref, res.Commit, mv)
	if err != nil {
		return nil, err
	}
	defer os.Remove(zipPath)

	key, stored, err := s.storeFile(ctx, zipPath)
	if err != nil {
		return nil, upstreamErr(modPath, version, upstreamGitHub, 0, "storing module zip", err)
	}
	c.ZipKey, c.ZipSize = key, stored
	return c, nil
}

func (s *Service) fetchUpstream(ctx context.Context, modPath, version string, needZip bool) (*db.GoproxyCached, error) {
	infoRaw, err := s.upstream.getBytes(ctx, modPath, version, "@v/"+escapeVersion(version)+".info")
	if err != nil {
		return nil, err
	}
	var info versionInfo
	if err := json.Unmarshal(infoRaw, &info); err != nil {
		return nil, upstreamErr(modPath, version, s.upstream.base, 200, "decoding .info", err)
	}
	gomod, err := s.upstream.getBytes(ctx, modPath, version, "@v/"+escapeVersion(version)+".mod")
	if err != nil {
		return nil, err
	}
	c := &db.GoproxyCached{
		Version:     info.Version,
		CommittedAt: info.Time,
		GoMod:       gomod,
	}
	if c.Version == "" {
		c.Version = version
	}
	if !needZip {
		return c, nil
	}

	rc, err := s.upstream.get(ctx, modPath, version, "@v/"+escapeVersion(version)+".zip")
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	key, size, err := s.store.Put(ctx, rc)
	if err != nil {
		return nil, upstreamErr(modPath, version, s.upstream.base, 0, "storing module zip", err)
	}
	c.ZipKey, c.ZipSize = key, size
	return c, nil
}

// storeFile streams a file on disk into blob storage.
func (s *Service) storeFile(ctx context.Context, path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	return s.store.Put(ctx, f)
}

// openZip streams a cached module zip out of blob storage, reporting the size
// storage itself holds rather than the cache row's copy of it.
func (s *Service) openZip(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	return s.store.Get(ctx, key)
}

// escapeVersion applies the module proxy's case encoding to a version. Versions
func escapeVersion(v string) string {
	e, err := module.EscapeVersion(v)
	if err != nil {
		return v
	}
	return e
}

// unescapeVersion reverses the proxy's case encoding on an incoming request.
func unescapeVersion(v string) (string, error) {
	return module.UnescapeVersion(v)
}
