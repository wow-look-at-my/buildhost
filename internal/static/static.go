package static

import (
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
	"github.com/wow-look-at-my/go-containers/set"
)

type ServeContext struct {
	Project   db.Project
	Release   db.Release
	Artifact  db.Artifact
	Store     storage.Storage
	StaticURL *url.URL
	BaseURL   string
	TmpDir    string
}

type Fmt interface {
	Name() string
	Serve(w http.ResponseWriter, r *http.Request, ctx ServeContext) error
}

var (
	fmtMu       sync.RWMutex
	fmtRegistry = map[string]Fmt{}
)

func RegisterFmt(f Fmt) {
	fmtMu.Lock()
	defer fmtMu.Unlock()
	fmtRegistry[f.Name()] = f
}

func LookupFmt(name string) (Fmt, bool) {
	fmtMu.RLock()
	defer fmtMu.RUnlock()
	f, ok := fmtRegistry[name]
	return f, ok
}

type Params struct {
	Project string
	Version string
	OS      db.OS
	Arch    db.Arch
	Fmt     string
	Debug   bool
	// Token, when set, is a signed temporary download token (see
	Token string
}

func For(project string) Params              { return Params{Project: project} }
func (p Params) WithVersion(v string) Params { p.Version = v; return p }
func (p Params) WithOS(os db.OS) Params      { p.OS = os; return p }
func (p Params) WithArch(a db.Arch) Params   { p.Arch = a; return p }
func (p Params) WithFmt(f string) Params     { p.Fmt = f; return p }
func (p Params) WithDebug(d bool) Params     { p.Debug = d; return p }
func (p Params) WithToken(t string) Params   { p.Token = t; return p }

func URL(staticBase *url.URL, p Params) string {
	u := *staticBase
	u.Path = "/file"
	u.RawQuery = p.values().Encode()
	return u.String()
}

func Redirect(w http.ResponseWriter, r *http.Request, staticBase *url.URL, p Params, code int) {
	http.Redirect(w, r, URL(staticBase, p), code)
}

// SignedURL builds a static download URL carrying a signed, expiring &token= that
// authorizes exactly this artifact (project, version, os, arch, fmt, debug) until
func SignedURL(staticBase *url.URL, p Params, exp time.Time) (string, string) {
	tok := auth.MintDownloadToken(p.Project, p.Version, string(p.OS), string(p.Arch), p.Fmt, p.Debug, exp)
	p.Token = tok
	return URL(staticBase, p), tok
}

func (p Params) values() url.Values {
	q := url.Values{}
	if p.Arch != "" {
		q.Set("arch", string(p.Arch))
	}
	if p.Debug {
		q.Set("debug", "1")
	}
	if p.Fmt != "" {
		q.Set("fmt", p.Fmt)
	}
	if p.Project != "" {
		q.Set("project", p.Project)
	}
	if p.OS != "" {
		q.Set("os", string(p.OS))
	}
	if p.Version != "" {
		q.Set("v", p.Version)
	}
	if p.Token != "" {
		q.Set("token", p.Token)
	}
	return q
}

var knownParams = set.Of(
	"arch", "debug", "fmt",
	"project", "os", "v", "token",
)

func canonicalQuery(raw url.Values) string {
	clean := url.Values{}
	for k, vs := range raw {
		if !knownParams.Contains(k) || len(vs) == 0 {
			continue
		}
		v := vs[0]
		// Fold platform-name aliases (e.g. RUNNER_OS "Linux", RUNNER_ARCH "X64",
		switch k {
		case "os":
			if c, ok := db.NormalizeOS(v); ok {
				v = string(c)
			}
		case "arch":
			if c, ok := db.NormalizeArch(v); ok {
				v = string(c)
			}
		}
		clean.Set(k, v)
	}
	// The deprecated GOOS/GOARCH-ordered wasm pair (os=js|wasip1, arch=wasm)
	// folds to the canonical os=wasm form. Pair-level, so it runs after the
	if o, a, ok := db.NormalizeLegacyWasmPair(clean.Get("os"), clean.Get("arch")); ok {
		clean.Set("os", string(o))
		clean.Set("arch", string(a))
	}
	return clean.Encode()
}
