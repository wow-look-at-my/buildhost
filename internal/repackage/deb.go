package repackage

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/model"
)

type Deb struct{}

func (d *Deb) Format() Format { return FormatDeb }

func (d *Deb) Applicable(a model.Artifact) bool {
	return a.OS == model.OSLinux
}

func (d *Deb) Repackage(_ context.Context, input Input) (*Output, error) {
	data, err := io.ReadAll(input.Binary)
	if err != nil {
		return nil, fmt.Errorf("read binary: %w", err)
	}

	arch := debArch(input.Artifact.Arch)
	version := strings.TrimPrefix(input.Release.Version, "v")
	if version == "" {
		version = fmt.Sprintf("%d", input.Release.VersionNum)
	}

	installDir := "/usr/bin/"
	switch input.Artifact.Kind {
	case model.KindLibrary:
		installDir = fmt.Sprintf("/usr/lib/%s/", input.Project.Name)
	case model.KindAssets:
		installDir = fmt.Sprintf("/usr/share/%s/", input.Project.Name)
	case model.KindArchive:
		installDir = fmt.Sprintf("/usr/share/%s/", input.Project.Name)
	}

	controlContent := fmt.Sprintf(
		"Package: %s\nVersion: %s\nArchitecture: %s\nMaintainer: %s\nDescription: %s\nSection: utils\nPriority: optional\n",
		sanitizeControlField(input.Project.Name), version, arch,
		sanitizeControlField(firstNonEmpty(input.Project.Homepage, "unknown")),
		sanitizeControlField(firstNonEmpty(input.Project.Description, input.Project.Name)))

	controlTar, err := buildTarGZ([]tarEntry{{
		Name: "./control",
		Data: []byte(controlContent),
		Mode: 0o644,
	}})
	if err != nil {
		return nil, fmt.Errorf("build control.tar.gz: %w", err)
	}

	fileName := input.Project.Name
	if input.Artifact.Kind == model.KindLibrary {
		fileName = input.Artifact.Filename
		if fileName == "" {
			fileName = "lib" + input.Project.Name + ".so"
		}
	}

	mode := int64(0o644)
	if input.Artifact.Kind == model.KindBinary {
		mode = 0o755
	}

	dataTar, err := buildTarGZ([]tarEntry{{
		Name: "." + installDir + fileName,
		Data: data,
		Mode: mode,
	}})
	if err != nil {
		return nil, fmt.Errorf("build data.tar.gz: %w", err)
	}

	debBinary := []byte("2.0\n")

	var buf bytes.Buffer
	writeArEntry(&buf, "debian-binary", debBinary)
	writeArEntry(&buf, "control.tar.gz", controlTar)
	writeArEntry(&buf, "data.tar.gz", dataTar)

	arBuf := bytes.NewBuffer(nil)
	arBuf.WriteString("!<arch>\n")
	arBuf.Write(buf.Bytes())

	filename := fmt.Sprintf("%s_%s_%s.deb", input.Project.Name, version, arch)
	return &Output{
		Reader:   arBuf,
		Filename: filename,
		Size:     int64(arBuf.Len()),
	}, nil
}

type tarEntry struct {
	Name string
	Data []byte
	Mode int64
}

func buildTarGZ(entries []tarEntry) ([]byte, error) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	for _, e := range entries {
		if err := tw.WriteHeader(&tar.Header{
			Name: e.Name,
			Size: int64(len(e.Data)),
			Mode: e.Mode,
		}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(e.Data); err != nil {
			return nil, err
		}
	}

	tw.Close()
	gw.Close()
	return buf.Bytes(), nil
}

func writeArEntry(buf *bytes.Buffer, name string, data []byte) {
	header := fmt.Sprintf("%-16s%-12d%-6d%-6d%-8s%-10d`\n",
		name, 0, 0, 0, "100644", len(data))
	buf.WriteString(header)
	buf.Write(data)
	if len(data)%2 != 0 {
		buf.WriteByte('\n')
	}
}

func debArch(a model.Arch) string {
	switch a {
	case model.ArchAMD64:
		return "amd64"
	case model.ArchARM64:
		return "arm64"
	case model.Arch386:
		return "i386"
	case model.ArchARM:
		return "armhf"
	default:
		return string(a)
	}
}

func sanitizeControlField(s string) string {
	return strings.NewReplacer("\n", " ", "\r", " ").Replace(s)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

