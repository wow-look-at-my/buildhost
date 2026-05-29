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
	"net/http"
	"strconv"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

const maxManifestSize = 4 << 20 // 4 MiB; manifests are tiny JSON documents

// descriptor is an OCI content descriptor (config, layer, or child manifest).
type descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	Platform  *struct {
		Architecture string `json:"architecture"`
		OS           string `json:"os"`
	} `json:"platform,omitempty"`
}

type parsedManifest struct {
	SchemaVersion int               `json:"schemaVersion"`
	MediaType     string            `json:"mediaType"`
	Config        *descriptor       `json:"config,omitempty"`
	Layers        []descriptor      `json:"layers,omitempty"`
	Manifests     []descriptor      `json:"manifests,omitempty"`
	Annotations   map[string]string `json:"annotations,omitempty"`
}

func isIndexMediaType(mt string) bool {
	return mt == "application/vnd.oci.image.index.v1+json" ||
		mt == "application/vnd.docker.distribution.manifest.list.v2+json"
}

// PutManifest handles PUT /v2/{name}/manifests/{reference}. It stores the
// manifest (or image index), associates every referenced blob with the project
// so the pull path will serve them, and -- when pushed by tag -- records a
// release of kind=docker artifacts and points the tag at it.
func (h *Handler) PutManifest(w http.ResponseWriter, r *http.Request, reference string) {
	if reference == "" {
		ociError(w, http.StatusNotFound, "MANIFEST_UNKNOWN", "manifest reference required")
		return
	}
	project := auth.ProjectFrom(r.Context())
	ctx := r.Context()

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxManifestSize))
	if err != nil {
		ociError(w, http.StatusBadRequest, "MANIFEST_INVALID", "failed to read manifest")
		return
	}
	digest := "sha256:" + hex.EncodeToString(sha256Sum(body))

	var m parsedManifest
	if err := json.Unmarshal(body, &m); err != nil {
		ociError(w, http.StatusBadRequest, "MANIFEST_INVALID", "manifest is not valid JSON")
		return
	}
	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = m.MediaType
	}
	index := len(m.Manifests) > 0 || isIndexMediaType(contentType)

	// Store the manifest document itself and link it to the project.
	key, size, err := h.Store.Put(ctx, bytes.NewReader(body))
	if err != nil || "sha256:"+key != digest {
		ociError(w, http.StatusInternalServerError, "UNKNOWN", "failed to store manifest")
		return
	}
	if err := h.DB.LinkOCIBlob(ctx, project.ID, key, contentType, size, true); err != nil {
		ociError(w, http.StatusInternalServerError, "UNKNOWN", "failed to record manifest")
		return
	}

	// Verify referenced blobs exist (already pushed) and link them.
	if index {
		for _, child := range m.Manifests {
			if !validDigest.MatchString(child.Digest) {
				continue
			}
			ok, _ := h.DB.BlobBelongsToProject(ctx, project.ID, child.Digest[7:])
			if !ok {
				ociError(w, http.StatusBadRequest, "MANIFEST_BLOB_UNKNOWN", "referenced manifest not found: "+child.Digest)
				return
			}
			h.DB.LinkOCIBlob(ctx, project.ID, child.Digest[7:], child.MediaType, child.Size, true)
		}
	} else {
		refs := append([]descriptor{}, m.Layers...)
		if m.Config != nil {
			refs = append(refs, *m.Config)
		}
		for _, ref := range refs {
			if !validDigest.MatchString(ref.Digest) {
				continue
			}
			ok, _ := h.DB.BlobBelongsToProject(ctx, project.ID, ref.Digest[7:])
			if !ok {
				ociError(w, http.StatusBadRequest, "MANIFEST_BLOB_UNKNOWN", "referenced blob not found: "+ref.Digest)
				return
			}
			h.DB.LinkOCIBlob(ctx, project.ID, ref.Digest[7:], ref.MediaType, ref.Size, false)
		}
	}

	// A push by digest (e.g. a child manifest of an index) is stored and linked
	// but does not create a tag or release; the index push (by tag) does that.
	if validDigest.MatchString(reference) {
		writeManifestCreated(w, project.Name, digest)
		return
	}

	// Idempotent re-push of an identical image to the same tag: no-op.
	if existing, err := h.DB.GetOCITag(ctx, project.ID, reference); err == nil && existing.ManifestDigest == digest {
		writeManifestCreated(w, project.Name, digest)
		return
	}

	release, err := h.createDockerRelease(ctx, project.ID, m.Annotations["org.opencontainers.image.revision"], m.Annotations["org.opencontainers.image.source"])
	if err != nil {
		ociError(w, http.StatusInternalServerError, "UNKNOWN", "failed to create release")
		return
	}

	if index {
		for _, child := range m.Manifests {
			if child.Platform == nil || child.Platform.OS == "" || child.Platform.OS == "unknown" || child.Platform.Architecture == "unknown" {
				continue // skip attestation / unknown-platform entries
			}
			h.createDockerArtifact(ctx, release.ID, child.Platform.OS, child.Platform.Architecture, child.Digest, child.Size)
		}
	} else {
		osName, arch := "", ""
		if m.Config != nil {
			osName, arch, _ = h.configPlatform(ctx, m.Config.Digest)
		}
		if osName == "" {
			osName = "linux"
		}
		h.createDockerArtifact(ctx, release.ID, osName, arch, digest, size)
	}

	if err := h.DB.PublishRelease(ctx, release.ID); err != nil {
		ociError(w, http.StatusInternalServerError, "UNKNOWN", "failed to publish release")
		return
	}
	if err := h.DB.SetOCITag(ctx, project.ID, reference, digest, release.ID); err != nil {
		ociError(w, http.StatusInternalServerError, "UNKNOWN", "failed to set tag")
		return
	}
	writeManifestCreated(w, project.Name, digest)
}

