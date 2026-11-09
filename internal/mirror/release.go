// The release mirror serves GitHub release assets under GitHub's own path:
// mirror.{domain}/github.com/<owner>/<repo>/releases/download/<tag>/<asset>.
// An asset comes from GitHub once and from the blob store after that.

package mirror

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
)

// ReleaseUpstream is GitHub. Its Service is the upstream column of each row.
var ReleaseUpstream = Upstream{Service: "github-releases", Remote: "https://github.com"}

const (
	// ReleaseSubdomain is the release mirror's own service. No project route shares it.
	ReleaseSubdomain = "mirror"
	// ReleaseRoot is the first path segment: the upstream host.
	ReleaseRoot = "github.com"
)

// ReleaseHandler serves GitHub release assets. It stores them as Handler stores blobs.
type ReleaseHandler struct {
	Handler
}

// Releases is the registered release handler.
var Releases = &ReleaseHandler{Handler: Handler{Up: ReleaseUpstream}}

func init() {
	h := Releases
	auth.OnReady(func() {
		h.DB = auth.DB()
		h.Store = auth.Store()
		h.TmpDir = auth.DataDir() + "/tmp/mirror"
		client, err := newClient()
		if err != nil {
			slog.Error("mirror: no upstream client; every release download through the mirror fails", "upstream", h.Up.Service, "err", err)
			h.clientErr = err
			return
		}
		h.Client = client
	})
	auth.ServiceHandleRaw(ReleaseSubdomain, h.Pattern(), h.ServeHTTP)
}

// Pattern is the only shape the mirror serves.
func (h *ReleaseHandler) Pattern() string {
	return "GET /" + ReleaseRoot + "/{owner}/{repo}/releases/download/{tag}/{asset}"
}

// releasePath is one asset URL. repository is owner/repo as the client spelled it.
type releasePath struct {
	repository, tag, asset string
}

// key is the repository as the database records it. GitHub ignores the case of owner and repo.
func (p releasePath) key() string { return strings.ToLower(p.repository) }

// upstreamPath is the path of the asset on GitHub, with each segment escaped.
func (p releasePath) upstreamPath() string {
	var b strings.Builder
	for _, s := range strings.Split(p.repository, "/") {
		b.WriteString("/" + url.PathEscape(s))
	}
	b.WriteString("/releases/download")
	for _, s := range strings.Split(p.tag, "/") {
		b.WriteString("/" + url.PathEscape(s))
	}
	b.WriteString("/" + url.PathEscape(p.asset))
	return b.String()
}

var (
	ownerRe    = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
	repoNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
	segmentRe  = regexp.MustCompile(`^[A-Za-z0-9._+@~-]{1,255}$`)
)

// maxTagBytes bounds a tag. A git ref name is short.
const maxTagBytes = 255

func validSegment(s string) bool {
	return segmentRe.MatchString(s) && s != "." && s != ".."
}

// releasePathOf checks the values the route matched. A route value can span
// segments, so owner, repo and asset must each still be one segment.
func releasePathOf(owner, repo, tag, asset string) (releasePath, bool) {
	if !ownerRe.MatchString(owner) || !repoNameRe.MatchString(repo) || repo == "." || repo == ".." || !validSegment(asset) {
		return releasePath{}, false
	}
	for _, s := range strings.Split(tag, "/") {
		if !validSegment(s) {
			return releasePath{}, false
		}
	}
	if len(tag) > maxTagBytes {
		return releasePath{}, false
	}
	return releasePath{repository: owner + "/" + repo, tag: tag, asset: asset}, true
}

