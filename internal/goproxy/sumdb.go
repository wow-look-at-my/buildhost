package goproxy

import (
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/auth"
)

// sumdbName is the one checksum database this proxy mirrors. A GOSUMDB of
// "<key> https://goproxy.{domain}/sumdb/sum.golang.org" reaches it.
const sumdbName = "sum.golang.org"

// sumdbOrigin is where a mirrored request goes. A test replaces it.
var sumdbOrigin = "https://" + sumdbName

// The checksum database holds public modules only. The go command skips it for
// every module in GONOSUMDB, so no private module path reaches this handler.
func registerSumDBRoutes() {
	auth.ServiceHandleRaw(Subdomain, "GET /sumdb/{name}/{rest...}", serveSumDB)
}

func serveSumDB(w http.ResponseWriter, r *http.Request) {
	name, rest := r.PathValue("name"), r.PathValue("rest")
	if name != sumdbName {
		http.Error(w, "this proxy mirrors only the "+sumdbName+" checksum database, not "+name, http.StatusNotFound)
		return
	}
	// The go command asks for "supported" to learn whether the proxy mirrors this database.
	if rest == "supported" {
		w.WriteHeader(http.StatusOK)
		return
	}
	if !strings.HasPrefix(rest, "lookup/") && !strings.HasPrefix(rest, "tile/") && rest != "latest" {
		http.Error(w, "not a checksum database endpoint: "+rest, http.StatusNotFound)
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, sumdbOrigin+"/"+rest, nil)
	if err != nil {
		http.Error(w, "building the checksum database request: "+err.Error(), http.StatusBadGateway)
		return
	}
	resp, err := sumdbClient.Do(req)
	if err != nil {
		http.Error(w, sumdbName+" did not answer: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for _, h := range []string{"Content-Type", "Cache-Control"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

var sumdbClient = &http.Client{Timeout: time.Minute}
