package static

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/repackage"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

var handler staticHandler

func init() {
	RegisterFmt(&rawFmt{})
	RegisterFmt(&symbolsFmt{})

	auth.OnReady(func() {
		handler.DB = auth.DB()
		handler.Store = auth.Store()
		handler.BaseURL = auth.BaseURL()
		handler.TmpDir = auth.DataDir() + "/tmp"

		for _, format := range repackage.RegisteredFormats() {
			RegisterRepackageFmt(format)
		}
	})
	auth.Handle("GET /static", parseRoute, handler.Serve)
}

type route struct {
	project string
}

func (r route) ProjectName() string      { return r.project }
func (r route) Access() auth.AccessLevel { return auth.ReadAccess }

func parseRoute(r *http.Request) auth.RouteInfo {
	id := r.URL.Query().Get("id")
	id = strings.TrimPrefix(id, "@buildhost/")
	return route{project: id}
}

type staticHandler struct {
	DB      *db.DB
	Store   storage.Storage
	BaseURL string
	TmpDir  string
}

func (h *staticHandler) Serve(w http.ResponseWriter, r *http.Request) {
	canonical := canonicalQuery(r.URL.Query())
	if canonical != r.URL.RawQuery {
		u := *r.URL
		u.RawQuery = canonical
		http.Redirect(w, r, u.String(), http.StatusMovedPermanently)
		return
	}

	q := r.URL.Query()
	version := q.Get("v")
	if version == "" || version == "latest" {
		http.Error(w, "v is required and must be a specific version", http.StatusBadRequest)
		return
	}

	osStr := q.Get("os")
	archStr := q.Get("arch")
	fmtStr := q.Get("fmt")
	if osStr == "" || archStr == "" {
		http.Error(w, "os and arch are required", http.StatusBadRequest)
		return
	}
	if fmtStr == "" {
		fmtStr = "raw"
	}

	f, ok := LookupFmt(fmtStr)
	if !ok {
		http.Error(w, "unsupported format", http.StatusBadRequest)
		return
	}

	project := auth.ProjectFrom(r.Context())

	release, err := resolveVersion(r.Context(), h.DB, project.ID, version)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	sctx := ServeContext{
		Project: *project,
		Release: *release,
		Store:   h.Store,
		BaseURL: h.BaseURL,
		TmpDir:  h.TmpDir,
	}

	if osStr != "any" && archStr != "any" {
		artifact, err := h.DB.GetArtifact(r.Context(), release.ID, osStr, archStr)
		if errors.Is(err, db.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		sctx.Artifact = *artifact
	}

	etag := computeETag(sctx, fmtStr)
	if match := r.Header.Get("If-None-Match"); match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")

	if err := f.Serve(w, r, sctx); err != nil {
		if w.Header().Get("Content-Length") == "" {
			http.Error(w, "not found", http.StatusNotFound)
		}
	}
}

func computeETag(ctx ServeContext, fmtStr string) string {
	var source string
	if ctx.Artifact.StorageKey != "" {
		source = ctx.Artifact.StorageKey + "-" + fmtStr
	} else {
		source = ctx.Project.Name + "-" + ctx.Release.Version + "-" + fmtStr
	}
	h := sha256.Sum256([]byte(source))
	return fmt.Sprintf(`"%x"`, h[:8])
}

func resolveVersion(ctx context.Context, database *db.DB, projectID int64, version string) (*db.Release, error) {
	rel, err := database.GetRelease(ctx, projectID, version)
	if err == nil {
		return rel, nil
	}
	if !errors.Is(err, db.ErrNotFound) {
		return nil, err
	}

	trimmed := strings.TrimPrefix(version, "v")
	if trimmed != version {
		rel, err = database.GetRelease(ctx, projectID, trimmed)
		if err == nil {
			return rel, nil
		}
		if !errors.Is(err, db.ErrNotFound) {
			return nil, err
		}
	}

	if strings.HasSuffix(version, ".0.0") {
		rel, err = database.GetRelease(ctx, projectID, strings.TrimSuffix(version, ".0.0"))
		if err == nil {
			return rel, nil
		}
	}

	return nil, db.ErrNotFound
}
