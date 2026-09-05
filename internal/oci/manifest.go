package oci

import (
	"bytes"
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
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/repackage"
	"github.com/wow-look-at-my/go-containers/set"
)

var validDigest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// ociLinuxArtifacts filters a release's platform-slot artifacts down to linux
// targets -- the only ones Docker can run as a container image. For an APE this
// is what keeps darwin/* and windows/* out of the synthesized index while still
// emitting a linux/arm64 child when the APE declares it. It returns a filtered
// slice sharing the input's backing array; callers must not append to the result.
func ociLinuxArtifacts(arts []db.PlatformArtifact) []db.PlatformArtifact {
	out := arts[:0]
	for _, a := range arts {
		if a.OS == db.OSLinux {
			out = append(out, a)
		}
	}
	return out
}

func (h *Handler) serveManifest(w http.ResponseWriter, r *http.Request, reference string) {
	project := auth.ProjectFrom(r.Context())

	if validDigest.MatchString(reference) {
		h.serveManifestByDigest(w, r, project, reference)
		return
	}

	// Real docker images pushed to the registry: the tag points at a stored
	tag, err := h.DB.GetOCITag(r.Context(), project.ID, reference)
	if err == nil {
		h.serveManifestByDigest(w, r, project, tag.ManifestDigest)
		return
	}
	if !errors.Is(err, db.ErrNotFound) {
		ociError(w, http.StatusInternalServerError, "UNKNOWN", "internal error")
		return
	}

	// Binary projects: synthesize a minimal OCI image from the uploaded binary.
	release, err := h.resolveTag(r.Context(), project, reference)
	if err != nil {
		ociError(w, http.StatusNotFound, "MANIFEST_UNKNOWN", "manifest unknown")
		return
	}

	h.serveIndex(w, r, project, release)
}

func (h *Handler) resolveTag(ctx context.Context, project *db.Project, tag string) (*db.Release, error) {
	if tag == "latest" {
		return h.DB.GetLatestRelease(ctx, project.ID)
	}
	rel, err := h.DB.GetRelease(ctx, project.ID, tag)
	if err == nil {
		return rel, nil
	}
	rel, err = h.DB.GetRelease(ctx, project.ID, "v"+tag)
	if err == nil {
		return rel, nil
	}
	if strings.HasPrefix(tag, "v") {
		rel, err = h.DB.GetRelease(ctx, project.ID, tag[1:])
		if err == nil {
			return rel, nil
		}
	}
	return nil, fmt.Errorf("tag not found: %s", tag)
}