// createDockerRelease auto-versions a release, retrying on the rare version_num
// race (SQLite serializes writes, but two pushes can read the same max).
func (h *Handler) createDockerRelease(ctx context.Context, projectID int64, gitCommit, notes string) (*db.Release, error) {
	var lastErr error
	for range 5 {
		n, err := h.DB.NextVersionNum(ctx, projectID)
		if err != nil {
			return nil, err
		}
		rel := &db.Release{
			ProjectID:  projectID,
			Version:    strconv.FormatInt(n, 10),
			VersionNum: n,
			GitCommit:  gitCommit,
			Notes:      notes,
		}
		err = h.DB.CreateRelease(ctx, rel)
		if err == nil {
			return rel, nil
		}
		if errors.Is(err, db.ErrConflict) {
			lastErr = err
			continue
		}
		return nil, err
	}
	return nil, lastErr
}

func (h *Handler) createDockerArtifact(ctx context.Context, releaseID int64, osName, arch, manifestDigest string, size int64) {
	a := &db.Artifact{
		ReleaseID:  releaseID,
		OS:         db.OS(osName),
		Arch:       db.Arch(arch),
		Kind:       db.KindDocker,
		StorageKey: manifestDigest[7:],
		Size:       size,
		SHA256:     manifestDigest[7:],
	}
	// Ignore conflicts: duplicate platforms within one index are harmless.
	_ = h.DB.CreateArtifact(ctx, a)
}

// configPlatform reads a stored image config blob to recover its os/architecture.
func (h *Handler) configPlatform(ctx context.Context, configDigest string) (osName, arch string, err error) {
	if !validDigest.MatchString(configDigest) {
		return "", "", fmt.Errorf("invalid config digest")
	}
	rc, _, err := h.Store.Get(ctx, configDigest[7:])
	if err != nil {
		return "", "", err
	}
	defer rc.Close()
	var cfg struct {
		Architecture string `json:"architecture"`
		OS           string `json:"os"`
	}
	if err := json.NewDecoder(rc).Decode(&cfg); err != nil {
		return "", "", err
	}
	return cfg.OS, cfg.Architecture, nil
}

func writeManifestCreated(w http.ResponseWriter, name, digest string) {
	w.Header().Set("Location", "/v2/"+name+"/manifests/"+digest)
	w.Header().Set("Docker-Content-Digest", digest)
	w.WriteHeader(http.StatusCreated)
}

func sha256Sum(b []byte) []byte {
	s := sha256.Sum256(b)
	return s[:]
}
