package static

import (
	"net/http"
	"net/url"
	"sync"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
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
}

func For(project string) Params              { return Params{Project: project} }
func (p Params) WithVersion(v string) Params { p.Version = v; return p }
func (p Params) WithOS(os db.OS) Params      { p.OS = os; return p }
func (p Params) WithArch(a db.Arch) Params   { p.Arch = a; return p }
func (p Params) WithFmt(f string) Params     { p.Fmt = f; return p }
func (p Params) WithDebug(d bool) Params     { p.Debug = d; return p }

func URL(staticBase *url.URL, p Params) string {
	u := *staticBase
	u.Path = "/file"
	u.RawQuery = p.values().Encode()
	return u.String()
}

func Redirect(w http.ResponseWriter, r *http.Request, staticBase *url.URL, p Params, code int) {
	http.Redirect(w, r, URL(staticBase, p), code)
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
	return q
}

var knownParams = map[string]bool{
	"arch": true, "debug": true, "fmt": true,
	"project": true, "os": true, "v": true,
}

func canonicalQuery(raw url.Values) string {
	clean := url.Values{}
	for k, vs := range raw {
		if knownParams[k] && len(vs) > 0 {
			clean.Set(k, vs[0])
		}
	}
	return clean.Encode()
}
