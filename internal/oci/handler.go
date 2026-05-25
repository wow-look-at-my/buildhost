package oci

import (
	"context"
	"encoding/json"
	"net/http"

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
		handler.Gen = repackage.NewGenerator(auth.Store(), auth.DB(), auth.BaseURL(), auth.DataDir()+"/tmp")

		auth.HandleRaw(auth.ServiceRoute("oci", "GET /v2/{$}"), handler.V2Root)
		auth.HandleRaw(auth.ServiceRoute("oci", "HEAD /v2/{$}"), handler.V2Root)
		auth.Handle(auth.ServiceRoute("oci", "GET /v2/{project}/manifests/{reference}"), parseRoute, handler.ServeManifest)
		auth.Handle(auth.ServiceRoute("oci", "HEAD /v2/{project}/manifests/{reference}"), parseRoute, handler.ServeManifestHead)
		auth.Handle(auth.ServiceRoute("oci", "GET /v2/{project}/blobs/{digest}"), parseRoute, handler.ServeBlob)
		auth.Handle(auth.ServiceRoute("oci", "GET /v2/{project}/tags/list"), parseRoute, handler.ServeTags)

		auth.ServiceRedirect("docker", "oci", true)
	})
}

type route struct {
	project   string
	reference string
	digest    string
}

func (r route) ProjectName() string      { return r.project }
func (r route) Access() auth.AccessLevel { return auth.ReadAccess }

func parseRoute(r *http.Request) auth.RouteInfo {
	return route{
		project:   r.PathValue("project"),
		reference: r.PathValue("reference"),
		digest:    r.PathValue("digest"),
	}
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
	w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	json.NewEncoder(w).Encode(map[string]any{})
}

func (h *Handler) ServeManifest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	rt := routeFrom(r.Context())
	h.serveManifest(w, r, rt.reference)
}

func (h *Handler) ServeManifestHead(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	rt := routeFrom(r.Context())
	h.serveManifest(w, r, rt.reference)
}

func (h *Handler) ServeBlob(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	rt := routeFrom(r.Context())
	h.serveBlob(w, r, rt.digest)
}

func (h *Handler) ServeTags(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	h.serveTags(w, r)
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
