package brew

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
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
		handler.DataDir = auth.DataDir()
		// Drop live lineage entries from a previous wiring and sweep crash
		handler.resetTapCache()
	})
	auth.ServiceHandle("brew", "GET /{project}", handler.parseRoute, handler.ServeFormula)
	auth.ServiceHandle("brew", "GET /Formula/{project}.rb", handler.parseRoute, handler.ServeFormula)
	// A slash-namespaced name needs its own route.
	auth.ServiceHandle("brew", "GET /Formula/{path...}", handler.parseRoute, handler.ServeFormula)
	auth.ServiceHandleRaw("brew", "GET /tap.git", handler.RedirectTap)
	auth.ServiceHandleRaw("brew", "GET /tap.git/{path...}", handler.RedirectTap)
	auth.ServiceHandleRaw("brew", "GET /private/tap.git", handler.ServePrivateTap)
	auth.ServiceHandleRaw("brew", "GET /private/tap.git/{path...}", handler.ServePrivateTap)
	auth.ServiceHandleRaw("git", "GET /brew/tap.git", handler.ServeTap)
	auth.ServiceHandleRaw("git", "GET /brew/tap.git/{path...}", handler.ServeTap)
	// Smart-HTTP git endpoints (see smart.go).
	auth.ServiceHandleRaw("brew", "GET /tap.git/info/refs", handler.ServeTapInfoRefs)
	auth.ServiceHandleRaw("brew", "POST /tap.git/git-upload-pack", handler.ServeTapUploadPack)
	auth.ServiceHandleRaw("brew", "GET /private/tap.git/info/refs", handler.ServePrivateTapInfoRefs)
	auth.ServiceHandleRaw("brew", "POST /private/tap.git/git-upload-pack", handler.ServePrivateTapUploadPack)
	auth.ServiceHandleRaw("git", "GET /brew/tap.git/info/refs", handler.ServeTapInfoRefs)
	auth.ServiceHandleRaw("git", "POST /brew/tap.git/git-upload-pack", handler.ServeTapUploadPack)
}

type route struct {
	project string
}

func (r route) ProjectName() string      { return r.project }
func (r route) Access() auth.AccessLevel { return auth.ReadAccess }

// parseRoute resolves the {project} path value to a project name. Normally
func (h *Handler) parseRoute(r *http.Request) auth.RouteInfo {
	return route{project: h.resolveFormulaProject(r)}
}

// formulaRequestName returns the formula name the request asks for, split at
// "@" into the name and the requested version ("" for the unversioned
// formula). A project name never contains "@".
func formulaRequestName(r *http.Request) (string, string) {
	name := r.PathValue("project")
	if name == "" {
		name = strings.TrimSuffix(r.PathValue("path"), ".rb")
	}
	name, version, _ := strings.Cut(name, "@")
	return name, version
}

func (h *Handler) resolveFormulaProject(r *http.Request) string {
	name, _ := formulaRequestName(r)
	if h.DB == nil || !strings.Contains(name, "-") {
		return name
	}
	if _, err := h.DB.GetProject(r.Context(), name); err == nil {
		return name
	}
	projects, err := h.DB.ListProjects(r.Context())
	if err != nil {
		return name
	}
	// ListProjects is name-ordered, so a (pathological) fold collision
	for _, p := range projects {
		if strings.Contains(p.Name, "/") && tapFormulaName(p.Name) == name && auth.TokenCanReadProject(r.Context(), &p) {
			return p.Name
		}
	}
	return name
}

type Handler struct {
	DB    *db.DB
	Store storage.Storage
	Gen   *repackage.Generator
	// TmpDir is the scratch root ({DataDir}/tmp in production -- the data
	TmpDir string
	// DataDir is the persistent data root. The tap's per-lineage git history
	DataDir string

	// tapMu guards tapSnaps and tapPins: the live tap lineages (an open
	tapMu    sync.Mutex
	tapSnaps map[string]*tapLineage
	tapPins  map[string]int

	// fillMu guards the background tar.gz digest filler (queueDigestFill).
	fillMu      sync.Mutex
	fillQueue   []digestFill
	filling     map[int64]bool
	fillRunning bool
	fillWG      sync.WaitGroup
}

func (h *Handler) ServeFormula(w http.ResponseWriter, r *http.Request) {
	project := auth.ProjectFrom(r.Context())

	_, version := formulaRequestName(r)
	mode := formulaLatest
	var release *db.Release
	var err error
	if version == "" {
		release, err = h.DB.GetLatestRelease(r.Context(), project.ID)
	} else {
		mode = formulaVersioned
		release, err = h.versionedRelease(r.Context(), project.ID, version)
	}
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	artifacts, err := h.DB.ListArtifactsByPlatform(r.Context(), release.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	out, err := h.formulaForRelease(r.Context(), *project, *release, artifacts, auth.RequestRootURL(r), repackage.BrewFormulaName(project.Name), mode)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-ruby")
	if project.IsPrivate {
		// Only reachable with a credential; never let a shared cache keep it.
		w.Header().Set("Cache-Control", "private, no-store")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	filename := project.Name
	if version != "" {
		filename = repackage.BrewVersionedFormulaName(project.Name, version)
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", filename+".rb"))
	io.Copy(w, out.Reader)
}

// versionedRelease finds the published default-branch release a name@version
// formula names: the same set, and the same version spelling, the tap's
// versioned formulas come from.
func (h *Handler) versionedRelease(ctx context.Context, projectID int64, version string) (*db.Release, error) {
	releases, err := h.DB.ListPublishedReleasesOnDefaultBranch(ctx, projectID)
	if err != nil {
		return nil, err
	}
	for _, release := range releases {
		if brewVersion(release) == version {
			return &release, nil
		}
	}
	return nil, db.ErrNotFound
}
