package auth

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

var (
	mux         = http.NewServeMux()
	mw          *Middleware
	readyFuncs  []func()
	sharedDB    *db.DB
	sharedStore storage.Storage
	sharedBase  string
	sharedData  string
	serviceURLs map[string]*url.URL
)

func Mux() *http.ServeMux       { return mux }
func DB() *db.DB                { return sharedDB }
func Store() storage.Storage    { return sharedStore }
func BaseURL() string           { return sharedBase }
func DataDir() string           { return sharedData }
func GetMiddleware() *Middleware { return mw }

func ServiceURL(name string) *url.URL { return serviceURLs[name] }
func StaticURL() *url.URL             { return serviceURLs["static"] }
func DLBaseURL() *url.URL             { return serviceURLs["dl"] }

func OnReady(fn func()) {
	readyFuncs = append(readyFuncs, fn)
}

func Init(database *db.DB, store storage.Storage, baseURL, dataDir, domain string, svcOverrides map[string]string, trustedIssuers, allowedOrgs, allowedEvents []string) {
	mux = http.NewServeMux()
	sharedDB = database
	sharedStore = store
	sharedBase = baseURL
	sharedData = dataDir

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
	svcURL := serviceURLs[subdomain]
	host := svcURL.Host
	prefix := svcURL.Path
	if method, path, ok := strings.Cut(pattern, " "); ok {
		return method + " " + host + prefix + path
	}
	return host + prefix + pattern
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
	mux.HandleFunc(fromURL.Host+"/", func(w http.ResponseWriter, r *http.Request) {
		target := &url.URL{
			Scheme:   toURL.Scheme,
			Host:     toURL.Host,
			Path:     toURL.Path + r.URL.Path,
			RawQuery: r.URL.RawQuery,
		}
		http.Redirect(w, r, target.String(), code)
	})
}
