package oci

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/repackage"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

var handler Handler

func init() {
	auth.OnReady(func() {
		handler.DB = auth.DB()
		handler.Store = auth.Store()
		handler.Gen = repackage.NewGenerator(auth.Store(), auth.BaseURL(), auth.DataDir()+"/tmp")
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

func parseRoute(r *http.Request) auth.RouteInfo {
	path := strings.TrimPrefix(r.URL.Path, "/v2/")
	parts := strings.SplitN(path, "/", 3)
	rt := route{}
	if len(parts) >= 1 {
		rt.project = parts[0]
	}
	if len(parts) >= 2 {
		rt.action = parts[1]
	}
	if len(parts) >= 3 {
		rt.reference = parts[2]
	}
	return rt
}

func routeFrom(ctx context.Context) route {
	return auth.RouteInfoFrom(ctx).(route)
}

type Handler struct {
	DB    *db.DB
	Store storage.Storage
	Gen   *repackage.Generator
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
