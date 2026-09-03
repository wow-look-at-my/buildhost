package goproxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"golang.org/x/mod/module"
)

// registerRoutes runs at package init, before the Service exists, so each
// handler resolves it per request via withService.
func registerRoutes() {
	// The module path is the whole prefix of the request path, so the protocol
	serve := withService((*Service).serve)
	auth.ServiceHandleRaw(Subdomain, "GET /{path...}", serve)
	auth.ServiceHandleRaw(Subdomain, "HEAD /{path...}", serve)
	// A literal path outranks the catch-all, and no module request can collide
	auth.ServiceHandleRaw(Subdomain, "GET /health", withService((*Service).serveHealth))
}

// withService adapts a Service method into a handler that resolves the running
// Service per request. Before auth.Init there is none: the routes exist (so the
func withService(fn func(*Service, http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := Current()
		if s == nil {
			http.Error(w, "buildhost goproxy: not initialized\n", http.StatusServiceUnavailable)
			return
		}
		fn(s, w, r)
	}
}

// request is a parsed module-proxy request.
type request struct {
	Module   string
	Version  string
	Endpoint string // "list", "info", "mod", "zip", "latest"
}

// parseRequest splits a module-proxy path into module, version and endpoint.
// The module path is case-encoded on the wire (an uppercase letter travels as
// "!" + its lowercase form) and is decoded here.
func parseRequest(p string) (request, error) {
	p = strings.TrimPrefix(p, "/")

	if escaped, ok := strings.CutSuffix(p, "/@latest"); ok {
		mod, err := module.UnescapePath(escaped)
		if err != nil {
			return request{}, invalidErr(escaped, "", "module path is not correctly case-encoded: "+err.Error())
		}
		return request{Module: mod, Endpoint: "latest"}, nil
	}

	escaped, rest, ok := strings.Cut(p, "/@v/")
	if !ok {
		return request{}, invalidErr(p, "", "not a module proxy path (expected .../@v/... or .../@latest)")
	}
	mod, err := module.UnescapePath(escaped)
	if err != nil {
		return request{}, invalidErr(escaped, "", "module path is not correctly case-encoded: "+err.Error())
	}
	if rest == "list" {
		return request{Module: mod, Endpoint: "list"}, nil
	}

	var suffix string
	switch {
	case strings.HasSuffix(rest, ".info"):
		suffix = ".info"
	case strings.HasSuffix(rest, ".mod"):
		suffix = ".mod"
	case strings.HasSuffix(rest, ".zip"):
		suffix = ".zip"
	default:
		return request{}, invalidErr(mod, "", "unknown module proxy endpoint "+rest)
	}
	rawVer := strings.TrimSuffix(rest, suffix)
	ver, err := unescapeVersion(rawVer)
	if err != nil {
		return request{}, invalidErr(mod, rawVer, "version is not correctly case-encoded: "+err.Error())
	}
	return request{Module: mod, Version: ver, Endpoint: strings.TrimPrefix(suffix, ".")}, nil
}

func (s *Service) serve(w http.ResponseWriter, r *http.Request) {
	started := time.Now()

	req, err := parseRequest(r.URL.EscapedPath())
	if err != nil {
		s.fail(w, r, request{}, err, started)
		return
	}

	if !s.accessible(r, req.Module) {
		s.fail(w, r, req, inaccessibleErr(req.Module, req.Version), started)
		return
	}

	switch req.Endpoint {
	case "list":
		s.serveList(w, r, req, started)
	case "latest":
		s.serveLatest(w, r, req, started)
	case "info", "mod", "zip":
		s.serveVersioned(w, r, req, started)
	default:
		s.fail(w, r, req, invalidErr(req.Module, req.Version, "unsupported endpoint"), started)
	}
}

// operatorView reports whether this caller may see the proxy's own
// configuration -- the private prefixes, the credential state, the readiness
// module. Each of those names private repositories, so it takes the same
func (s *Service) operatorView(r *http.Request) bool {
	if t := auth.TokenFrom(r.Context()); t != nil && t.ProjectID == nil &&
		(t.HasScope("read") || t.HasScope("write")) {
		return true
	}
	_, ok := auth.UserFrom(r.Context())
	return ok
}

// accessible reports whether this caller may see this module.
//
// A module outside the private namespaces is public source: it needs no
func (s *Service) accessible(r *http.Request, modPath string) bool {
	if !s.isPrivate(modPath) {
		return true
	}
	if t := auth.TokenFrom(r.Context()); t != nil && t.ProjectID == nil &&
		(t.HasScope("read") || t.HasScope("write")) {
		return true
	}
	refs, err := parseModulePath(modPath)
	if err != nil {
		return false
	}
	for _, ref := range refs {
		if auth.UserCanReadRepo(r.Context(), ref.Owner+"/"+ref.Repo) {
			return true
		}
	}
	return false
}

