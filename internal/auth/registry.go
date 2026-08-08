package auth

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/config"
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
	sharedSiteDomain          string
	sharedPrimaryDomain       string
)

func Router() *router.Router      { return mux }
func DB() *db.DB                  { return sharedDB }
func Store() storage.Storage      { return sharedStore }
func DataDir() string             { return sharedData }
func GetMiddleware() *Middleware  { return mw }
func SiteFetchDomains() []string  { return sharedFetchDomains }
func GitHubWebhookSecret() string { return sharedGitHubWebhookSecret }

// SiteDomain is the optional dedicated domain for project static sites
// (BUILDHOST_SITE_DOMAIN); "" when the feature is off. When set, project sites
// are also served at {project}.<SiteDomain> and the SSO handoff routes exist.
func SiteDomain() string { return sharedSiteDomain }

// PrimaryDomain is the apex carrying the GitHub OAuth callback
// (BUILDHOST_PRIMARY_DOMAIN); "" when unset. Site-domain browser sign-ins
// redirect to https://<PrimaryDomain>/__signin. It may also be
// config.PrimaryDomainAny ("*"), the explicit opt-in to serving every Host --
// callers that need a real hostname to build a URL must use PinnedPrimaryDomain.
func PrimaryDomain() string { return sharedPrimaryDomain }

// PinnedPrimaryDomain is PrimaryDomain reduced to an actually-addressable host,
// or "" when no single host is canonical. It collapses the two "not pinned to
// one apex" cases -- unset, and the config.PrimaryDomainAny ("*") wildcard --
// so URL builders cannot emit a literal "https://*/...". Use this anywhere the
// value becomes a hostname; use PrimaryDomain only to inspect the raw setting.
func PinnedPrimaryDomain() string {
	if sharedPrimaryDomain == config.PrimaryDomainAny {
		return ""
	}
	return sharedPrimaryDomain
}

func OnReady(fn func()) {
	readyFuncs = append(readyFuncs, fn)
}

func Init(database *db.DB, store storage.Storage, dataDir string, trustedIssuers, allowedOrgs, allowedEvents, siteFetchDomains []string, githubWebhookSecret, githubClientID, githubClientSecret, siteDomain, primaryDomain string) {
	sharedDB = database
	sharedStore = store
	sharedData = dataDir
	sharedFetchDomains = siteFetchDomains
	sharedGitHubWebhookSecret = githubWebhookSecret
	sharedSiteDomain = strings.ToLower(strings.Trim(siteDomain, " ."))
	sharedPrimaryDomain = strings.ToLower(strings.Trim(primaryDomain, " ."))

	initDownloadSecret(dataDir)
	// Config-conditional: registers the /__sso handoff routes only when a site
	// domain is configured, so an unset BUILDHOST_SITE_DOMAIN leaves the route
	// table byte-identical. Must run before the OnReady funcs below only for
	// tidiness -- route precedence is score-based, not registration-order-based.
	registerSSOHandoffRoutes()

	mw = &Middleware{
		DB: database,
		Verifier: NewOIDCVerifier(OIDCConfig{
			TrustedIssuers: trustedIssuers,
			AllowedOrgs:    allowedOrgs,
			AllowedEvents:  allowedEvents,
		}),
		GitHub: NewGitHubAuth(githubClientID, githubClientSecret),
	}
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

// HandlePrimary and HandleRawPrimary register main-domain routes that belong to
// the registry's own UI/API surface (the web frontend and /api/v1). When
// BUILDHOST_PRIMARY_DOMAIN is configured they answer ONLY on that apex: on any
// other unclaimed host (a stray CNAME, the bare site apex, ...) they serve the
// router's canonical not-found response, so an unknown domain pointed at
// buildhost is indistinguishable from a host with no such route. With no
// primary domain configured they are byte-identical to Handle/HandleRaw (fully
// host-agnostic, the historical behavior), so existing deployments and tests
// are unchanged.
//
// Deliberately NOT primary-scoped (must answer on any unclaimed host):
// /healthz and /ready-to-update (container-internal probes address the server
// by container DNS/localhost), /llms.txt, the sign-in flow (/__signin,
// /__signin/callback, /__signout), /__sso (bare-site-apex redemption), and the
// /npm/* -> npm.{domain} convenience redirect. Service-subdomain routes
// (ServiceHandle*) and {project}.<site-domain> routes have host-bearing
// patterns and are never affected.
func HandlePrimary(pattern string, parse ParseFunc, handler http.HandlerFunc) {
	mux.HandleFunc(pattern, router.Allow, primaryOnly(requireProjectFunc(parse, handler)))
}

func HandleRawPrimary(pattern string, handler http.HandlerFunc) {
	mux.HandleFunc(pattern, router.Allow, primaryOnly(handler))
}

// primaryOnly gates a handler to the configured primary apex (exact host match,
// port stripped, case-folded; PrimaryDomain() is stored lowercased). The gate
// runs BEFORE requireProject on purpose: a request on a foreign host must
// produce exactly the router's not-found response -- http.NotFound, the same
// call the router makes for an unregistered path -- with no auth semantics
// (401s, sign-in redirects, OIDC auto-provisioning) that would reveal the
// route exists. It reads PrimaryDomain() per request, not at registration
// time, because web routes register in init() before config is known.
//
// config.PrimaryDomainAny ("*") is the operator's explicit opt-in to serving
// every Host, so it passes everything through -- byte-identical to the
// historical unpinned behavior. An empty PrimaryDomain cannot reach here in a
// real server (config.Validate rejects it at startup); it is still treated as
// pass-through so that tests and library callers constructing an auth registry
// directly keep the permissive default rather than silently 404ing everything.
func primaryOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pd := PrimaryDomain()
		pinned := pd != "" && pd != config.PrimaryDomainAny
		if pinned && strings.ToLower(hostNoPort(r.Host)) != pd {
			http.NotFound(w, r)
			return
		}
		h(w, r)
	}
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

// siteDomainPattern turns a path-only pattern into a host+path pattern anchored
// to a project label under a configured site domain, e.g. ("pazer.site",
// "GET /{path...}") becomes "GET {project}.pazer.site/{path...}". A non-final
// host parameter binds exactly one request-host label, so the handler reads the
// project name via r.PathValue("project"). The pattern's two literal host
// labels outrank every service route's one, so the site domain's whole
// one-label-deep host space belongs to the project family.
func siteDomainPattern(domain, pattern string) string {
	method := ""
	rest := pattern
	if i := strings.IndexByte(pattern, ' '); i >= 0 {
		method = pattern[:i+1] // keep the trailing space
		rest = pattern[i+1:]
	}
	return method + "{project}." + domain + rest
}

// SiteDomainHandle registers a project-auth'd route on the {project}.<domain>
// scheme, the site-domain sibling of ServiceHandle. domain must be a literal
// (registration is per configured domain -- a {project}.{domain} form would
// swallow every >=3-label host with zero host literals and lose to every
// service route).
func SiteDomainHandle(domain, pattern string, parse ParseFunc, handler http.HandlerFunc) {
	mux.HandleFunc(siteDomainPattern(domain, pattern), router.Allow, requireProjectFunc(parse, handler))
}

// SiteDomainHandleRaw registers an unauthenticated route on the
// {project}.<domain> scheme (the /__sso redemption endpoint -- its caller is by
// definition unauthenticated).
func SiteDomainHandleRaw(domain, pattern string, handler http.HandlerFunc) {
	mux.HandleFunc(siteDomainPattern(domain, pattern), router.Allow, handler)
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
