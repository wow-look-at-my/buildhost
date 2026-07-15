package repackage

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/db"
)

func init() { Register(&NPM{}) }

type NPM struct{}

func (n *NPM) Format() Format { return FormatNPM }

func (n *NPM) Applicable(a db.Artifact) bool {
	return a.Kind != db.KindLibrary && !a.Kind.ServedViaDockerOnly()
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

	binMode := int64(0o644)
	if input.Artifact.Kind == db.KindBinary {
		binMode = 0o755
	}

	r := streamPipe(func(w io.Writer) error {
		gw := gzip.NewWriter(w)
		tw := tar.NewWriter(gw)
		if err := tw.WriteHeader(&tar.Header{Name: "package/package.json", Size: int64(len(packageJSON)), Mode: 0o644}); err != nil {
			return fmt.Errorf("write tar header: %w", err)
		}
		if _, err := tw.Write([]byte(packageJSON)); err != nil {
			return fmt.Errorf("write package.json: %w", err)
		}
		if err := tw.WriteHeader(&tar.Header{Name: "package/bin/" + input.Project.Name, Size: input.Size, Mode: binMode}); err != nil {
			return fmt.Errorf("write tar header: %w", err)
		}
		if _, err := io.Copy(tw, input.Reader); err != nil {
			return fmt.Errorf("write binary: %w", err)
		}
		if err := tw.Close(); err != nil {
			return fmt.Errorf("close tar: %w", err)
		}
		return gw.Close()
	})

	filename := fmt.Sprintf("%s-%s-%s-%s.tgz", input.Project.Name, version, npmOS, npmArch)
	return &Output{
		Reader:   r,
		Filename: filename,
		Size:     SizeUnknown,
	}, nil
}

func npmPlatform(os db.OS) string {
	switch os {
	case db.OSDarwin:
		return "darwin"
	case db.OSWindows:
		return "win32"
	default:
		return string(os)
	}
}

func npmArch(a db.Arch) string {
	switch a {
	case db.ArchAMD64:
		return "x64"
	case db.ArchARM64:
		return "arm64"
	case db.Arch386:
		return "ia32"
	default:
		return string(a)
	}
}
