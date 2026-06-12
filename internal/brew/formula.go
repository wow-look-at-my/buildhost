package brew

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/repackage"
)

func (h *Handler) formulaForRelease(ctx context.Context, project db.Project, release db.Release, artifacts []db.Artifact, baseURL string) (*repackage.Output, error) {
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

		tgz, err := h.Gen.Generate(ctx, repackage.FormatTarGZ, project, release, a, baseURL)
		if err != nil {
			return nil, err
		}
		hsh := sha256.New()
		_, err = io.Copy(hsh, tgz.Reader)
		tgz.Reader.Close()
		if err != nil {
			return nil, err
		}
		sum := hsh.Sum(nil)

		resources = append(resources, repackage.BrewResource{
			OS:     osName,
			Arch:   archName,
			URL:    brewDownloadURL(baseURL, project.Name, release.Version, a.OS, a.Arch),
			SHA256: fmt.Sprintf("%x", sum),
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
		Resources:   resources,
	})
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
