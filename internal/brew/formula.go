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

type formulaMode int

const (
	// formulaLatest is the unversioned formula; missing digests are computed.
	formulaLatest formulaMode = iota
	// formulaVersioned is a name@version formula; missing digests are computed.
	formulaVersioned
	// formulaVersionedCached is a name@version formula built from cached returns errDigestPending.
	formulaVersionedCached
)

var errDigestPending = errors.New("tar.gz digest not cached yet")

// brewVersion is the version string a formula declares, and the one a user
// types after "@".
func brewVersion(release db.Release) string {
	version := strings.TrimPrefix(release.Version, "v")
	if version == "" {
		version = fmt.Sprintf("%d", release.VersionNum)
	}
	return version
}

func (h *Handler) formulaForRelease(ctx context.Context, project db.Project, release db.Release, artifacts []db.PlatformArtifact, baseURL string, mode formulaMode) (*repackage.Output, error) {
	// A digit-leading project name can never be a loadable Homebrew formula
	if !repackage.BrewEligibleProjectName(project.Name) {
		return nil, db.ErrNotFound
	}
	version := brewVersion(release)
	className := repackage.BrewClassName(project.Name)
	if mode != formulaLatest {
		name, ok := repackage.BrewVersionedClassName(project.Name, version)
		if !ok {
			return nil, db.ErrNotFound
		}
		className = name
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

		var sum string
		var err error
		if mode == formulaVersionedCached {
			sum, err = h.cachedTarGZSHA256(ctx, a)
			if errors.Is(err, errDigestPending) {
				h.queueDigestFill(project, release, a, baseURL)
			}
		} else {
			sum, err = h.tarGZSHA256(ctx, project, release, a, baseURL)
		}
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

	return repackage.RenderBrewFormula(repackage.BrewFormula{
		ClassName:   className,
		Name:        project.Name,
		Description: firstNonEmpty(project.Description, project.Name),
		Homepage:    firstNonEmpty(project.Homepage, baseURL),
		Version:     version,
		License:     firstNonEmpty(project.License, "MIT"),
		Kind:        kind,
		// A private project's formula downloads through the tap's token-aware
		Private:   project.IsPrivate,
		Versioned: mode != formulaLatest,
		// The project's packaging-agnostic create_service setting.
		Service:   project.CreateService,
		Resources: resources,
	})
}

// tarGZSHA256 returns the hex sha256 of the artifact's tar.gz repackage -- the
// exact payload the formula's download URL serves via dl/static.
func (h *Handler) tarGZSHA256(ctx context.Context, project db.Project, release db.Release, a db.PlatformArtifact, baseURL string) (string, error) {
	cached, err := h.cachedTarGZSHA256(ctx, a)
	if !errors.Is(err, errDigestPending) {
		return cached, err
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
	if err := h.DB.CreatePackagedArtifact(ctx, a.ID, a.CacheFormat(string(repackage.FormatTarGZ)), a.StorageKey, size, sum, tgz.Filename, string(metaJSON)); err != nil {
		slog.Warn("cache tar.gz digest", "artifact_id", a.ID, "err", err)
	}
	return sum, nil
}

// cachedTarGZSHA256 returns the cached digest tarGZSHA256 would compute, or
// errDigestPending when there is none for the current TransformVersion.
func (h *Handler) cachedTarGZSHA256(ctx context.Context, a db.PlatformArtifact) (string, error) {
	_, _, cached, _, metadata, err := h.DB.GetPackagedArtifact(ctx, a.ID, a.CacheFormat(string(repackage.FormatTarGZ)))
	if errors.Is(err, db.ErrNotFound) {
		return "", errDigestPending
	}
	if err != nil {
		return "", err
	}
	if tarGZMetadataTransform(metadata) != repackage.TransformVersion {
		return "", errDigestPending
	}
	return cached, nil
}

type digestFill struct {
	project  db.Project
	release  db.Release
	artifact db.PlatformArtifact
	baseURL  string
}

// queueDigestFill schedules tarGZSHA256 for an artifact on the handler's one
// background filler. An artifact already queued or in progress is skipped.
func (h *Handler) queueDigestFill(project db.Project, release db.Release, a db.PlatformArtifact, baseURL string) {
	h.fillMu.Lock()
	defer h.fillMu.Unlock()
	if h.filling[a.ID] {
		return
	}
	if h.filling == nil {
		h.filling = map[int64]bool{}
	}
	h.filling[a.ID] = true
	h.fillWG.Add(1)
	h.fillQueue = append(h.fillQueue, digestFill{project: project, release: release, artifact: a, baseURL: baseURL})
	if !h.fillRunning {
		h.fillRunning = true
		go h.fillDigests()
	}
}

func (h *Handler) fillDigests() {
	for {
		h.fillMu.Lock()
		if len(h.fillQueue) == 0 {
			h.fillRunning = false
			h.fillMu.Unlock()
			return
		}
		job := h.fillQueue[0]
		h.fillQueue = h.fillQueue[1:]
		h.fillMu.Unlock()

		if _, err := h.tarGZSHA256(context.Background(), job.project, job.release, job.artifact, job.baseURL); err != nil {
			slog.Warn("fill tar.gz digest for versioned formula", "project", job.project.Name, "version", job.release.Version, "artifact_id", job.artifact.ID, "err", err)
		}

		h.fillMu.Lock()
		delete(h.filling, job.artifact.ID)
		h.fillMu.Unlock()
		h.fillWG.Done()
	}
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
