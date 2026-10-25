// Package mirror is a read-only pull-through mirror of public registries. Each
// upstream is a path prefix on oci.{domain}: oci.{domain}/v2/ghcr/<repo>/...
// A manifest or blob comes from the upstream once and from the blob store after that.
package mirror

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

// Upstream is one public registry the mirror serves.
type Upstream struct {
	// Service is the path prefix under oci.{domain}/v2/ and the upstream column of each row.
	Service string
	// Remote is the registry base URL.
	Remote string
}

// Upstreams are the registries the mirror serves.
var Upstreams = []Upstream{
	{Service: "dockerhub", Remote: "https://registry-1.docker.io"},
	{Service: "ghcr", Remote: "https://ghcr.io"},
}

// TagTTL is how long a tag's digest is used before the upstream is asked again.
const TagTTL = time.Minute

// maxManifestBytes bounds a manifest body. A real one is a few kilobytes.
const maxManifestBytes = 4 << 20

// fullAccept asks for an index first, so the mirror stores what a multi-arch pull needs.
var fullAccept = strings.Join([]string{
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.docker.distribution.manifest.v2+json",
}, ", ")

// Handler serves one upstream.
type Handler struct {
	Up     Upstream
	DB     *db.DB
	Store  storage.Storage
	TmpDir string
	Client *http.Client

	tokens tokenCache
	// clientErr is why there is no upstream client. Every upstream call fails with it.
	clientErr error
}

// Handlers are the registered handlers, one per upstream.
var Handlers []*Handler

func init() {
	for _, u := range Upstreams {
		h := &Handler{Up: u}
		Handlers = append(Handlers, h)
		auth.OnReady(func() {
			h.DB = auth.DB()
			h.Store = auth.Store()
			h.TmpDir = auth.DataDir() + "/tmp/mirror"
			client, err := newClient()
			if err != nil {
				slog.Error("mirror: no upstream client; every pull through the mirror fails", "upstream", u.Service, "err", err)
				h.clientErr = err
				return
			}
			h.Client = client
		})
		auth.ServiceHandleRaw(Subdomain, h.Pattern(), h.ServeHTTP)
	}
}

// Subdomain is the service the mirror shares with buildhost's own registry.
const Subdomain = "oci"

// Pattern takes every method.
func (h *Handler) Pattern() string { return "/v2/" + h.Up.Service + "/{path...}" }

// authorized needs a token with the read scope.
func authorized(r *http.Request) bool {
	t := auth.TokenFrom(r.Context())
	return t != nil && t.HasScope("read")
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("Www-Authenticate", `Basic realm="buildhost"`)
	regError(w, http.StatusUnauthorized, "UNAUTHORIZED", "a buildhost token with the read scope is required")
}

// ServeHTTP serves every path under the upstream's prefix.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		regError(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "the mirror is read-only")
		return
	}
	if !authorized(r) {
		unauthorized(w)
		return
	}
	if r.PathValue("path") == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{})
		return
	}
	p, ok := parsePath(r.PathValue("path"))
	if !ok {
		regError(w, http.StatusNotFound, "NAME_UNKNOWN", "the mirror serves only /v2/<repository>/manifests/<reference> and /v2/<repository>/blobs/<digest>")
		return
	}
	if p.kind == "manifests" {
		h.manifest(w, r, p.repo, p.ref)
		return
	}
	h.blob(w, r, p.repo, p.ref)
}

type path struct {
	repo, kind, ref string
}

var (
	repoRe   = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*(?:/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*)*$`)
	tagRe    = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`)
	digestRe = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)

func parsePath(p string) (path, bool) {
	for _, kind := range []string{"manifests", "blobs"} {
		i := strings.LastIndex(p, "/"+kind+"/")
		if i <= 0 {
			continue
		}
		out := path{repo: p[:i], kind: kind, ref: p[i+len(kind)+2:]}
		if !repoRe.MatchString(out.repo) {
			return path{}, false
		}
		if digestRe.MatchString(out.ref) || (kind == "manifests" && tagRe.MatchString(out.ref)) {
			return out, true
		}
		return path{}, false
	}
	return path{}, false
}

