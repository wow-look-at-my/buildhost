package oci

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

var handler Handler

func init() {
	auth.OnReady(func() {
		handler.DB = auth.DB()
		handler.Store = auth.Store()
	})
	auth.HandleRaw("GET /v2/", handler.V2Root)
	auth.HandleHandler("/v2/", parseRoute, &handler)
}

type route struct {
	project   string
	action    string
	reference string
}

func (r route) ProjectName() string      { return r.project }
func (r route) Access() auth.AccessLevel { return auth.ReadAccess }

// ociActions are the reserved action keywords from the OCI distribution spec.
// They sit between the (possibly multi-segment) name and the reference.
var ociActions = []string{"manifests", "blobs", "tags"}

func parseRoute(r *http.Request) auth.RouteInfo {
	// OCI distribution path: /v2/{name}/{action}/{reference}
	// {name} may contain '/' (e.g. "library/nginx"), so a naive split-by-'/'
	// can't distinguish "library/nginx" + "manifests" from a 3-segment name.
	//
	// References (tags, digests) never contain '/' per spec, so the right
	// boundary is the RIGHTMOST /<action>/ whose trailing portion has no '/'.
	// This handles names that themselves contain an action keyword as a segment
	// (e.g. project "foo/manifests" with action "blobs").
	path := strings.TrimPrefix(r.URL.Path, "/v2/")

	bestI := -1
	var bestAction string
	for _, action := range ociActions {
		needle := "/" + action + "/"
		i := strings.LastIndex(path, needle)
		if i <= 0 {
			continue
		}
		ref := path[i+len(needle):]
		if strings.Contains(ref, "/") {
			continue
		}
		if i > bestI {
			bestI = i
			bestAction = action
		}
	}
	if bestI > 0 {
		return route{
			project:   path[:bestI],
			action:    bestAction,
			reference: path[bestI+len("/"+bestAction+"/"):],
		}
	}

	// Action-only URLs: /v2/{name}/{action} with no reference (will 404).
	for _, action := range ociActions {
		suffix := "/" + action
		if strings.HasSuffix(path, suffix) && len(path) > len(suffix) {
			return route{
				project: path[:len(path)-len(suffix)],
				action:  action,
			}
		}
	}
	// Bare name (or /v2/ root). Auth/handler will 404 -- nothing to serve.
	return route{project: path}
}

func routeFrom(ctx context.Context) route {
	return auth.RouteInfoFrom(ctx).(route)
}

type Handler struct {
	DB    *db.DB
	Store storage.Storage
}

func (h *Handler) V2Root(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{})
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt := routeFrom(r.Context())

	// TODO: respect rt.reference -- currently all tags/digests resolve to the same manifest
	switch rt.action {
	case "manifests":
		if rt.reference == "" {
			http.NotFound(w, r)
			return
		}
		h.serveManifest(w, r, rt.reference)
	case "blobs":
		if rt.reference == "" {
			http.NotFound(w, r)
			return
		}
		h.serveBlob(w, r, rt.reference)
	default:
		http.NotFound(w, r)
	}
}
