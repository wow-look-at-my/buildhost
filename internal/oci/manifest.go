package oci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/model"
)

var validDigest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

func (h *Handler) serveManifest(w http.ResponseWriter, r *http.Request, reference string) {
	project := auth.ProjectFrom(r.Context())

	if validDigest.MatchString(reference) {
		h.serveManifestByDigest(w, r, project, reference)
		return
	}

	release, err := h.resolveTag(r.Context(), project, reference)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	h.serveIndex(w, r, project, release)
}

func (h *Handler) resolveTag(ctx context.Context, project *model.Project, tag string) (*model.Release, error) {
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

func (h *Handler) serveIndex(w http.ResponseWriter, r *http.Request, project *model.Project, release *model.Release) {
	artifacts, err := h.DB.ListArtifacts(r.Context(), release.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

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
		storageKey, size, _, _, err := h.DB.GetPackagedArtifact(r.Context(), a.ID, "oci")
		if err != nil {
			continue
		}
		entry := indexEntry{
			MediaType: "application/vnd.oci.image.manifest.v1+json",
			Digest:    "sha256:" + storageKey,
			Size:      size,
		}
		entry.Platform.Architecture = string(a.Arch)
		entry.Platform.OS = string(a.OS)
		manifests = append(manifests, entry)
	}

	if len(manifests) == 0 {
		http.NotFound(w, r)
		return
	}

	// Single platform: serve the manifest directly instead of an index
	if len(manifests) == 1 {
		h.serveManifestByDigest(w, r, project, manifests[0].Digest)
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

	w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(indexData)))
	w.Header().Set("Docker-Content-Digest", "sha256:"+hex.EncodeToString(digest[:]))

	if r.Method != http.MethodHead {
		w.Write(indexData)
	}
}

func (h *Handler) serveManifestByDigest(w http.ResponseWriter, r *http.Request, project *model.Project, digest string) {
	key := digest[7:]

	belongs, err := h.DB.BlobBelongsToProject(r.Context(), project.ID, key)
	if err != nil || !belongs {
		http.NotFound(w, r)
		return
	}

	rc, size, err := h.Store.Get(r.Context(), key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
	w.Header().Set("Docker-Content-Digest", digest)

	if r.Method != http.MethodHead {
		io.Copy(w, rc)
	}
}

func (h *Handler) serveBlob(w http.ResponseWriter, r *http.Request, digest string) {
	if !validDigest.MatchString(digest) {
		http.NotFound(w, r)
		return
	}
	key := digest[7:]

	project := auth.ProjectFrom(r.Context())
	belongs, err := h.DB.BlobBelongsToProject(r.Context(), project.ID, key)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !belongs {
		http.NotFound(w, r)
		return
	}

	rc, size, err := h.Store.Get(r.Context(), key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
	w.Header().Set("Docker-Content-Digest", digest)

	if r.Method != http.MethodHead {
		io.Copy(w, rc)
	}
}

func (h *Handler) serveTags(w http.ResponseWriter, r *http.Request) {
	project := auth.ProjectFrom(r.Context())
	releases, err := h.DB.ListReleases(r.Context(), project.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var tags []string
	hasOCI := false
	for _, rel := range releases {
		if !rel.Published {
			continue
		}
		artifacts, err := h.DB.ListArtifacts(r.Context(), rel.ID)
		if err != nil {
			continue
		}
		for _, a := range artifacts {
			if _, _, _, _, err := h.DB.GetPackagedArtifact(r.Context(), a.ID, "oci"); err == nil {
				tags = append(tags, rel.Version)
				hasOCI = true
				break
			}
		}
	}
	if hasOCI {
		tags = append(tags, "latest")
	}

	resp := map[string]any{
		"name": project.Name,
		"tags": tags,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