func (s *Service) serveList(w http.ResponseWriter, r *http.Request, req request, started time.Time) {
	if !s.isPrivate(req.Module) {
		body, err := s.upstream.getBytes(r.Context(), req.Module, "", "@v/list")
		if err != nil {
			s.fail(w, r, req, err, started)
			return
		}
		s.ok(w, r, req, "upstream", "fetch", started, "text/plain; charset=UTF-8", body)
		return
	}

	versions, err := s.listVersions(r.Context(), req.Module)
	if err != nil {
		s.fail(w, r, req, err, started)
		return
	}
	s.ok(w, r, req, "github", "fetch", started, "text/plain; charset=UTF-8",
		[]byte(strings.Join(versions, "\n")+"\n"))
}

func (s *Service) serveLatest(w http.ResponseWriter, r *http.Request, req request, started time.Time) {
	if !s.isPrivate(req.Module) {
		body, err := s.upstream.getBytes(r.Context(), req.Module, "", "@latest")
		if err != nil {
			s.fail(w, r, req, err, started)
			return
		}
		s.ok(w, r, req, "upstream", "fetch", started, "application/json", body)
		return
	}

	res, err := s.latest(r.Context(), req.Module)
	if err != nil {
		s.fail(w, r, req, err, started)
		return
	}
	body, err := json.Marshal(versionInfo{Version: res.Version, Time: res.Time})
	if err != nil {
		s.fail(w, r, req, upstreamErr(req.Module, "", "", 0, "encoding @latest", err), started)
		return
	}
	// Cache the resolution so the .info/.mod that follows is a hit.
	if id, idErr := s.db.GoproxyModuleID(r.Context(), req.Module, "github"); idErr == nil {
		_ = s.db.PutGoproxyCached(r.Context(), id, cachedFromResolved(res))
	}
	s.ok(w, r, req, "github", "fetch", started, "application/json", body)
}

func (s *Service) serveVersioned(w http.ResponseWriter, r *http.Request, req request, started time.Time) {
	needZip := req.Endpoint == "zip"
	c, hit, err := s.cachedOrFetch(r.Context(), req.Module, req.Version, needZip)
	if err != nil {
		s.fail(w, r, req, err, started)
		return
	}
	outcome := "fetch"
	if hit {
		outcome = "hit"
	}
	source := s.sourceName(req.Module)

	switch req.Endpoint {
	case "info":
		body, err := json.Marshal(versionInfo{Version: c.Version, Time: c.CommittedAt})
		if err != nil {
			s.fail(w, r, req, upstreamErr(req.Module, req.Version, "", 0, "encoding .info", err), started)
			return
		}
		s.ok(w, r, req, source, outcome, started, "application/json", body)
	case "mod":
		s.ok(w, r, req, source, outcome, started, "text/plain; charset=UTF-8", c.GoMod)
	case "zip":
		s.serveZip(w, r, req, c.ZipKey, c.ZipSize, source, outcome, started)
	}
}

func (s *Service) serveZip(w http.ResponseWriter, r *http.Request, req request, key string, size int64, source, outcome string, started time.Time) {
	if key == "" {
		s.fail(w, r, req, upstreamErr(req.Module, req.Version, source, 0,
			"the module zip was not stored", nil), started)
		return
	}
	rc, storedSize, err := s.openZip(r.Context(), key)
	if err != nil {
		s.fail(w, r, req, upstreamErr(req.Module, req.Version, "storage", 0,
			"reading the cached module zip", err), started)
		return
	}
	defer rc.Close()
	if storedSize > 0 {
		size = storedSize
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Length", fmt.Sprint(size))
	// A module version is immutable, so its zip may be cached forever.
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		s.record(req, source, outcome, http.StatusOK, "", started)
		return
	}
	n, _ := io.Copy(w, rc)
	s.metrics.addBytes(n)
	s.record(req, source, outcome, http.StatusOK, "", started)
}

func (s *Service) ok(w http.ResponseWriter, r *http.Request, req request, source, outcome string, started time.Time, contentType string, body []byte) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", fmt.Sprint(len(body)))
	if req.Endpoint == "info" || req.Endpoint == "mod" {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		if _, err := w.Write(body); err == nil {
			s.metrics.addBytes(int64(len(body)))
		}
	}
	s.record(req, source, outcome, http.StatusOK, "", started)
}

// fail answers a failed fetch. This is the single exit for every error, and the
func (s *Service) fail(w http.ResponseWriter, r *http.Request, req request, err error, started time.Time) {
	e := asError(req.Module, req.Version, err)
	logFailure(e)

	status := e.HTTPStatus()
	w.Header().Set("Content-Type", "text/plain; charset=UTF-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		// The body is the whole diagnosis: `go mod download` prints a proxy's
		_, _ = io.WriteString(w, e.Body())
	}
	s.record(req, e.Upstream, "error", status, e.Kind.String(), started)
}

func (s *Service) record(req request, source, outcome string, status int, detail string, started time.Time) {
	s.metrics.record(Event{
		At:       time.Now().UTC(),
		Module:   req.Module,
		Version:  req.Version,
		Endpoint: req.Endpoint,
		Source:   source,
		Outcome:  outcome,
		Status:   status,
		Detail:   detail,
		Duration: time.Since(started).Round(time.Millisecond).String(),
	})
}
