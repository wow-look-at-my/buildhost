package brew

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"sort"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/repackage"
)

func (h *Handler) formulaForRelease(ctx context.Context, project db.Project, release db.Release, artifacts []db.Artifact, baseURL string) (*repackage.Output, error) {
	// A digit-leading project name can never be a loadable Homebrew formula
	// (see repackage.BrewEligibleProjectName); emitting one would put
	// syntactically invalid Ruby in the tap and break evaluation of every
	// formula in it. Treat it as not found: the formula endpoints 404 and the
	// tap build skips it.
	if !repackage.BrewEligibleProjectName(project.Name) {
		return nil, db.ErrNotFound
	}

	resources := make([]repackage.BrewResource, 0, len(artifacts))
	var kind string

	sort.SliceStable(artifacts, func(i, j int) bool {
		if artifacts[i].OS != artifacts[j].OS {
			return artifacts[i].OS < artifacts[j].OS
		}
		return artifacts[i].Arch < artifacts[j].Arch
	})

	for _, a := range artifacts {
		osName, archName, ok := brewPlatform(a)
		if !ok {
			continue
		}
		if kind == "" {
			kind = string(a.Kind)
		}

		sum, err := h.tarGZSHA256(ctx, project, release, a, baseURL)
		if err != nil {
			return nil, err
		}

		resources = append(resources, repackage.BrewResource{
			OS:     osName,
			Arch:   archName,
			URL:    brewDownloadURL(baseURL, project.Name, release.Version, a.OS, a.Arch),
			SHA256: sum,
		})
	}

	if len(resources) == 0 {
		return nil, db.ErrNotFound
	}

	version := strings.TrimPrefix(release.Version, "v")
	if version == "" {
		version = fmt.Sprintf("%d", release.VersionNum)
	}

	return repackage.RenderBrewFormula(repackage.BrewFormula{
		ClassName:   repackage.BrewClassName(project.Name),
		Name:        project.Name,
		Description: firstNonEmpty(project.Description, project.Name),
		Homepage:    firstNonEmpty(project.Homepage, baseURL),
		Version:     version,
		License:     firstNonEmpty(project.License, "MIT"),
		Kind:        kind,
		// A private project's formula downloads through the tap's token-aware
		// strategy (the artifact endpoints reject anonymous requests).
		Private: project.IsPrivate,
		// The project's packaging-agnostic create_service setting, which the
		// brew format materializes as a `service do` block so `brew services
		// start` manages the binary as a login service.
		Service:   project.CreateService,
		Resources: resources,
	})
}

// tarGZSHA256 returns the hex sha256 of the artifact's tar.gz repackage -- the
// exact payload the formula's download URL serves via dl/static. The digest is
// cached in packaged_artifacts under format "tar.gz" so it is computed once per
// artifact instead of on every formula/tap request. Caching a digest for a blob
// that is regenerated per download is sound because tar.gz generation is
// deterministic for an artifact: the tar header carries only the immutable
// project name, size, and kind-derived mode (zero mtimes -- archive/tar writes
// a zero ModTime as constant epoch 0), gzip emits fixed header fields (mtime 0,
// OS 255), and the input is the content-addressed stored blob. Homebrew's own
// checksum verification of the on-demand download already depends on exactly
// this stability. The row is a digest cache only: no tar.gz blob is stored, so
// storage_key records the SOURCE artifact blob (a key the retention refcount
// already tracks) and the row is dropped with its artifact on eviction.
func (h *Handler) tarGZSHA256(ctx context.Context, project db.Project, release db.Release, a db.Artifact, baseURL string) (string, error) {
	_, _, cached, _, metadata, err := h.DB.GetPackagedArtifact(ctx, a.ID, string(repackage.FormatTarGZ))
	if err == nil && tarGZMetadataTransform(metadata) == repackage.TransformVersion {
		return cached, nil
	}
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		return "", err
	}

	tgz, err := h.Gen.Generate(ctx, repackage.FormatTarGZ, project, release, a, baseURL)
	if err != nil {
		return "", err
	}
	hsh := sha256.New()
	size, err := io.Copy(hsh, tgz.Reader)
	tgz.Reader.Close()
	if err != nil {
		return "", err
	}
	sum := fmt.Sprintf("%x", hsh.Sum(nil))

	// Best-effort cache fill: the digest above is already correct for this
	// response. INSERT OR REPLACE makes a concurrent double-compute benign --
	// the value is deterministic, so both writers store the same digest.
	metaJSON, merr := json.Marshal(tarGZMetadata{Transform: repackage.TransformVersion})
	if merr != nil {
		return sum, nil
	}
	if err := h.DB.CreatePackagedArtifact(ctx, a.ID, string(repackage.FormatTarGZ), a.StorageKey, size, sum, tgz.Filename, string(metaJSON)); err != nil {
		slog.Warn("cache tar.gz digest", "artifact_id", a.ID, "err", err)
	}
	return sum, nil
}

func brewPlatform(a db.Artifact) (string, string, bool) {
	if a.Kind == db.KindAssets || a.Kind.ServedViaDockerOnly() {
		return "", "", false
	}

	osName := ""
	switch a.OS {
	case db.OSDarwin:
		osName = "macos"
	case db.OSLinux:
		osName = "linux"
	default:
		return "", "", false
	}

	archName := ""
	switch a.Arch {
	case db.ArchAMD64:
		archName = "intel"
	case db.ArchARM64:
		archName = "arm"
	default:
		return "", "", false
	}

	return osName, archName, true
}

func brewDownloadURL(baseURL, project, version string, osName db.OS, arch db.Arch) string {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return ""
	}
	q := url.Values{}
	q.Set("arch", string(arch))
	q.Set("fmt", "tar.gz")
	q.Set("os", string(osName))
	q.Set("v", version)
	return u.Scheme + "://dl." + u.Host + "/" + project + "?" + q.Encode()
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// tarGZMetadata records which transformation pipeline a cached tar.gz digest
// was computed under. A row written before the field existed (or under a
// different pipeline) reads as a miss and is recomputed in place -- the
// alternative is a Homebrew formula whose sha256 describes bytes the server no
// longer produces, which fails `brew install` outright.
type tarGZMetadata struct {
	Transform string `json:"transform"`
}

func tarGZMetadataTransform(metadata string) string {
	var m tarGZMetadata
	if err := json.Unmarshal([]byte(metadata), &m); err != nil {
		return ""
	}
	return m.Transform
}
