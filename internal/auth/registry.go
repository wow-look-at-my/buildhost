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
	sharedBase     string
	sharedData     string
	sharedFetchDomains []string
)

func Router() *router.Router     { return mux }
func DB() *db.DB                 { return sharedDB }
func Store() storage.Storage     { return sharedStore }
func BaseURL() string            { return sharedBase }
func DataDir() string            { return sharedData }
func GetMiddleware() *Middleware { return mw }
func SiteFetchDomains() []string { return sharedFetchDomains }

func OnReady(fn func()) {
	readyFuncs = append(readyFuncs, fn)
}

func Init(database *db.DB, store storage.Storage, baseURL, dataDir string, trustedIssuers, allowedOrgs, allowedEvents, siteFetchDomains []string) {
	mux = router.New()
	serviceMu.Lock()
	serviceRouters = map[string]*router.Router{}
	serviceMu.Unlock()

	sharedDB = database
	sharedStore = store
	sharedBase = baseURL
	sharedData = dataDir
	sharedFetchDomains = siteFetchDomains

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
	return r
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
		domain := domainFromRequest(r)
		scheme := "https"
		if strings.HasPrefix(sharedBase, "http://") {
			scheme = "http"
		}
		target := &url.URL{
			Scheme:   scheme,
			Host:     to + "." + domain,
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
	domain := domainFromRequest(r)
	scheme := "https"
	if strings.HasPrefix(sharedBase, "http://") {
		scheme = "http"
	}
	return &url.URL{Scheme: scheme, Host: service + "." + domain}
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
