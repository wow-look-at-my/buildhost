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
	mux                = router.New()
	hostRouters        = map[string]*router.Router{}
	hostMu             sync.RWMutex
	mw                 *Middleware
	readyFuncs         []func()
	sharedDB           *db.DB
	sharedStore        storage.Storage
	sharedBase         string
	sharedData         string
	serviceURLs        map[string]*url.URL
	sharedFetchDomains []string
)

func Router() *router.Router    { return mux }
func DB() *db.DB                { return sharedDB }
func Store() storage.Storage    { return sharedStore }
func BaseURL() string           { return sharedBase }
func DataDir() string           { return sharedData }
func GetMiddleware() *Middleware { return mw }
func SiteFetchDomains() []string { return sharedFetchDomains }

func ServiceURL(name string) *url.URL { return serviceURLs[name] }
func StaticURL() *url.URL             { return serviceURLs["static"] }
func DLBaseURL() *url.URL             { return serviceURLs["dl"] }

func OnReady(fn func()) {
	readyFuncs = append(readyFuncs, fn)
}

func Init(database *db.DB, store storage.Storage, baseURL, dataDir, domain string, svcOverrides map[string]string, trustedIssuers, allowedOrgs, allowedEvents, siteFetchDomains []string) {
	mux = router.New()
	hostMu.Lock()
	hostRouters = map[string]*router.Router{}
	hostMu.Unlock()

	sharedDB = database
	sharedStore = store
	sharedBase = baseURL
	sharedData = dataDir
	sharedFetchDomains = siteFetchDomains

	u, err := url.Parse(baseURL)
	if err != nil {
		panic("invalid BUILDHOST_BASE_URL: " + err.Error())
	}
	scheme := u.Scheme

	serviceURLs = make(map[string]*url.URL)
	for _, svc := range []string{"apt", "brew", "dl", "npm", "oci", "docker", "sites", "static"} {
		if override, ok := svcOverrides[svc]; ok {
			parsed, err := url.Parse(override)
			if err != nil {
				panic("invalid BUILDHOST_" + strings.ToUpper(svc) + "_URL: " + err.Error())
			}
			serviceURLs[svc] = parsed
		} else if domain != "" {
			serviceURLs[svc] = &url.URL{Scheme: scheme, Host: svc + "." + domain}
		}
	}

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

func hostRouter(host string) *router.Router {
	hostMu.RLock()
	r, ok := hostRouters[host]
	hostMu.RUnlock()
	if ok {
		return r
	}
	hostMu.Lock()
	defer hostMu.Unlock()
	if r, ok = hostRouters[host]; ok {
		return r
	}
	r = router.New()
	hostRouters[host] = r
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

func serviceHandle(host, pattern string, parse ParseFunc, handler http.HandlerFunc) {
	hostRouter(host).HandleFunc(pattern, router.Allow, requireProjectFunc(parse, handler))
}

func serviceHandleRaw(host, pattern string, handler http.HandlerFunc) {
	hostRouter(host).HandleFunc(pattern, router.Allow, handler)
}

func serviceHandleHandler(host, pattern string, parse ParseFunc, handler http.Handler) {
	hostRouter(host).Handle(pattern, router.Allow, requireProject(parse)(handler))
}

func ServiceRoute(subdomain, pattern string) string {
	svcURL := serviceURLs[subdomain]
	return svcURL.Host + "\x00" + pattern
}

func ServiceHandle(subdomain, pattern string, parse ParseFunc, handler http.HandlerFunc) {
	svcURL := serviceURLs[subdomain]
	serviceHandle(svcURL.Host, svcURL.Path+pattern, parse, handler)
}

func ServiceHandleRaw(subdomain, pattern string, handler http.HandlerFunc) {
	svcURL := serviceURLs[subdomain]
	serviceHandleRaw(svcURL.Host, svcURL.Path+pattern, handler)
}

func ServiceHandleHandler(subdomain, pattern string, parse ParseFunc, handler http.Handler) {
	svcURL := serviceURLs[subdomain]
	serviceHandleHandler(svcURL.Host, svcURL.Path+pattern, parse, handler)
}

func ServiceRedirect(from, to string, permanent bool) {
	fromURL := serviceURLs[from]
	toURL := serviceURLs[to]
	if fromURL == nil || toURL == nil {
		return
	}
	code := http.StatusFound
	if permanent {
		code = http.StatusMovedPermanently
	}
	hostRouter(fromURL.Host).HandleFunc("/{path...}", router.Allow, func(w http.ResponseWriter, r *http.Request) {
		target := &url.URL{
			Scheme:   toURL.Scheme,
			Host:     toURL.Host,
			Path:     toURL.Path + r.URL.Path,
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
	hostMu.RLock()
	hr, ok := hostRouters[host]
	hostMu.RUnlock()
	if ok {
		hr.ServeHTTP(w, r)
		return
	}
	mux.ServeHTTP(w, r)
}

func AllRoutes() []router.Route {
	var all []router.Route
	all = append(all, mux.Routes()...)
	hostMu.RLock()
	defer hostMu.RUnlock()
	for host, r := range hostRouters {
		for _, route := range r.Routes() {
			route.Pattern = host + route.Pattern
			all = append(all, route)
		}
	}
	return all
}
