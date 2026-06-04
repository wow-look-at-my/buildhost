package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

//go:embed static/style.css
var styleCSS []byte

var styleETag string

var handler Handler

func init() {
	sum := sha256.Sum256(styleCSS)
	styleETag = fmt.Sprintf(`"%x"`, sum[:8])

	auth.OnReady(func() {
		handler.DB = auth.DB()
	})

	// Home and the stylesheet are public (no project context). Project and
	// release pages go through auth.Handle with HiddenReadAccess, so the shared
	// requireProject middleware enforces visibility (the one place auth lives)
	// and returns a 404 -- never a 401 -- for a private project the viewer may
	// not see, and never auto-provisions on a GET. Visibility is GitHub-style:
	// a private project is indistinguishable from one that does not exist.
	auth.HandleRaw("GET /", handler.Index)
	auth.HandleRaw("GET /_ui/style.css", handler.Stylesheet)
	auth.Handle("GET /projects/{project}", parseProjectRoute, handler.Project)
	auth.Handle("GET /projects/{project}/releases/{version}", parseProjectRoute, handler.Release)
}

type Handler struct {
	DB *db.DB
}

// route carries the project name for requireProject. HiddenReadAccess makes an
// unauthorized view a 404 rather than a 401, so private projects do not leak.
type route struct {
	project string
}

func (r route) ProjectName() string      { return r.project }
func (r route) Access() auth.AccessLevel { return auth.HiddenReadAccess }

func parseProjectRoute(r *http.Request) auth.RouteInfo {
	return route{project: r.PathValue("project")}
}

// Index renders the home page: every public project, plus any private project
// the request's token is authorized for (mirroring GET /api/v1/projects).
func (h *Handler) Index(w http.ResponseWriter, r *http.Request) {
	// GET / only serves the document root; anything else on the main domain
	// that reached here is genuinely unknown.
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	rows, err := h.DB.ListProjectSummaries(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}

	t := auth.TokenFrom(r.Context())
	visible := rows[:0:0]
	for _, p := range rows {
		if !p.IsPrivate || (t != nil && t.HasScope("read") && t.AuthorizedForProject(p.ID)) {
			visible = append(visible, p)
		}
	}

	h.render(w, r, "index", buildIndexView(visible))
}

// Project renders a single project's metadata, published releases, deployed
// sites, and install/download commands. requireProject (HiddenReadAccess) has
// already enforced visibility and put the project in the context.
func (h *Handler) Project(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	project := auth.ProjectFrom(ctx)

	rels, err := h.DB.ListReleaseSummaries(ctx, project.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	sites, err := h.DB.ListSites(ctx, project.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	// Determine install commands from the latest published release's contents.
	var latestVersion string
	var hasBinary bool
	for _, rel := range rels {
		if rel.Published {
			latestVersion = rel.Version
			break
		}
	}
	if latestVersion != "" {
		if latestRel, err := h.DB.GetRelease(ctx, project.ID, latestVersion); err == nil {
			if arts, err := h.DB.ListArtifacts(ctx, latestRel.ID); err == nil {
				hasBinary = hasNonDockerArtifact(arts)
			}
		}
	}

	h.render(w, r, "project", buildProjectView(r, project, rels, sites, hasBinary, latestVersion))
}

// Release renders one release's artifacts with per-format download links.
// requireProject (HiddenReadAccess) has already enforced project visibility;
// an unknown version within a visible project is a plain 404.
func (h *Handler) Release(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	project := auth.ProjectFrom(ctx)

	rel := h.resolveRelease(ctx, project, r.PathValue("version"))
	if rel == nil {
		http.NotFound(w, r)
		return
	}

	arts, err := h.DB.ListArtifacts(ctx, rel.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	h.render(w, r, "release", buildReleaseView(r, project, rel, arts))
}

// resolveRelease maps a URL version spec to a published release, or nil. It
// accepts "latest", an exact version, or a "v"-prefixed version. Unpublished
// (draft) releases are never exposed through the public frontend.
func (h *Handler) resolveRelease(ctx context.Context, project *db.Project, version string) *db.Release {
	if version == "" || version == "latest" {
		rel, err := h.DB.GetLatestRelease(ctx, project.ID)
		if err != nil {
			return nil
		}
		return rel
	}

	rel, err := h.DB.GetRelease(ctx, project.ID, version)
	if errors.Is(err, db.ErrNotFound) {
		if trimmed := strings.TrimPrefix(version, "v"); trimmed != version {
			rel, err = h.DB.GetRelease(ctx, project.ID, trimmed)
		}
	}
	if err != nil || rel == nil || !rel.Published {
		return nil
	}
	return rel
}

// Stylesheet serves the single same-origin stylesheet for the frontend.
func (h *Handler) Stylesheet(w http.ResponseWriter, r *http.Request) {
	if match := r.Header.Get("If-None-Match"); match == styleETag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("ETag", styleETag)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(styleCSS)
}

// render executes a page template into a buffer first so a template error
// surfaces as a clean 500 rather than a half-written 200.
func (h *Handler) render(w http.ResponseWriter, r *http.Request, name string, data any) {
	tmpl, ok := templates[name]
	if !ok {
		h.fail(w, r, fmt.Errorf("unknown template %q", name))
		return
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "base.html", data); err != nil {
		h.fail(w, r, err)
		return
	}

	// The global security middleware sets a default-src 'none' CSP; relax it
	// here just enough for our one same-origin stylesheet and inline SVG/data
	// favicon. No scripts are ever served, so script-src stays absent.
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; style-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'none'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(buf.Bytes())
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "web frontend error", "err", err, "path", r.URL.Path)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

// hasNonDockerArtifact reports whether the set contains a real binary that can
// be downloaded or repackaged (as opposed to a docker-image-only release).
func hasNonDockerArtifact(arts []db.Artifact) bool {
	for _, a := range arts {
		if !a.Kind.ServedViaDockerOnly() {
			return true
		}
	}
	return false
}
