package dl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/repackage"
	"github.com/wow-look-at-my/buildhost/internal/storage"
	"github.com/wow-look-at-my/buildhost/internal/strip"
)

var handler Handler

func init() {
	auth.OnReady(func() {
		handler.DB = auth.DB()
		handler.Store = auth.Store()
		handler.Gen = repackage.NewGenerator(auth.Store(), auth.BaseURL())
	})
	auth.Handle("GET /dl/{project}/latest/{os}/{arch}", parseRoute, handler.DownloadLatest)
	auth.Handle("GET /dl/{project}/branch/{branch}/{os}/{arch}", parseRoute, handler.DownloadBranch)
	auth.Handle("GET /dl/{project}/{version}/{os}/{arch}", parseRoute, handler.Download)
}

type route struct {
	project string
	version string
	branch  string
	os      string
	arch    string
}

func (r route) ProjectName() string      { return r.project }
func (r route) Access() auth.AccessLevel { return auth.ReadAccess }

func parseRoute(r *http.Request) auth.RouteInfo {
	return route{
		project: r.PathValue("project"),
		version: r.PathValue("version"),
		branch:  r.PathValue("branch"),
		os:      r.PathValue("os"),
		arch:    r.PathValue("arch"),
	}
}

func routeFrom(ctx context.Context) route {
	return auth.RouteInfoFrom(ctx).(route)
}

type Handler struct {
	DB    *db.DB
	Store storage.Storage
	Gen   *repackage.Generator
}

func handleDBErr(w http.ResponseWriter, r *http.Request, err error) bool {
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return true
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return true
	}
	return false
}

const cacheImmutable = "public, max-age=31536000, immutable"

func (h *Handler) Download(w http.ResponseWriter, r *http.Request) {
	project := auth.ProjectFrom(r.Context())
	rt := routeFrom(r.Context())

	var (
		release *db.Release
		err     error
	)
	if rt.version == "latest" {
		w.Header().Set("Cache-Control", "no-cache")
		release, err = h.DB.GetLatestRelease(r.Context(), project.ID)
	} else {
		w.Header().Set("Cache-Control", cacheImmutable)
		release, err = h.DB.GetRelease(r.Context(), project.ID, rt.version)
	}
	if handleDBErr(w, r, err) {
		return
	}

	h.serveArtifact(w, r, project, release, rt.os, rt.arch)
}

func (h *Handler) DownloadLatest(w http.ResponseWriter, r *http.Request) {
	project := auth.ProjectFrom(r.Context())
	rt := routeFrom(r.Context())

	w.Header().Set("Cache-Control", "no-cache")
	release, err := h.DB.GetLatestRelease(r.Context(), project.ID)
	if handleDBErr(w, r, err) {
		return
	}

	h.serveArtifact(w, r, project, release, rt.os, rt.arch)
}

func (h *Handler) DownloadBranch(w http.ResponseWriter, r *http.Request) {
	project := auth.ProjectFrom(r.Context())
	rt := routeFrom(r.Context())

	w.Header().Set("Cache-Control", "no-cache")
	release, err := h.DB.GetLatestReleaseByBranch(r.Context(), project.ID, rt.branch)
	if handleDBErr(w, r, err) {
		return
	}

	h.serveArtifact(w, r, project, release, rt.os, rt.arch)
}

func (h *Handler) serveArtifact(w http.ResponseWriter, r *http.Request, project *db.Project, release *db.Release, osStr, archStr string) {
	artifact, err := h.DB.GetArtifact(r.Context(), release.ID, osStr, archStr)
	if handleDBErr(w, r, err) {
		return
	}

	_ = h.DB.IncrementDownloadCount(r.Context(), artifact.ID)

	format := r.URL.Query().Get("format")
	wantDebug := r.URL.Query().Get("debug") == "1"

	if wantDebug || format == "" || format == "raw" {
		data, err := h.readArtifact(r.Context(), artifact.StorageKey)
		if err != nil {
			http.Error(w, "blob not found", http.StatusNotFound)
			return
		}
		if (artifact.Kind == db.KindBinary || artifact.Kind == db.KindLibrary) && strip.Available() {
			if result, serr := strip.StripBytes(data); serr == nil {
				if wantDebug {
					h.serveBytes(w, result.Debug, fmt.Sprintf("%s-%s.debug", project.Name, release.Version))
					return
				}
				data = result.Stripped
			} else if wantDebug {
				http.NotFound(w, r)
				return
			}
		} else if wantDebug {
			http.NotFound(w, r)
			return
		}
		h.serveBytes(w, data, project.Name)
		return
	}

	output, err := h.Gen.Generate(r.Context(), repackage.Format(format), *project, *release, *artifact)
	if err != nil {
		http.Error(w, "format not available", http.StatusNotFound)
		return
	}
	h.serveOutput(w, output)
}

func (h *Handler) readArtifact(ctx context.Context, key string) ([]byte, error) {
	rc, _, err := h.Store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

func (h *Handler) serveBytes(w http.ResponseWriter, data []byte, filename string) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.Write(data)
}

func (h *Handler) serveOutput(w http.ResponseWriter, out *repackage.Output) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", out.Filename))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", out.Size))
	io.Copy(w, out.Reader)
}

func (h *Handler) serveBlob(w http.ResponseWriter, r *http.Request, key, filename string) {
	rc, size, err := h.Store.Get(r.Context(), key)
	if err != nil {
		http.Error(w, "blob not found", http.StatusNotFound)
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
	io.Copy(w, rc)
}
