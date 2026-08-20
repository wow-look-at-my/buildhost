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
	// A slash-namespaced name needs its own route: the {project}.rb pattern is
	// one path segment, so "git-fixed/git-fsck" never matched it and fell
	// through to GET /{project}, which read the whole path as a project name
	// and answered "project not found".
	auth.ServiceHandle("brew", "GET /Formula/{path...}", handler.parseRoute, handler.ServeFormula)
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
	// Smart-HTTP git endpoints (see smart.go), always the same pair UNDER a
	// tap path -- never at a host root, whose namespace belongs to formula and
	// project routes ("info/refs" is even a valid slash-namespaced project
	// name). The literal info/refs routes outscore the {path...} file routes
	// and dispatch on ?service=: absent means the dumb-HTTP file serving
	// above, verbatim; git-upload-pack means the smart ref advertisement. The
	// POST routes are the smart fetch; the dumb protocol never POSTs. Both
	// brew.{domain}/tap.git and git.{domain}/brew/tap.git are first-class,
	// directly served clone URLs (the smart pair never redirects; only the
	// bare /tap.git path and the dumb file paths keep the anonymous 301);
	// /private/tap.git keeps its 401 Basic challenge for anonymous requests.
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
// they are equal, but tap formula FILENAMES fold the slash namespace
// (tapFormulaName: "gcc/pgo" -> gcc-pgo.rb), so the per-formula URL a user
// copies out of the tap carries the folded name and used to 404. When no
// project matches the literal name, fall back to the project whose folded tap
// name matches exactly (a literally named project always wins). The fold
// candidates are restricted to projects the REQUEST may read
// (auth.TokenCanReadProject -- exactly the rule that decides tap membership),
// so the fallback can only ever name a project whose formula the same request
// already sees in its tap: an anonymous probe of a private project's folded
// name stays indistinguishable from a nonexistent project (404), never a
// 401 existence leak. requireProject still applies its normal auth to
// whatever this resolves -- resolution routes, it never grants.
func (h *Handler) parseRoute(r *http.Request) auth.RouteInfo {
	return route{project: h.resolveFormulaProject(r)}
}

func (h *Handler) resolveFormulaProject(r *http.Request) string {
	name := r.PathValue("project")
	if name == "" {
		name = strings.TrimSuffix(r.PathValue("path"), ".rb")
	}
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
	// resolves deterministically to the first visible folded match.
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
	// volume is the only writable path in the hardened image, never /tmp).
	TmpDir string
	// DataDir is the persistent data root. The tap's per-lineage git history
	// lives under {DataDir}/brew-tap (taphistory.go) -- deliberately NOT under
	// the swept tmp scratch root, because the history must survive restarts to
	// keep `brew update` clients fast-forwarding.
	DataDir string

	// tapMu guards tapSnaps and tapPins: the live tap lineages (an open
	// os.Root over each persistent history dir), keyed by (request base URL,
	// credential scope) -- see tapcache.go. Refreshes and file opens happen
	// under the mutex, so a lineage dir is never removed between map
	// resolution and open. tapPins counts in-flight smart-HTTP requests per
	// lineage DIRECTORY; a pinned directory is skipped by the disk-cap
	// eviction so a streaming pack can never lose its objects mid-walk.
	tapMu    sync.Mutex
	tapSnaps map[string]*tapLineage
	tapPins  map[string]int
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

	artifacts, err := h.DB.ListArtifactsByPlatform(r.Context(), release.ID)
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
