package repackage

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/model"
)

type NPM struct{}

func (n *NPM) Format() Format { return FormatNPM }

func (n *NPM) Applicable(a model.Artifact) bool {
	return a.Kind != model.KindLibrary
}

func (n *NPM) Repackage(_ context.Context, input Input) (*Output, error) {
	size, err := inputSize(input.Binary)
	if err != nil {
		return nil, fmt.Errorf("get input size: %w", err)
	}

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

	tw.WriteHeader(&tar.Header{
		Name: "package/package.json",
		Size: int64(len(packageJSON)),
		Mode: 0o644,
	})
	tw.Write([]byte(packageJSON))

	binMode := int64(0o644)
	if input.Artifact.Kind == model.KindBinary {
		binMode = 0o755
	}
	tw.WriteHeader(&tar.Header{
		Name: "package/bin/" + input.Project.Name,
		Size: size,
		Mode: binMode,
	})
	io.Copy(tw, input.Binary)

	tw.Close()
	gw.Close()

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
