// Package ociclient pushes a locally built container image (an OCI image
// layout directory or tarball, as produced by `docker buildx build --output
// type=oci` or `docker save`) to a buildhost OCI registry.
//
// Its reason to exist is upload sizing: docker/buildx/crane push every blob as
// ONE request, and the proxy in front of a deployed buildhost caps request
// bodies (Cloudflare's edge 413s bodies over ~100 MB), so layers past the cap
// can never arrive through those clients. This client asks the server for its
// advertised safe request size (GET /api/v1/server-info,
// max_direct_upload_bytes) and uploads every larger blob through the OCI
// chunked upload session -- sequential PATCH appends each under the limit,
// finalized by a digest-checked PUT -- so layers of any size pass the proxy.
// The decision is made from the blob size up front, never by reacting to a
// 413. Interrupted chunks resume from the server's committed size (the upload
// status endpoint).
package ociclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/uploadclient"
)

// retryAttempts bounds per-chunk no-progress loops and status-read retries.
const retryAttempts = 4

// RetryBaseDelay is doubled per retry (1s, 2s, 4s). Var so tests can shorten it.
var RetryBaseDelay = time.Second

// Pusher pushes an OCI image layout to one buildhost registry project.
type Pusher struct {
	// Registry is the OCI registry host[:port], e.g. "oci.pazer.build".
	Registry string
	// Project is the image's project name on buildhost (may be slash-namespaced).
	Project string
	// Token authenticates every request (Bearer; a write-scoped API token or a
	// GitHub Actions OIDC JWT).
	Token string
	// PlainHTTP uses http:// instead of https:// (local servers, tests).
	PlainHTTP bool
	// Server is the apex server URL for GET /api/v1/server-info. Empty derives
	// it from Registry by stripping the leading service label ("oci." /
	// "docker.").
	Server string
	// ChunkSize caps how much of a blob each request carries; 0 uses the
	// server's advertised max_direct_upload_bytes (fallback 95 MiB). Values
	// above the advertised limit are clamped down to it -- a bigger request
	// would be exactly the 413 this client exists to avoid.
	ChunkSize int64
	// Client defaults to http.DefaultClient.
	Client *http.Client
	// Stdout receives progress lines; nil discards them.
	Stdout io.Writer

	chunk  int64 // resolved per Push
	pushed map[string]bool
}

// ParseRefs parses full image references ("<registry>/<project>[:tag]") into
// one registry+project and the tag set. Every ref must name the same registry
// and project (one push, several tags); a ref without a tag means "latest".
func ParseRefs(refs []string) (registry, project string, tags []string, err error) {
	if len(refs) == 0 {
		return "", "", nil, fmt.Errorf("at least one image reference is required")
	}
	for _, ref := range refs {
		reg, proj, tag, err := splitRef(ref)
		if err != nil {
			return "", "", nil, err
		}
		if registry == "" {
			registry, project = reg, proj
		} else if reg != registry || proj != project {
			return "", "", nil, fmt.Errorf("all references must name the same registry and project: %s/%s vs %s/%s", registry, project, reg, proj)
		}
		tags = append(tags, tag)
	}
	return registry, project, tags, nil
}

// splitRef splits "<registry>/<project>[:tag]". The first path segment must
// look like a registry host (contain "." or ":", the docker convention), since
// there is no default registry to assume.
func splitRef(ref string) (registry, project, tag string, err error) {
	slash := strings.Index(ref, "/")
	if slash <= 0 {
		return "", "", "", fmt.Errorf("reference %q must include a registry host (e.g. oci.example.com/project:tag)", ref)
	}
	registry = ref[:slash]
	if !strings.ContainsAny(registry, ".:") {
		return "", "", "", fmt.Errorf("reference %q: first segment %q does not look like a registry host", ref, registry)
	}
	rest := ref[slash+1:]
	tag = "latest"
	if colon := strings.LastIndex(rest, ":"); colon >= 0 && !strings.Contains(rest[colon:], "/") {
		tag = rest[colon+1:]
		rest = rest[:colon]
	}
	if rest == "" || tag == "" {
		return "", "", "", fmt.Errorf("reference %q has an empty project or tag", ref)
	}
	return registry, rest, tag, nil
}

