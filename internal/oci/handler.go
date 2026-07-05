package oci

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/repackage"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

var handler Handler

func init() {
	auth.OnReady(func() {
		handler.DB = auth.DB()
		handler.Store = auth.Store()
		handler.Gen = repackage.NewGenerator(auth.Store(), auth.DB(), auth.DataDir()+"/tmp")
		handler.uploads = newUploadStore(auth.DataDir()+"/tmp/oci-uploads", config.MaxBlobSize())
	})
	auth.ServiceHandleRaw("oci", "GET /v2/{$}", handler.V2Root)
	auth.ServiceHandleRaw("oci", "HEAD /v2/{$}", handler.V2Root)
	auth.ServiceHandleHandler("oci", "/v2/", parseRoute, &handler)

	auth.ServiceRedirect("docker", "oci", true)
}

type route struct {
	project   string
	action    string
	reference string
	method    string
}

func (r route) ProjectName() string { return r.project }

// Access is write for push verbs (so requireProject enforces a write-scoped
// token authorized for the project) and read for pulls.
func (r route) Access() auth.AccessLevel {
	switch r.method {
	case http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete:
		return auth.WriteAccess
	}
	return auth.ReadAccess
}

var ociActions = []string{"manifests", "blobs", "tags"}

func parseRoute(r *http.Request) auth.RouteInfo {
	rt := parseOCIPath(strings.TrimPrefix(r.URL.Path, "/v2/"))
	rt.method = r.Method
	return rt
}

func parseOCIPath(path string) route {
	if i := strings.LastIndex(path, "/blobs/uploads"); i > 0 {
		after := path[i+len("/blobs/uploads"):]
		if after == "" || strings.HasPrefix(after, "/") {
			return route{project: path[:i], action: "uploads", reference: strings.TrimPrefix(after, "/")}
		}
	}

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

	for _, action := range ociActions {
		suffix := "/" + action
		if strings.HasSuffix(path, suffix) && len(path) > len(suffix) {
			return route{
				project: path[:len(path)-len(suffix)],
				action:  action,
			}
		}
	}
	return route{project: path}
}

func routeFrom(ctx context.Context) route {
	return auth.RouteInfoFrom(ctx).(route)
}

type Handler struct {
	DB      *db.DB
	Store   storage.Storage
	Gen     *repackage.Generator
	uploads *uploadStore
}

// V2Root answers the OCI base endpoint GET/HEAD /v2/. The Docker/OCI client
// begins every auth handshake with an unauthenticated request here to discover
// the scheme: a registry that requires credentials MUST reply 401 with a
// WWW-Authenticate challenge so the client knows to send them. Replying 200
// anonymously makes the client conclude no auth is needed -- it never sends
// credentials, the first real (manifest) request then 401s, and the pull dies.
// Mirror the manifest/blob endpoints: challenge when unauthenticated, 200 once a
// valid credential is presented (the global auth middleware has, by this point,
// placed the verified token in the request context).
func (h *Handler) V2Root(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	if auth.TokenFrom(r.Context()) == nil {
		w.Header().Set("Www-Authenticate", `Basic realm="buildhost"`)
		ociError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{})
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")

	rt := routeFrom(r.Context())

	switch rt.action {
	case "manifests":
		if r.Method == http.MethodPut {
			h.PutManifest(w, r, rt.reference)
			return
		}
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
	case "uploads":
		switch r.Method {
		case http.MethodPost:
			h.StartBlobUpload(w, r)
		case http.MethodPatch:
			h.PatchBlobUpload(w, r, rt.reference)
		case http.MethodPut:
			h.PutBlobUpload(w, r, rt.reference)
		default:
			ociError(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "unsupported method")
		}
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
