package static

import (
	"net/http"
	"net/url"
	"sync"

	"github.com/wow-look-at-my/buildhost/internal/model"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

type ServeContext struct {
	Project  model.Project
	Release  model.Release
	Artifact model.Artifact
	Store    storage.Storage
	BaseURL  string
	TmpDir   string
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
	ID      string
	Version string
	OS      model.OS
	Arch    model.Arch
	Fmt     string
	Debug   bool
}

func For(id string) Params                        { return Params{ID: id} }
func (p Params) WithVersion(v string) Params      { p.Version = v; return p }
func (p Params) WithOS(os model.OS) Params        { p.OS = os; return p }
func (p Params) WithArch(a model.Arch) Params     { p.Arch = a; return p }
func (p Params) WithFmt(f string) Params          { p.Fmt = f; return p }
func (p Params) WithDebug(d bool) Params          { p.Debug = d; return p }

const RedirectCode = http.StatusFound

func URL(baseURL string, p Params) string {
	return baseURL + "/static?" + p.query()
}

func Redirect(w http.ResponseWriter, r *http.Request, baseURL string, p Params) {
	http.Redirect(w, r, URL(baseURL, p), RedirectCode)
}

func (p Params) query() string {
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
	if p.ID != "" {
		q.Set("id", p.ID)
	}
	if p.OS != "" {
		q.Set("os", string(p.OS))
	}
	if p.Version != "" {
		q.Set("v", p.Version)
	}
	return q.Encode()
}

var knownParams = map[string]bool{
	"arch": true, "debug": true, "fmt": true,
	"id": true, "os": true, "v": true,
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