func (h *Handler) manifest(w http.ResponseWriter, r *http.Request, repo, ref string) {
	ctx := r.Context()
	digest := ref
	if !digestRe.MatchString(ref) {
		d, mediaType, err := h.resolveTag(ctx, repo, ref)
		if err != nil {
			h.fail(w, err, "MANIFEST_UNKNOWN")
			return
		}
		if !accepts(r.Header.Get("Accept"), mediaType) {
			regError(w, http.StatusNotFound, "MANIFEST_UNKNOWN", fmt.Sprintf("%s:%s is a %s, which this request does not accept", repo, ref, mediaType))
			return
		}
		digest = d
	}
	blob, found, err := h.stored(ctx, digest)
	if err != nil {
		h.fail(w, err, "MANIFEST_UNKNOWN")
		return
	}
	if !found || blob.MediaType == "" {
		blob, err = h.fetchManifest(ctx, repo, digest)
		if err != nil {
			h.fail(w, err, "MANIFEST_UNKNOWN")
			return
		}
	}
	h.serveStored(w, r, blob)
}

// resolveTag answers the digest a tag names. It asks the upstream once per
// TagTTL. When the upstream fails, the last digest it named still serves, so
// a registry outage does not stop a pull the mirror already holds.
func (h *Handler) resolveTag(ctx context.Context, repo, tag string) (string, string, error) {
	last, known, err := h.DB.GetMirrorTag(ctx, h.Up.Service, repo, tag)
	if err != nil {
		return "", "", err
	}
	if known && time.Since(last.CheckedAt) < TagTTL {
		return last.Digest, last.MediaType, nil
	}
	digest, mediaType, err := h.headManifest(ctx, repo, tag)
	if err != nil {
		var ue *upstreamError
		if known && !(errors.As(err, &ue) && ue.absent()) {
			slog.Warn("mirror: the upstream did not answer for a tag; serving the digest it named last",
				"upstream", h.Up.Service, "repository", repo, "tag", tag, "digest", last.Digest, "checked_at", last.CheckedAt, "err", err)
			return last.Digest, last.MediaType, nil
		}
		return "", "", err
	}
	if err := h.DB.PutMirrorTag(ctx, h.Up.Service, repo, tag, digest, mediaType); err != nil {
		return "", "", err
	}
	return digest, mediaType, nil
}

func (h *Handler) headManifest(ctx context.Context, repo, tag string) (string, string, error) {
	resp, err := h.upstream(ctx, http.MethodHead, repo, "manifests/"+tag, fullAccept)
	if err != nil {
		return "", "", err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", h.statusError(resp, repo, "manifests/"+tag)
	}
	digest := resp.Header.Get("Docker-Content-Digest")
	mediaType := mediaTypeOf(resp.Header.Get("Content-Type"))
	if digestRe.MatchString(digest) && mediaType != "" {
		return digest, mediaType, nil
	}
	// The upstream named no digest, so read the body and hash it.
	blob, err := h.fetchManifest(ctx, repo, tag)
	if err != nil {
		return "", "", err
	}
	return blob.Digest, blob.MediaType, nil
}

// fetchManifest reads a manifest from the upstream and stores it. ref is a
// tag or a digest. A digest must match the body the upstream sent.
func (h *Handler) fetchManifest(ctx context.Context, repo, ref string) (db.MirrorBlob, error) {
	resp, err := h.upstream(ctx, http.MethodGet, repo, "manifests/"+ref, fullAccept)
	if err != nil {
		return db.MirrorBlob{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return db.MirrorBlob{}, h.statusError(resp, repo, "manifests/"+ref)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxManifestBytes+1))
	if err != nil {
		return db.MirrorBlob{}, &upstreamError{remote: h.Up.Remote, msg: "read manifest: " + err.Error()}
	}
	if len(body) > maxManifestBytes {
		return db.MirrorBlob{}, &upstreamError{remote: h.Up.Remote, msg: fmt.Sprintf("the manifest of %s:%s is over %d bytes", repo, ref, maxManifestBytes)}
	}
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if digestRe.MatchString(ref) && ref != digest {
		return db.MirrorBlob{}, &upstreamError{remote: h.Up.Remote, msg: fmt.Sprintf("asked for %s and got a body with digest %s", ref, digest)}
	}
	mediaType := mediaTypeOf(resp.Header.Get("Content-Type"))
	if mediaType == "" {
		return db.MirrorBlob{}, &upstreamError{remote: h.Up.Remote, msg: "the manifest of " + repo + ":" + ref + " has no Content-Type"}
	}
	return h.save(ctx, digest, mediaType, strings.NewReader(string(body)))
}

