package brew

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/repackage"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

var handler Handler

func init() {
	auth.OnReady(func() {
		handler.DB = auth.DB()
		handler.Store = auth.Store()
		handler.Gen = repackage.NewGenerator(auth.Store(), auth.DB(), auth.DataDir()+"/tmp")
		handler.TmpDir = auth.DataDir() + "/tmp"
		// Drop any cached tap snapshot from a previous wiring and sweep
		// snapshot dirs a crashed previous process left under the data dir.
		handler.resetTapCache()
	})
	auth.ServiceHandle("brew", "GET /{project}", parseRoute, handler.ServeFormula)
	auth.ServiceHandle("brew", "GET /Formula/{project}.rb", parseRoute, handler.ServeFormula)
	auth.ServiceHandleRaw("brew", "GET /tap.git", handler.RedirectTap)
	auth.ServiceHandleRaw("brew", "GET /tap.git/{path...}", handler.RedirectTap)
	auth.ServiceHandleRaw("git", "GET /brew/tap.git", handler.ServeTap)
	auth.ServiceHandleRaw("git", "GET /brew/tap.git/{path...}", handler.ServeTap)
}

type route struct {
	project string
}

func (r route) ProjectName() string      { return r.project }
func (r route) Access() auth.AccessLevel { return auth.ReadAccess }

func parseRoute(r *http.Request) auth.RouteInfo {
	return route{project: r.PathValue("project")}
}

type Handler struct {
	DB    *db.DB
	Store storage.Storage
	Gen   *repackage.Generator
	// TmpDir is the scratch root ({DataDir}/tmp in production -- the data
	// volume is the only writable path in the hardened image, never /tmp).
	// Tap snapshots are materialized under TmpDir/brew-tap.
	TmpDir string

	// tapMu guards tapSnap: the single cached on-disk tap snapshot (see
	// tapcache.go). Rebuilds and file opens happen under the mutex, so a
	// snapshot dir is never removed between pointer resolution and open.
	tapMu   sync.Mutex
	tapSnap *tapSnapshot
}

func (h *Handler) ServeFormula(w http.ResponseWriter, r *http.Request) {
	project := auth.ProjectFrom(r.Context())

	release, err := h.DB.GetLatestRelease(r.Context(), project.ID)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	artifacts, err := h.DB.ListArtifacts(r.Context(), release.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	out, err := h.formulaForRelease(r.Context(), *project, *release, artifacts, auth.RequestRootURL(r))
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-ruby")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", project.Name+".rb"))
	io.Copy(w, out.Reader)
}
