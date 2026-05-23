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
	auth.HandleRaw("GET /v2/{$}", handler.V2Root)
	auth.HandleRaw("HEAD /v2/{$}", handler.V2Root)
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
}

func (h *Handler) V2Root(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	json.NewEncoder(w).Encode(map[string]any{})
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")

	rt := routeFrom(r.Context())

	switch rt.action {
	case "manifests":
		if rt.reference == "" {
			ociError(w, http.StatusNotFound, "MANIFEST_UNKNOWN", "manifest reference required")
			return
		}
		h.serveManifest(w, r, rt.reference)
	case "blobs":
		if rt.reference == "" {
			ociError(w, http.StatusNotFound, "BLOB_UNKNOWN", "blob digest required")
			return
		}
		h.serveBlob(w, r, rt.reference)
	case "tags":
		h.serveTags(w, r)
	default:
		ociError(w, http.StatusNotFound, "NAME_UNKNOWN", "unknown endpoint")
	}
}

func ociError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"errors": []map[string]string{
			{"code": code, "message": message},
		},
	})
}
