package auth

import (
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
	"github.com/wow-look-at-my/router"
)

var (
	mux            = router.New()
	serviceRouters = map[string]*router.Router{}
	serviceMu      sync.RWMutex
	mw             *Middleware
	readyFuncs     []func()
	sharedDB       *db.DB
	sharedStore    storage.Storage
	sharedData     string
	sharedFetchDomains []string
)

func Router() *router.Router     { return mux }
func DB() *db.DB                 { return sharedDB }
func Store() storage.Storage     { return sharedStore }
func DataDir() string            { return sharedData }
func GetMiddleware() *Middleware { return mw }
func SiteFetchDomains() []string { return sharedFetchDomains }

func OnReady(fn func()) {
	readyFuncs = append(readyFuncs, fn)
}

func Init(database *db.DB, store storage.Storage, dataDir string, trustedIssuers, allowedOrgs, allowedEvents, siteFetchDomains []string) {
	sharedDB = database
	sharedStore = store
	sharedData = dataDir
	sharedFetchDomains = siteFetchDomains

	mw = &Middleware{DB: database, Verifier: NewOIDCVerifier(OIDCConfig{
		TrustedIssuers: trustedIssuers,
		AllowedOrgs:    allowedOrgs,
		AllowedEvents:  allowedEvents,
	})}
	for _, fn := range readyFuncs {
		fn()
	}
}

func svcRouter(name string) *router.Router {
	serviceMu.RLock()
	r, ok := serviceRouters[name]
	serviceMu.RUnlock()
	if ok {
		return r
	}
	serviceMu.Lock()
	defer serviceMu.Unlock()
	if r, ok = serviceRouters[name]; ok {
		return r
	}
	r = router.New()
	serviceRouters[name] = r
	// Every service that answers on <name>.{domain} also gets a main-domain
	// path redirect ({domain}/<name>/... -> <name>.{domain}/...), so the
	// path-style URLs documented in /llms.txt resolve for callers that don't
	// address the subdomain directly. Registered once, when the service router
	// is first created.
	registerServicePathRedirect(name)
	return r
}

// registerServicePathRedirect bounces {domain}/<service> and
// {domain}/<service>/... to the <service>.{domain} subdomain that serves it.
// It preserves the raw escaped path (so a scoped npm package's %2f survives the
// hop) and the query string, and targets <service>.<full host> rather than
// DeriveServiceURL -- the latter strips the first label, which is right only
// when the request already arrived on a service subdomain, not the main domain.
func registerServicePathRedirect(service string) {
	redirect := func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.EscapedPath(), "/"+service)
		loc := strings.Replace(RequestBaseURL(r), "://", "://"+service+".", 1) + rest
		if r.URL.RawQuery != "" {
			loc += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, loc, http.StatusMovedPermanently)
	}
	HandleRaw("/"+service, redirect)
	HandleRaw("/"+service+"/{path...}", redirect)
}

func Handle(pattern string, parse ParseFunc, handler http.HandlerFunc) {
	mux.HandleFunc(pattern, router.Allow, requireProjectFunc(parse, handler))
}

func HandleRaw(pattern string, handler http.HandlerFunc) {
	mux.HandleFunc(pattern, router.Allow, handler)
}

func HandleHandler(pattern string, parse ParseFunc, handler http.Handler) {
	mux.Handle(pattern, router.Allow, requireProject(parse)(handler))
}

func ServiceHandle(subdomain, pattern string, parse ParseFunc, handler http.HandlerFunc) {
	svcRouter(subdomain).HandleFunc(pattern, router.Allow, requireProjectFunc(parse, handler))
}

func ServiceHandleRaw(subdomain, pattern string, handler http.HandlerFunc) {
	svcRouter(subdomain).HandleFunc(pattern, router.Allow, handler)
}

func ServiceHandleHandler(subdomain, pattern string, parse ParseFunc, handler http.Handler) {
	svcRouter(subdomain).Handle(pattern, router.Allow, requireProject(parse)(handler))
}

func ServiceRedirect(from, to string, permanent bool) {
	code := http.StatusFound
	if permanent {
		code = http.StatusMovedPermanently
	}
	svcRouter(from).HandleFunc("/{path...}", router.Allow, func(w http.ResponseWriter, r *http.Request) {
		target := &url.URL{
			Scheme:   RequestScheme(r),
			Host:     to + "." + domainFromRequest(r),
			Path:     r.URL.Path,
			RawQuery: r.URL.RawQuery,
		}
		http.Redirect(w, r, target.String(), code)
	})
}

func ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	if dot := strings.IndexByte(host, '.'); dot > 0 {
		subdomain := host[:dot]
		serviceMu.RLock()
		sr, ok := serviceRouters[subdomain]
		serviceMu.RUnlock()
		if ok {
			sr.ServeHTTP(w, r)
			return
		}
	}
	mux.ServeHTTP(w, r)
}

func domainFromRequest(r *http.Request) string {
	host := r.Host
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	if dot := strings.IndexByte(host, '.'); dot > 0 {
		return host[dot+1:]
	}
	return host
}

func DeriveServiceURL(r *http.Request, service string) *url.URL {
	return &url.URL{Scheme: RequestScheme(r), Host: service + "." + domainFromRequest(r)}
}

// RequestScheme returns the scheme the client used to reach this server. We run
// behind a TLS-terminating Cloudflare Tunnel (and an internal nginx sidecar that
// rewrites X-Forwarded-Proto), so rather than trust a forwarded header we treat
// loopback hosts as http and everything else as https.
func RequestScheme(r *http.Request) string {
	host := hostNoPort(r.Host)
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || host == "127.0.0.1" || host == "::1" {
		return "http"
	}
	return "https"
}

// RequestBaseURL reconstructs this server's own base URL from the request
// (scheme + Host), so nothing depends on a configured "this is my URL" value.
func RequestBaseURL(r *http.Request) string {
	return RequestScheme(r) + "://" + r.Host
}

func hostNoPort(host string) string {
	if i := strings.LastIndex(host, ":"); i >= 0 {
		return host[:i]
	}
	return host
}

func AllRoutes() []router.Route {
	var all []router.Route
	all = append(all, mux.Routes()...)
	serviceMu.RLock()
	defer serviceMu.RUnlock()
	for name, r := range serviceRouters {
		for _, route := range r.Routes() {
			route.Pattern = name + ".*/" + strings.TrimPrefix(route.Pattern, "/")
			all = append(all, route)
		}
	}
	return all
}
