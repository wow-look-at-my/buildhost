package brew

import (
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
		// leftovers (legacy scratch snapshots, orphaned temp files). The
		// persistent tap history under {DataDir}/brew-tap survives -- that is
		// what keeps `brew update` clients fast-forwarding across restarts.
		handler.resetTapCache()
	})
	auth.ServiceHandle("brew", "GET /{project}", handler.parseRoute, handler.ServeFormula)
	auth.ServiceHandle("brew", "GET /Formula/{project}.rb", handler.parseRoute, handler.ServeFormula)
	auth.ServiceHandleRaw("brew", "GET /tap.git", handler.RedirectTap)
	auth.ServiceHandleRaw("brew", "GET /tap.git/{path...}", handler.RedirectTap)
	// The authenticated tap: challenges anonymous requests (401 + Basic) so
	// creds-in-URL git clients actually transmit their credential, then serves
	// a tap scoped to it (all public projects plus the private ones it can
	// read). See ServePrivateTap for why this is a separate path from /tap.git.
	auth.ServiceHandleRaw("brew", "GET /private/tap.git", handler.ServePrivateTap)
	auth.ServiceHandleRaw("brew", "GET /private/tap.git/{path...}", handler.ServePrivateTap)
	auth.ServiceHandleRaw("git", "GET /brew/tap.git", handler.ServeTap)
	auth.ServiceHandleRaw("git", "GET /brew/tap.git/{path...}", handler.ServeTap)
}

type route struct {
	project string
}

func (r route) ProjectName() string      { return r.project }
func (r route) Access() auth.AccessLevel { return auth.ReadAccess }

// parseRoute resolves the {project} path value to a project name. Normally
// they are equal, but tap formula FILENAMES fold the slash namespace
// (tapFormulaName: "gcc/pgo" -> gcc-pgo.rb), so the per-formula URL a user
// copies out of the tap carries the folded name and used to 404. When no
// project matches the literal name, fall back to the project whose folded tap
// name matches exactly (a literally named project always wins). requireProject
// applies its normal read auth to whatever this resolves, so a private
// project's formula stays exactly as gated as under its unfolded name.
func (h *Handler) parseRoute(r *http.Request) auth.RouteInfo {
	return route{project: h.resolveFormulaProject(r)}
}

func (h *Handler) resolveFormulaProject(r *http.Request) string {
	name := r.PathValue("project")
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
	// resolves deterministically to the first folded match.
	for _, p := range projects {
		if strings.Contains(p.Name, "/") && tapFormulaName(p.Name) == name {
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
	// volume is the only writable path in the hardened image, never /tmp).
	TmpDir string
	// DataDir is the persistent data root. The tap's per-lineage git history
	// lives under {DataDir}/brew-tap (taphistory.go) -- deliberately NOT under
	// the swept tmp scratch root, because the history must survive restarts to
	// keep `brew update` clients fast-forwarding.
	DataDir string

	// tapMu guards tapSnaps: the live tap lineages (an open os.Root over each
	// persistent history dir), keyed by (request base URL, credential scope)
	// -- see tapcache.go. Refreshes and file opens happen under the mutex, so
	// a lineage dir is never removed between map resolution and open.
	tapMu    sync.Mutex
	tapSnaps map[string]*tapLineage
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
	if project.IsPrivate {
		// Only reachable with a credential; never let a shared cache keep it.
		w.Header().Set("Cache-Control", "private, no-store")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", project.Name+".rb"))
	io.Copy(w, out.Reader)
}