// save puts the bytes in the store and records them. Layers are compressed
// already, so the store keeps them as they are.
func (h *Handler) save(ctx context.Context, digest, mediaType string, r io.Reader) (db.MirrorBlob, error) {
	var (
		key  string
		size int64
		err  error
	)
	if up, ok := h.Store.(storage.UncompressedPutter); ok {
		key, size, err = up.PutUncompressed(ctx, r)
	} else {
		key, size, err = h.Store.Put(ctx, r)
	}
	if err != nil {
		return db.MirrorBlob{}, fmt.Errorf("store %s: %w", digest, err)
	}
	if "sha256:"+key != digest {
		return db.MirrorBlob{}, fmt.Errorf("the store keyed %s as %s", digest, key)
	}
	if err := h.DB.PutMirrorBlob(ctx, h.Up.Service, digest, mediaType, key, size); err != nil {
		return db.MirrorBlob{}, err
	}
	return db.MirrorBlob{Upstream: h.Up.Service, Digest: digest, MediaType: mediaType, StorageKey: key, Size: size}, nil
}

// stored returns the blob the mirror holds. A row whose bytes are gone from
// the store is dropped, so the caller fetches it again.
func (h *Handler) stored(ctx context.Context, digest string) (db.MirrorBlob, bool, error) {
	blob, found, err := h.DB.GetMirrorBlob(ctx, h.Up.Service, digest)
	if err != nil || !found {
		return blob, found, err
	}
	ok, err := h.Store.Exists(ctx, blob.StorageKey)
	if err != nil {
		return blob, false, err
	}
	if ok {
		return blob, true, nil
	}
	slog.Error("mirror: a recorded blob is missing from the store; fetching it again", "upstream", h.Up.Service, "digest", digest)
	if err := h.DB.DeleteMirrorBlob(ctx, h.Up.Service, digest); err != nil {
		return blob, false, err
	}
	return db.MirrorBlob{}, false, nil
}

func (h *Handler) serveStored(w http.ResponseWriter, r *http.Request, blob db.MirrorBlob) {
	ctx := r.Context()
	if err := h.DB.MarkMirrorBlobRead(ctx, h.Up.Service, blob.Digest); err != nil {
		slog.Error("mirror: mark read", "digest", blob.Digest, "err", err)
	}
	w.Header().Set("Docker-Content-Digest", blob.Digest)
	w.Header().Set("Etag", `"`+blob.Digest+`"`)
	if blob.MediaType != "" {
		w.Header().Set("Content-Type", blob.MediaType)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	if rg, ok := h.Store.(storage.RandomGetter); ok {
		ra, size, err := rg.OpenReaderAt(ctx, blob.StorageKey)
		if err == nil {
			defer ra.Close()
			http.ServeContent(w, r, "", time.Time{}, io.NewSectionReader(ra, 0, size))
			return
		}
		if !errors.Is(err, storage.ErrRandomUnsupported) {
			h.fail(w, err, "BLOB_UNKNOWN")
			return
		}
	}
	rc, size, err := h.Store.Get(ctx, blob.StorageKey)
	if err != nil {
		h.fail(w, err, "BLOB_UNKNOWN")
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	if r.Method == http.MethodHead {
		return
	}
	io.Copy(w, rc)
}

// mediaTypeOf drops the parameters of a Content-Type.
func mediaTypeOf(ct string) string {
	mt, _, _ := strings.Cut(ct, ";")
	return strings.TrimSpace(mt)
}

// accepts reports whether an Accept header takes mediaType. No header takes anything.
func accepts(header, mediaType string) bool {
	if strings.TrimSpace(header) == "" {
		return true
	}
	for _, part := range strings.Split(header, ",") {
		mt := mediaTypeOf(part)
		if mt == "*/*" || mt == mediaType {
			return true
		}
	}
	return false
}

func regError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"errors": []map[string]string{{"code": code, "message": msg}},
	})
}

// fail answers an error.
func (h *Handler) fail(w http.ResponseWriter, err error, absentCode string) {
	var ue *upstreamError
	if !errors.As(err, &ue) {
		slog.Error("mirror: request failed", "upstream", h.Up.Service, "err", err)
		regError(w, http.StatusInternalServerError, "UNKNOWN", err.Error())
		return
	}
	switch {
	case ue.absent():
		regError(w, http.StatusNotFound, absentCode, ue.Error())
	case ue.status == http.StatusTooManyRequests:
		regError(w, http.StatusTooManyRequests, "TOOMANYREQUESTS", ue.Error())
	default:
		slog.Error("mirror: the upstream failed", "upstream", h.Up.Service, "err", err)
		regError(w, http.StatusBadGateway, "UNKNOWN", ue.Error())
	}
}