func (h *Handler) serveIndex(w http.ResponseWriter, r *http.Request, project *db.Project, release *db.Release) {
	artifacts, err := h.DB.ListArtifactsByPlatform(r.Context(), release.ID)
	if err != nil {
		ociError(w, http.StatusInternalServerError, "UNKNOWN", "internal error")
		return
	}
	// Docker can only run linux images, so advertise only the linux children an
	// APE artifact covers -- a darwin or windows child is not a runnable
	// container image, and advertising one is a broken promise to the client
	// (docker selects it and the container cannot start). A release whose APE
	// covers no linux platform then correctly serves no OCI image.
	artifacts = ociLinuxArtifacts(artifacts)

	type indexEntry struct {
		MediaType string `json:"mediaType"`
		Digest    string `json:"digest"`
		Size      int64  `json:"size"`
		Platform  struct {
			Architecture string `json:"architecture"`
			OS           string `json:"os"`
		} `json:"platform"`
	}

	var manifests []indexEntry
	for _, a := range artifacts {
		out, err := h.Gen.GenerateForPlatform(r.Context(), repackage.FormatOCI, *project, *release, a, auth.RequestRootURL(r))
		if err != nil {
			// The platform drops out of the index; say so, or the pull fails with "manifest unknown" and no cause anywhere.
			slog.ErrorContext(r.Context(), "oci: cannot synthesize image for platform",
				"project", project.Name, "version", release.Version, "os", a.OS, "arch", a.Arch, "err", err)
			continue
		}
		manifestData, err := io.ReadAll(out.Reader)
		out.Reader.Close()
		if err != nil {
			continue
		}
		sum := sha256.Sum256(manifestData)
		digest := "sha256:" + hex.EncodeToString(sum[:])

		// Integrity check: only advertise a child the pull path can actually
		belongs, err := h.DB.BlobBelongsToProject(r.Context(), project.ID, digest[7:])
		if err != nil || !belongs {
			continue
		}

		entry := indexEntry{
			MediaType: "application/vnd.oci.image.manifest.v1+json",
			Digest:    digest,
			Size:      int64(len(manifestData)),
		}
		entry.Platform.Architecture = string(a.Arch)
		entry.Platform.OS = string(a.OS)
		manifests = append(manifests, entry)
	}

	if len(manifests) == 0 {
		ociError(w, http.StatusNotFound, "MANIFEST_UNKNOWN", "no OCI artifacts for this release")
		return
	}

	if len(manifests) == 1 {
		h.serveSingleManifest(w, r, project, release, manifests[0])
		return
	}

	index := struct {
		SchemaVersion int          `json:"schemaVersion"`
		MediaType     string       `json:"mediaType"`
		Manifests     []indexEntry `json:"manifests"`
	}{
		SchemaVersion: 2,
		MediaType:     "application/vnd.oci.image.index.v1+json",
		Manifests:     manifests,
	}

	indexData, _ := json.Marshal(index)
	digest := sha256.Sum256(indexData)

	// Persist the index document under its own content digest and link it to the
	// project, exactly as Generate persisted each child manifest above. The Docker
	// daemon's classic (non-containerd) image store pulls a tag by reading the
	if err := h.persistManifestBlob(r.Context(), project.ID, indexData, "application/vnd.oci.image.index.v1+json"); err != nil {
		ociError(w, http.StatusInternalServerError, "UNKNOWN", "failed to persist index")
		return
	}

	w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(indexData)))
	w.Header().Set("Docker-Content-Digest", "sha256:"+hex.EncodeToString(digest[:]))

	if r.Method != http.MethodHead {
		w.Write(indexData)
	}
}

func (h *Handler) serveSingleManifest(w http.ResponseWriter, r *http.Request, project *db.Project, release *db.Release, entry struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	Platform  struct {
		Architecture string `json:"architecture"`
		OS           string `json:"os"`
	} `json:"platform"`
}) {
	artifacts, err := h.DB.ListArtifactsByPlatform(r.Context(), release.ID)
	if err != nil {
		ociError(w, http.StatusNotFound, "MANIFEST_UNKNOWN", "manifest unknown")
		return
	}
	// Same linux-only filter as serveIndex: serveIndex forwards to this when one
	// child remains, so without the filter here it could pick a darwin/windows
	// artifact that serveIndex already dropped.
	artifacts = ociLinuxArtifacts(artifacts)

	for _, a := range artifacts {
		out, err := h.Gen.GenerateForPlatform(r.Context(), repackage.FormatOCI, *project, *release, a, auth.RequestRootURL(r))
		if err != nil {
			slog.ErrorContext(r.Context(), "oci: cannot synthesize image for platform",
				"project", project.Name, "version", release.Version, "os", a.OS, "arch", a.Arch, "err", err)
			continue
		}
		manifestData, err := io.ReadAll(out.Reader)
		out.Reader.Close()
		if err != nil {
			continue
		}
		digest := sha256.Sum256(manifestData)

		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(manifestData)))
		w.Header().Set("Docker-Content-Digest", "sha256:"+hex.EncodeToString(digest[:]))

		if r.Method != http.MethodHead {
			w.Write(manifestData)
		}
		return
	}

	ociError(w, http.StatusNotFound, "MANIFEST_UNKNOWN", "manifest unknown")
}

