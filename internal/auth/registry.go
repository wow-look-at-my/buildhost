package auth

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
	"github.com/wow-look-at-my/router"
)

var (
	mux                       = router.New()
	mw                        *Middleware
	readyFuncs                []func()
	sharedDB                  *db.DB
	sharedStore               storage.Storage
	sharedData                string
	sharedFetchDomains        []string
	sharedGitHubWebhookSecret string
)

func Router() *router.Router      { return mux }
func DB() *db.DB                  { return sharedDB }
func Store() storage.Storage      { return sharedStore }
func DataDir() string             { return sharedData }
func GetMiddleware() *Middleware  { return mw }
func SiteFetchDomains() []string  { return sharedFetchDomains }
func GitHubWebhookSecret() string { return sharedGitHubWebhookSecret }

func OnReady(fn func()) {
	readyFuncs = append(readyFuncs, fn)
}

func Init(database *db.DB, store storage.Storage, dataDir string, trustedIssuers, allowedOrgs, allowedEvents, siteFetchDomains []string, githubWebhookSecret string) {
	sharedDB = database
	sharedStore = store
	sharedData = dataDir
	sharedFetchDomains = siteFetchDomains
	sharedGitHubWebhookSecret = githubWebhookSecret

	mw = &Middleware{DB: database, Verifier: NewOIDCVerifier(OIDCConfig{
		TrustedIssuers: trustedIssuers,
		AllowedOrgs:    allowedOrgs,
		AllowedEvents:  allowedEvents,
	})}
	for _, fn := range readyFuncs {
		fn()
	}
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

// servicePattern turns a path-only service pattern into a host+path pattern
// anchored to the service's subdomain, e.g. ("apt", "GET /{path...}") becomes
// "GET apt.{domain}/{path...}". The router matches the host's first label
// against the subdomain and binds {domain} to the rest of the request Host, so
// the registered pattern is exactly what is matched -- no host dispatch table.
func servicePattern(subdomain, pattern string) string {
	method := ""
	rest := pattern
	if i := strings.IndexByte(pattern, ' '); i >= 0 {
		method = pattern[:i+1] // keep the trailing space
		rest = pattern[i+1:]
	}
	return method + subdomain + ".{domain}" + rest
}

func ServiceHandle(subdomain, pattern string, parse ParseFunc, handler http.HandlerFunc) {
	mux.HandleFunc(servicePattern(subdomain, pattern), router.Allow, requireProjectFunc(parse, handler))
}

func ServiceHandleRaw(subdomain, pattern string, handler http.HandlerFunc) {
	mux.HandleFunc(servicePattern(subdomain, pattern), router.Allow, handler)
}

func ServiceHandleHandler(subdomain, pattern string, parse ParseFunc, handler http.Handler) {
	mux.Handle(servicePattern(subdomain, pattern), router.Allow, requireProject(parse)(handler))
}

func ServiceRedirect(from, to string, permanent bool) {
	code := http.StatusFound
	if permanent {
		code = http.StatusMovedPermanently
	}
	mux.HandleFunc(servicePattern(from, "/{path...}"), router.Allow, func(w http.ResponseWriter, r *http.Request) {
		target := &url.URL{
			Scheme:   RequestScheme(r),
			Host:     to + "." + domainFromRequest(r),
			Path:     r.URL.Path,
			RawQuery: r.URL.RawQuery,
		}
		http.Redirect(w, r, target.String(), code)
	})
}

// ServeHTTP dispatches every request through the single router. Service
// subdomains are matched by the host portion of their registered patterns;
// unknown hosts fall through to the host-agnostic (main-domain) routes.
func ServeHTTP(w http.ResponseWriter, r *http.Request) {
	mux.ServeHTTP(w, r)
}

func domainFromRequest(r *http.Request) string {
	host := r.Host
	port := ""
	if i := strings.LastIndex(host, ":"); i >= 0 {
		port = host[i:]
		host = host[:i]
	}
	if dot := strings.IndexByte(host, '.'); dot > 0 {
		host = host[dot+1:]
	}
	return host + port
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

// RequestRootURL returns the root domain URL (scheme + bare domain, no service
// subdomain). Use this when building cross-service URLs from within a handler
// that itself runs on a service subdomain (e.g. brew.example.com → https://example.com).
func RequestRootURL(r *http.Request) string {
	return RequestScheme(r) + "://" + domainFromRequest(r)
}

func hostNoPort(host string) string {
	if i := strings.LastIndex(host, ":"); i >= 0 {
		return host[:i]
	}
	return host
}

// AllRoutes returns every registered route exactly as registered. Service
// routes carry their subdomain and {domain} host token in the real pattern
// (e.g. "apt.{domain}/{path...}"), so nothing is synthesized here.
func AllRoutes() []router.Route {
	return mux.Routes()
}
