package auth

import (
	"net/http"

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
	patterns    []string
)

func Mux() *http.ServeMux        { return mux }
func DB() *db.DB                 { return sharedDB }
func Store() storage.Storage     { return sharedStore }
func BaseURL() string            { return sharedBase }
func DataDir() string            { return sharedData }
func GetMiddleware() *Middleware  { return mw }

func OnReady(fn func()) {
	readyFuncs = append(readyFuncs, fn)
}

func Init(database *db.DB, store storage.Storage, baseURL, dataDir string, trustedIssuers, allowedOrgs, allowedEvents []string) {
	sharedDB = database
	sharedStore = store
	sharedBase = baseURL
	sharedData = dataDir
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

func Patterns() []string { return patterns }

func Handle(pattern string, parse ParseFunc, handler http.HandlerFunc) {
	patterns = append(patterns, pattern)
	mux.HandleFunc(pattern, requireProjectFunc(parse, handler))
}

func HandleRaw(pattern string, handler http.HandlerFunc) {
	patterns = append(patterns, pattern)
	mux.HandleFunc(pattern, handler)
}

func HandleHandler(pattern string, parse ParseFunc, handler http.Handler) {
	patterns = append(patterns, pattern)
	mux.Handle(pattern, requireProject(parse)(handler))
}
