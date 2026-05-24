package repackage

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/model"
)

func init() { Register(&NPM{}) }

type NPM struct{}

func (n *NPM) Format() Format { return FormatNPM }

func (n *NPM) Applicable(a model.Artifact) bool {
	return a.Kind != model.KindLibrary
}

func (n *NPM) Repackage(_ context.Context, input Input) (*Output, error) {
	version := strings.TrimPrefix(input.Release.Version, "v")
	if version == "" {
		version = fmt.Sprintf("%d.0.0", input.Release.VersionNum)
	}
	if !strings.Contains(version, ".") {
		version = version + ".0.0"
	}

	npmOS := npmPlatform(input.Artifact.OS)
	npmArch := npmArch(input.Artifact.Arch)

	pkgJSON, _ := json.MarshalIndent(map[string]any{
		"name":        "@buildhost/" + input.Project.Name + "-" + npmOS + "-" + npmArch,
		"version":     version,
		"description": firstNonEmpty(input.Project.Description, input.Project.Name),
		"os":          []string{npmOS},
		"cpu":         []string{npmArch},
		"bin":         map[string]string{input.Project.Name: "./bin/" + input.Project.Name},
	}, "", "  ")
	packageJSON := string(pkgJSON) + "\n"

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	if err := tw.WriteHeader(&tar.Header{
		Name: "package/package.json",
		Size: int64(len(packageJSON)),
		Mode: 0o644,
	}); err != nil {
		return nil, fmt.Errorf("write tar header: %w", err)
	}
	if _, err := tw.Write([]byte(packageJSON)); err != nil {
		return nil, fmt.Errorf("write package.json: %w", err)
	}

	binMode := int64(0o644)
	if input.Artifact.Kind == model.KindBinary {
		binMode = 0o755
	}
	if err := tw.WriteHeader(&tar.Header{
		Name: "package/bin/" + input.Project.Name,
		Size: int64(len(input.Data)),
		Mode: binMode,
	}); err != nil {
		return nil, fmt.Errorf("write tar header: %w", err)
	}
	if _, err := tw.Write(input.Data); err != nil {
		return nil, fmt.Errorf("write binary: %w", err)
	}

	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("close tar: %w", err)
	}
	if err := gw.Close(); err != nil {
		return nil, fmt.Errorf("close gzip: %w", err)
	}

	filename := fmt.Sprintf("%s-%s-%s-%s.tgz", input.Project.Name, version, npmOS, npmArch)
	return &Output{
		Reader:   &buf,
		Filename: filename,
		Size:     int64(buf.Len()),
	}, nil
}

func npmPlatform(os model.OS) string {
	switch os {
	case model.OSDarwin:
		return "darwin"
	case model.OSWindows:
		return "win32"
	default:
		return string(os)
	}
}

func npmArch(a model.Arch) string {
	switch a {
	case model.ArchAMD64:
		return "x64"
	case model.ArchARM64:
		return "arm64"
	case model.Arch386:
		return "ia32"
	default:
		return string(a)
	}
}