// DeriveServer guesses the apex server URL from a registry host by stripping
// the leading OCI service label. Returns "" when the host has no such label
// (the caller must then pass --server explicitly for server-info; pushes still
// work with the built-in chunk fallback).
func DeriveServer(registry string, plainHTTP bool) string {
	host, ok := strings.CutPrefix(registry, "oci.")
	if !ok {
		host, ok = strings.CutPrefix(registry, "docker.")
	}
	if !ok || host == "" {
		return ""
	}
	scheme := "https"
	if plainHTTP {
		scheme = "http"
	}
	return scheme + "://" + host
}

func (p *Pusher) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return http.DefaultClient
}

func (p *Pusher) stdout() io.Writer {
	if p.Stdout != nil {
		return p.Stdout
	}
	return io.Discard
}

func (p *Pusher) baseURL() string {
	scheme := "https"
	if p.PlainHTTP {
		scheme = "http"
	}
	return scheme + "://" + p.Registry
}

// Push uploads the image at layoutPath (an OCI layout directory or tarball)
// and tags it with every tag.
func (p *Pusher) Push(layoutPath string, tags []string) error {
	if len(tags) == 0 {
		return fmt.Errorf("at least one tag is required")
	}
	l, err := openLayout(layoutPath)
	if err != nil {
		return err
	}
	defer l.Close()

	root, err := l.root()
	if err != nil {
		return err
	}

	p.chunk = p.resolveChunkSize()
	p.pushed = map[string]bool{}
	fmt.Fprintf(p.stdout(), "pushing %s to %s/%s (chunk limit %d MiB)\n", root.Digest, p.Registry, p.Project, p.chunk>>20)

	body, manifest, err := l.readManifest(root)
	if err != nil {
		return err
	}
	if err := p.pushChildren(l, manifest); err != nil {
		return err
	}

	mediaType := manifest.MediaType
	if mediaType == "" {
		mediaType = root.MediaType
	}
	for _, tag := range tags {
		if err := p.putManifest(tag, mediaType, body); err != nil {
			return err
		}
		fmt.Fprintf(p.stdout(), "tagged %s/%s:%s (%s)\n", p.Registry, p.Project, tag, root.Digest)
	}
	return nil
}

// pushChildren uploads everything a manifest references, depth-first: an
// index's child manifests (each pushed by digest after its own children), an
// image manifest's config and layer blobs.
func (p *Pusher) pushChildren(l *layout, m *imageManifest) error {
	if isIndexMediaType(m.MediaType) || len(m.Manifests) > 0 {
		for _, child := range m.Manifests {
			body, childManifest, err := l.readManifest(child)
			if err != nil {
				return err
			}
			if err := p.pushChildren(l, childManifest); err != nil {
				return err
			}
			mt := childManifest.MediaType
			if mt == "" {
				mt = child.MediaType
			}
			if err := p.putManifest(child.Digest, mt, body); err != nil {
				return err
			}
		}
		return nil
	}
	blobs := m.Layers
	if m.Config != nil {
		blobs = append(blobs, *m.Config)
	}
	for _, b := range blobs {
		if err := p.pushBlob(l, b); err != nil {
			return err
		}
	}
	return nil
}

// resolveChunkSize picks the per-request byte cap: the server's advertised
// max_direct_upload_bytes (fallback: the built-in 95 MiB default), further
// lowered -- never raised -- by an explicit ChunkSize. Anything above the
// advertised limit is the 413 this client exists to avoid, so overrides only
// clamp down. A negative ChunkSize disables chunking entirely (every blob is
// one monolithic request -- for servers with no proxy body cap).
func (p *Pusher) resolveChunkSize() int64 {
	if p.ChunkSize < 0 {
		return math.MaxInt64
	}
	limit := p.serverDirectLimit()
	if p.ChunkSize > 0 && p.ChunkSize < limit {
		return p.ChunkSize
	}
	return limit
}

