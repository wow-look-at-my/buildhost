package apt

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/repackage"
)

// debDigest returns the size and hex sha256 of the artifact's deb repackage --
// the exact payload the pool download serves via dl/static. The pair is cached
// in packaged_artifacts under format "deb" (brew's tarGZSHA256 pattern), so it
// is computed once per artifact instead of on every Packages/Release/InRelease
// request. Caching a digest for a payload that is regenerated per download is
// sound because deb generation is deterministic for a fixed input set: fixed
// ar member headers (zero timestamp/uid/gid), zero tar mtimes, fixed gzip
// header fields (mtime 0, OS 255), fixed member order, and the
// content-addressed stored blob (pinned by TestDebGenerationDeterministic).
// The row is a digest cache only: no deb blob is stored, storage_key records
// the SOURCE artifact blob (a key the retention refcount already tracks), and
// the row is dropped with its artifact on eviction (deleteReleaseRows).
//
// Unlike tar.gz -- whose bytes depend only on inputs that are immutable for a
// given artifact row -- the deb also bakes in MUTABLE project fields: the
// control file carries Description and Homepage, and the create_service
// materialization adds postinst/prerm/unit members. Those can change with no
// new release (PATCH /api/v1/projects/{project}, or a publish re-asserting
// project metadata), which would leave a stale digest advertised for a deb
// that now downloads differently -- apt would then reject every download
// against the signed index. So the row's metadata records a fingerprint of
// exactly those inputs (debDigestFingerprint) and a mismatch is treated as a
// miss: the row is refilled in place via the INSERT OR REPLACE upsert.
func (h *Handler) debDigest(ctx context.Context, project *db.Project, release *db.Release, a *db.PlatformArtifact, baseURL string) (int64, string, error) {
	fp := debDigestFingerprint(project, &a.Artifact)
	cacheFormat := a.CacheFormat(string(repackage.FormatDeb))
	_, size, sum, _, metadata, err := h.DB.GetPackagedArtifact(ctx, a.ID, cacheFormat)
	if err == nil && debMetadataFingerprint(metadata) == fp {
		return size, sum, nil
	}
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		return 0, "", err
	}

	out, err := h.Gen.GenerateForPlatform(ctx, repackage.FormatDeb, *project, *release, *a, baseURL)
	if err != nil {
		return 0, "", err
	}
	hsh := sha256.New()
	n, err := io.Copy(hsh, out.Reader)
	out.Reader.Close()
	if err != nil {
		return 0, "", err
	}
	sum = fmt.Sprintf("%x", hsh.Sum(nil))

	// Best-effort cache fill: the digest above is already correct for this
	// response. INSERT OR REPLACE makes a concurrent double-compute benign --
	// the value is deterministic per input set, so racing writers store the
	// same digest (and a fingerprint-triggered refill simply replaces the
	// stale row).
	metaJSON, merr := json.Marshal(debMetadata{Inputs: fp})
	if merr != nil {
		return n, sum, nil
	}
	if err := h.DB.CreatePackagedArtifact(ctx, a.ID, cacheFormat, a.StorageKey, n, sum, out.Filename, string(metaJSON)); err != nil {
		slog.Warn("cache deb digest", "artifact_id", a.ID, "err", err)
	}
	return n, sum, nil
}

// debMetadata is the packaged_artifacts.metadata document stored with format
// "deb" rows.
type debMetadata struct {
	// Inputs fingerprints the mutable generation inputs the cached digest was
	// computed under; see debDigestFingerprint.
	Inputs string `json:"inputs_sha256"`
}

// debMetadataFingerprint extracts the stored input fingerprint ("" for a
// missing/unparseable document, which reads as a mismatch and refills).
func debMetadataFingerprint(metadata string) string {
	var m debMetadata
	if err := json.Unmarshal([]byte(metadata), &m); err != nil {
		return ""
	}
	return m.Inputs
}

// debDigestFingerprint hashes every deb-generation input that can change
// WITHOUT the artifact row changing: the project name (defensive -- there is
// no rename path today), description, homepage, and the effective
// create_service materialization. Everything else that shapes the deb bytes
// (release version, artifact arch/kind/filename, the content-addressed blob)
// is immutable for a given artifact id, so (artifact_id, fingerprint) pins the
// exact bytes the cached digest describes.
func debDigestFingerprint(project *db.Project, a *db.Artifact) string {
	withService := project.CreateService && a.Kind == db.KindBinary
	hsh := sha256.New()
	// repackage.TransformVersion is part of the fingerprint because the deb's
	// payload is the artifact AFTER download-time transformation: if stripping
	// changes, these digests must be recomputed or apt rejects every pool
	// download with "Hash Sum mismatch".
	for _, s := range []string{project.Name, project.Description, project.Homepage, fmt.Sprintf("service=%t", withService), repackage.TransformVersion} {
		hsh.Write([]byte(s))
		hsh.Write([]byte{0})
	}
	return fmt.Sprintf("%x", hsh.Sum(nil))
}