// ServeHTTP serves one release asset.
func (h *ReleaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// The CDN caches a .zip or a .tar.gz by its extension.
	w.Header().Set("Cache-Control", "private")
	w.Header().Set("Vary", "Authorization")
	if !authorized(r) {
		w.Header().Set("Www-Authenticate", `Basic realm="buildhost"`)
		http.Error(w, "a buildhost token with the read scope is required", http.StatusUnauthorized)
		return
	}
	p, ok := releasePathOf(r.PathValue("owner"), r.PathValue("repo"), r.PathValue("tag"), r.PathValue("asset"))
	if !ok {
		http.Error(w, "not a GitHub release asset path", http.StatusNotFound)
		return
	}
	ctx := r.Context()
	digest, known, err := h.DB.GetMirrorReleaseAsset(ctx, h.Up.Service, p.key(), p.tag, p.asset)
	if err != nil {
		h.releaseFail(w, err)
		return
	}
	if known {
		blob, found, err := h.stored(ctx, digest)
		if err != nil {
			h.releaseFail(w, err)
			return
		}
		if found {
			setDisposition(w, p.asset)
			h.serveStored(w, r, blob)
			return
		}
	}
	h.fetchAsset(w, r, p)
}

// fetchAsset streams an asset to the client and to a temporary file at once.
// The file goes to the store when the whole body arrived. A client that goes
// away does not stop the fetch, so the next download still finds the asset.
func (h *ReleaseHandler) fetchAsset(w http.ResponseWriter, r *http.Request, p releasePath) {
	ctx := context.WithoutCancel(r.Context())
	resp, err := h.send(ctx, http.MethodGet, h.Up.Remote+p.upstreamPath(), "", "")
	if err != nil {
		h.releaseFail(w, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		h.releaseFail(w, h.statusError(resp, p.repository, "releases/download/"+p.tag+"/"+p.asset))
		return
	}
	if err := os.MkdirAll(h.TmpDir, 0o755); err != nil {
		h.releaseFail(w, err)
		return
	}
	tmp, err := os.CreateTemp(h.TmpDir, "asset-*")
	if err != nil {
		h.releaseFail(w, err)
		return
	}
	defer func() {
		tmp.Close()
		os.Remove(tmp.Name())
	}()
	w.Header().Set("Content-Type", "application/octet-stream")
	setDisposition(w, p.asset)
	if resp.ContentLength >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	}
	w.WriteHeader(http.StatusOK)
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, hash, &clientWriter{w: w}), resp.Body)
	if err != nil {
		slog.Error("mirror: release asset fetch broke off; nothing stored", "repository", p.repository, "tag", p.tag, "asset", p.asset, "bytes", n, "err", err)
		return
	}
	if resp.ContentLength >= 0 && n != resp.ContentLength {
		slog.Error("mirror: release asset is shorter than its Content-Length; nothing stored", "repository", p.repository, "tag", p.tag, "asset", p.asset, "bytes", n, "want", resp.ContentLength)
		return
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		slog.Error("mirror: rewind release asset", "asset", p.asset, "err", err)
		return
	}
	digest := "sha256:" + hex.EncodeToString(hash.Sum(nil))
	if _, err := h.save(ctx, digest, "", tmp); err != nil {
		slog.Error("mirror: store release asset", "repository", p.repository, "asset", p.asset, "err", err)
		return
	}
	if err := h.DB.PutMirrorReleaseAsset(ctx, h.Up.Service, p.key(), p.tag, p.asset, digest); err != nil {
		slog.Error("mirror: record release asset", "repository", p.repository, "asset", p.asset, "err", err)
		return
	}
	slog.Info("mirror: stored release asset", "repository", p.repository, "tag", p.tag, "asset", p.asset, "digest", digest, "bytes", n)
}

// setDisposition names the file the way GitHub names it.
func setDisposition(w http.ResponseWriter, asset string) {
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": asset}))
}

func (h *ReleaseHandler) releaseFail(w http.ResponseWriter, err error) {
	var ue *upstreamError
	if !errors.As(err, &ue) {
		slog.Error("mirror: release request failed", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	switch {
	case ue.absent():
		http.Error(w, ue.Error(), http.StatusNotFound)
	case ue.status == http.StatusTooManyRequests:
		http.Error(w, ue.Error(), http.StatusTooManyRequests)
	default:
		slog.Error("mirror: GitHub failed a release download", "err", err)
		http.Error(w, ue.Error(), http.StatusBadGateway)
	}
}
