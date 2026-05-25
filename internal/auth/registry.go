package auth

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

var (
	mux            = http.NewServeMux()
	mw             *Middleware
	readyFuncs     []func()
	sharedDB       *db.DB
	sharedStore    storage.Storage
	sharedBase     string
	sharedData     string
	sharedDomain   string
	sharedScheme   string
	sharedStaticURL *url.URL
	sharedDLBaseURL *url.URL
)

func Mux() *http.ServeMux        { return mux }
func DB() *db.DB                 { return sharedDB }
func Store() storage.Storage     { return sharedStore }
func BaseURL() string            { return sharedBase }
func DataDir() string            { return sharedData }
func Domain() string             { return sharedDomain }
func StaticURL() *url.URL        { return sharedStaticURL }
func DLBaseURL() *url.URL        { return sharedDLBaseURL }
func GetMiddleware() *Middleware  { return mw }

func OnReady(fn func()) {
	readyFuncs = append(readyFuncs, fn)
}

func Init(database *db.DB, store storage.Storage, baseURL, dataDir, domain string, trustedIssuers, allowedOrgs, allowedEvents []string) {
	sharedDB = database
	sharedStore = store
	sharedBase = baseURL
	sharedData = dataDir
	sharedDomain = domain

	u, _ := url.Parse(baseURL)
	sharedScheme = u.Scheme
	sharedStaticURL = &url.URL{Scheme: sharedScheme, Host: "static." + domain}
	sharedDLBaseURL = &url.URL{Scheme: sharedScheme, Host: "dl." + domain}

	mw = &Middleware{DB: database, Verifier: NewOIDCVerifier(OIDCConfig{
		BaseURL:        baseURL,
		TrustedIssuers: trustedIssuers,
		AllowedOrgs:    allowedOrgs,
		AllowedEvents:  allowedEvents,
	})}
	for _, fn := range readyFuncs {
		fn()
	}
}

func Handle(pattern string, parse ParseFunc, handler http.HandlerFunc) {
	mux.HandleFunc(pattern, requireProjectFunc(parse, handler))
}

func HandleRaw(pattern string, handler http.HandlerFunc) {
	mux.HandleFunc(pattern, handler)
}

func HandleHandler(pattern string, parse ParseFunc, handler http.Handler) {
	mux.Handle(pattern, requireProject(parse)(handler))
}

func ServiceRoute(subdomain, pattern string) string {
	host := subdomain + "." + sharedDomain
	if method, path, ok := strings.Cut(pattern, " "); ok {
		return method + " " + host + path
	}
	return host + pattern
}

func ServiceRedirect(from, to string, permanent bool) {
	code := http.StatusFound
	if permanent {
		code = http.StatusMovedPermanently
	}
	fromHost := from + "." + sharedDomain
	toHost := to + "." + sharedDomain
	mux.HandleFunc(fromHost+"/", func(w http.ResponseWriter, r *http.Request) {
		target := &url.URL{
			Scheme:   sharedScheme,
			Host:     toHost,
			Path:     r.URL.Path,
			RawQuery: r.URL.RawQuery,
		}
		http.Redirect(w, r, target.String(), code)
	})
}
