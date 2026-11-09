// mirror.go serves mirrored upstream content through static.{domain}/file.

package static

import (
	"net/http"
	"regexp"
	"sync"
)

// MirrorSource serves content a mirror holds. sha256 is lower-case hex and
// name is the file name the client gets.
type MirrorSource interface {
	ServeMirrored(w http.ResponseWriter, r *http.Request, sha256, name string)
}

var (
	mirrorMu      sync.RWMutex
	mirrorSources = map[string]MirrorSource{}
)

// RegisterMirror names the source that answers ?mirror=<name>.
func RegisterMirror(name string, src MirrorSource) {
	mirrorMu.Lock()
	defer mirrorMu.Unlock()
	mirrorSources[name] = src
}

func lookupMirror(name string) (MirrorSource, bool) {
	mirrorMu.RLock()
	defer mirrorMu.RUnlock()
	src, ok := mirrorSources[name]
	return src, ok
}

var (
	sha256Re     = regexp.MustCompile(`^[a-f0-9]{64}$`)
	mirrorNameRe = regexp.MustCompile(`^[A-Za-z0-9._+@~-]{1,255}$`)
)

// serveMirror answers a ?mirror= request. It runs without the project check:
// mirrored content is public upstream, and sha256 names it exactly.
func serveMirror(w http.ResponseWriter, r *http.Request) {
	canonical := canonicalQuery(r.URL.Query())
	if canonical != r.URL.RawQuery {
		u := *r.URL
		u.RawQuery = canonical
		http.Redirect(w, r, u.String(), http.StatusMovedPermanently)
		return
	}
	q := r.URL.Query()
	src, ok := lookupMirror(q.Get("mirror"))
	sum, name := q.Get("sha256"), q.Get("name")
	if !ok || !sha256Re.MatchString(sum) || !mirrorNameRe.MatchString(name) || name == "." || name == ".." {
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "a mirror download needs mirror=<source>, sha256=<hex> and name=<file>", http.StatusNotFound)
		return
	}
	src.ServeMirrored(w, r, sum, name)
}
