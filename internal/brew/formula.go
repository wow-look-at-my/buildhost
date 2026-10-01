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

func (h *Handler) formulaForRelease(ctx context.Context, project db.Project, release db.Release, artifacts []db.PlatformArtifact, baseURL string) (*repackage.Output, error) {
	// A digit-leading project name can never be a loadable Homebrew formula
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
		osName, archName, ok := brewPlatform(a.Artifact)
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
		Private: project.IsPrivate,
		// The project's packaging-agnostic create_service setting, which the
		Service:   project.CreateService,
		Resources: resources,
	})
}

// tarGZSHA256 returns the hex sha256 of the artifact's tar.gz repackage -- the
// exact payload the formula's download URL serves via dl/static. The digest is
func (h *Handler) tarGZSHA256(ctx context.Context, project db.Project, release db.Release, a db.PlatformArtifact, baseURL string) (string, error) {
	cacheFormat := a.CacheFormat(string(repackage.FormatTarGZ))
	_, _, cached, _, metadata, err := h.DB.GetPackagedArtifact(ctx, a.ID, cacheFormat)
	if err == nil && tarGZMetadataTransform(metadata) == repackage.TransformVersion {
		return cached, nil
	}
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		return "", err
	}

	tgz, err := h.Gen.GenerateForPlatform(ctx, repackage.FormatTarGZ, project, release, a, baseURL)
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
	metaJSON, merr := json.Marshal(tarGZMetadata{Transform: repackage.TransformVersion})
	if merr != nil {
		return sum, nil
	}
	if err := h.DB.CreatePackagedArtifact(ctx, a.ID, cacheFormat, a.StorageKey, size, sum, tgz.Filename, string(metaJSON)); err != nil {
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