func (h *Handler) serveManifestByDigest(w http.ResponseWriter, r *http.Request, project *db.Project, digest string) {
	if !validDigest.MatchString(digest) {
		ociError(w, http.StatusNotFound, "MANIFEST_UNKNOWN", "invalid digest format")
		return
	}
	key := digest[7:]

	belongs, err := h.DB.BlobBelongsToProject(r.Context(), project.ID, key)
	if err != nil || !belongs {
		ociError(w, http.StatusNotFound, "MANIFEST_UNKNOWN", "manifest unknown")
		return
	}

	rc, size, err := h.Store.Get(r.Context(), key)
	if err != nil {
		ociError(w, http.StatusNotFound, "MANIFEST_UNKNOWN", "manifest unknown")
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Type", h.manifestContentType(r.Context(), project, key))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
	w.Header().Set("Docker-Content-Digest", digest)

	if r.Method != http.MethodHead {
		io.Copy(w, rc)
	}
}

// persistManifestBlob stores a synthesized manifest or index document under its
// own content digest and links it to the project, so a subsequent by-digest
// GET/HEAD resolves via serveManifestByDigest (gated on BlobBelongsToProject).
func (h *Handler) persistManifestBlob(ctx context.Context, projectID int64, data []byte, mediaType string) error {
	key, size, err := h.Store.Put(ctx, bytes.NewReader(data))
	if err != nil {
		return err
	}
	return h.DB.LinkOCIBlob(ctx, projectID, key, mediaType, size, true)
}

func (h *Handler) serveBlob(w http.ResponseWriter, r *http.Request, digest string) {
	if !validDigest.MatchString(digest) {
		ociError(w, http.StatusNotFound, "BLOB_UNKNOWN", "invalid digest format")
		return
	}
	key := digest[7:]

	project := auth.ProjectFrom(r.Context())
	belongs, err := h.DB.BlobBelongsToProject(r.Context(), project.ID, key)
	if err != nil {
		ociError(w, http.StatusInternalServerError, "UNKNOWN", "internal error")
		return
	}
	if !belongs {
		ociError(w, http.StatusNotFound, "BLOB_UNKNOWN", "blob unknown to registry")
		return
	}

	rc, size, err := h.Store.Get(r.Context(), key)
	if err != nil {
		ociError(w, http.StatusNotFound, "BLOB_UNKNOWN", "blob unknown to registry")
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
	w.Header().Set("Docker-Content-Digest", digest)

	if r.Method != http.MethodHead {
		io.Copy(w, rc)
	}
}

// manifestContentType returns the media type a stored manifest/index should be
// served with, recovered from its blob-link record (so an image index is not
// mislabelled as an image manifest). Falls back to the OCI image manifest type.
func (h *Handler) manifestContentType(ctx context.Context, project *db.Project, key string) string {
	if link, err := h.DB.GetOCIBlobLink(ctx, project.ID, key); err == nil && link.MediaType != "" {
		return link.MediaType
	}
	return "application/vnd.oci.image.manifest.v1+json"
}

func (h *Handler) serveTags(w http.ResponseWriter, r *http.Request) {
	project := auth.ProjectFrom(r.Context())

	seen := set.New[string]()
	tags := []string{}
	add := func(t string) {
		if t != "" && seen.Add(t) {
			tags = append(tags, t)
		}
	}

	// Real docker tags pushed via the registry push API.
	if ociTags, err := h.DB.ListOCITags(r.Context(), project.ID); err == nil {
		for _, t := range ociTags {
			add(t.Tag)
		}
	}

	// Binary projects: each published release with a binary artifact is a
	releases, err := h.DB.ListReleases(r.Context(), project.ID)
	if err != nil {
		ociError(w, http.StatusInternalServerError, "UNKNOWN", "internal error")
		return
	}
	hasBinary := false
	for _, rel := range releases {
		if !rel.Published {
			continue
		}
		artifacts, err := h.DB.ListArtifacts(r.Context(), rel.ID)
		if err != nil {
			continue
		}
		for _, a := range artifacts {
			if a.Kind == db.KindBinary {
				add(rel.Version)
				hasBinary = true
				break
			}
		}
	}
	if hasBinary {
		add("latest")
	}

	resp := map[string]any{
		"name": project.Name,
		"tags": tags,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