// serverDirectLimit fetches max_direct_upload_bytes from the apex
// /api/v1/server-info. Any failure falls back to the built-in default -- never
// an error, exactly like the REST upload client.
func (p *Pusher) serverDirectLimit() int64 {
	server := p.Server
	if server == "" {
		server = DeriveServer(p.Registry, p.PlainHTTP)
	}
	if server == "" {
		return uploadclient.DefaultChunkThreshold
	}
	req, err := http.NewRequest(http.MethodGet, server+"/api/v1/server-info", nil)
	if err != nil {
		return uploadclient.DefaultChunkThreshold
	}
	c := *p.client()
	c.Timeout = 5 * time.Second
	resp, err := c.Do(req)
	if err != nil {
		return uploadclient.DefaultChunkThreshold
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return uploadclient.DefaultChunkThreshold
	}
	var info struct {
		MaxDirectUploadBytes int64 `json:"max_direct_upload_bytes"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&info) != nil || info.MaxDirectUploadBytes <= 0 {
		return uploadclient.DefaultChunkThreshold
	}
	return info.MaxDirectUploadBytes
}

// pushBlob uploads one blob, skipping blobs the registry already has (HEAD),
// monolithically when it fits in a single request and through a chunked upload
// session otherwise.
func (p *Pusher) pushBlob(l *layout, d descriptor) error {
	if p.pushed[d.Digest] {
		return nil
	}
	path, err := l.blobPath(d.Digest, d.Size)
	if err != nil {
		return err
	}

	if exists, err := p.blobExists(d.Digest); err != nil {
		return err
	} else if exists {
		fmt.Fprintf(p.stdout(), "  blob %s already present, skipped\n", d.Digest)
		p.pushed[d.Digest] = true
		return nil
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}

	if st.Size() <= p.chunk {
		err = p.pushBlobMonolithic(d.Digest, f, st.Size())
	} else {
		err = p.pushBlobChunked(d.Digest, f, st.Size())
	}
	if err != nil {
		return err
	}
	p.pushed[d.Digest] = true
	return nil
}

func (p *Pusher) blobExists(digest string) (bool, error) {
	resp, err := p.do(http.MethodHead, p.baseURL()+"/v2/"+p.Project+"/blobs/"+digest, nil, 0, nil)
	if err != nil {
		return false, fmt.Errorf("check blob %s: %w", digest, err)
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK, nil
}

// pushBlobMonolithic uploads a blob in one request:
// POST /v2/{name}/blobs/uploads/?digest=... with the blob as the body.
func (p *Pusher) pushBlobMonolithic(digest string, f *os.File, size int64) error {
	resp, err := p.do(http.MethodPost, p.baseURL()+"/v2/"+p.Project+"/blobs/uploads/?digest="+url.QueryEscape(digest), f, size, nil)
	if err != nil {
		return fmt.Errorf("upload blob %s: %w", digest, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("upload blob %s failed: %s: %s", digest, resp.Status, readErrBody(resp))
	}
	fmt.Fprintf(p.stdout(), "  blob %s uploaded (%d bytes)\n", digest, size)
	return nil
}

// putManifest uploads a manifest or index under a tag or digest reference.
func (p *Pusher) putManifest(reference, mediaType string, body []byte) error {
	if mediaType == "" {
		return fmt.Errorf("manifest %s has no mediaType", reference)
	}
	header := map[string]string{"Content-Type": mediaType}
	resp, err := p.do(http.MethodPut, p.baseURL()+"/v2/"+p.Project+"/manifests/"+reference, bytes.NewReader(body), int64(len(body)), header)
	if err != nil {
		return fmt.Errorf("put manifest %s: %w", reference, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("put manifest %s failed: %s: %s", reference, resp.Status, readErrBody(resp))
	}
	return nil
}

// do issues one authenticated request. size < 0 leaves Content-Length unset.
func (p *Pusher) do(method, target string, body io.Reader, size int64, header map[string]string) (*http.Response, error) {
	req, err := http.NewRequest(method, target, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.ContentLength = size
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	if p.Token != "" {
		req.Header.Set("Authorization", "Bearer "+p.Token)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	return p.client().Do(req)
}

// absoluteURL resolves a Location header (usually path-absolute) against the
// registry base.
func (p *Pusher) absoluteURL(loc string) (string, error) {
	base, err := url.Parse(p.baseURL())
	if err != nil {
		return "", err
	}
	ref, err := url.Parse(loc)
	if err != nil {
		return "", fmt.Errorf("parse Location %q: %w", loc, err)
	}
	return base.ResolveReference(ref).String(), nil
}

// addQuery returns target with an extra query parameter, preserving any the
// server already put in the Location URL.
func addQuery(target, key, value string) (string, error) {
	u, err := url.Parse(target)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set(key, value)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// readErrBody returns a short prefix of an error response body for messages.
func readErrBody(resp *http.Response) string {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return strings.TrimSpace(string(b))
}
