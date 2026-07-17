package repackage

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/db"
)

func init() { Register(&Deb{}) }

type Deb struct{}

func (d *Deb) Format() Format { return FormatDeb }

func (d *Deb) Applicable(a db.Artifact) bool {
	return a.OS == db.OSLinux && !a.Kind.ServedViaDockerOnly()
}

func (d *Deb) Repackage(_ context.Context, input Input) (*Output, error) {
	arch := debArch(input.Artifact.Arch)
	version := strings.TrimPrefix(input.Release.Version, "v")
	if version == "" {
		version = fmt.Sprintf("%d", input.Release.VersionNum)
	}

	// A buildhost project name may be slash-namespaced (e.g. "myrepo/server"),
	// which is not a legal Debian package name. Fold it to a valid one (see
	// DebPackageName); this is also the installed binary's name on $PATH.
	pkgName := DebPackageName(input.Project.Name)

	installDir := "/usr/bin/"
	switch input.Artifact.Kind {
	case db.KindLibrary:
		installDir = fmt.Sprintf("/usr/lib/%s/", pkgName)
	case db.KindAssets:
		installDir = fmt.Sprintf("/usr/share/%s/", pkgName)
	case db.KindArchive:
		installDir = fmt.Sprintf("/usr/share/%s/", pkgName)
	}

	controlContent := fmt.Sprintf(
		"Package: %s\nVersion: %s\nArchitecture: %s\nMaintainer: %s\nDescription: %s\nSection: utils\nPriority: optional\n",
		pkgName, version, arch,
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

	fileName := pkgName
	if input.Artifact.Kind == db.KindLibrary {
		fileName = input.Artifact.Filename
		if fileName == "" {
			fileName = "lib" + pkgName + ".so"
		}
	}

	mode := int64(0o644)
	if input.Artifact.Kind == db.KindBinary {
		mode = 0o755
	}

	// The ar container needs each member's exact byte length in its header, before the
	// body. The data.tar.gz member's compressed length isn't known until it's produced,
	// so stream the artifact -> tar -> gzip into a temp file (the compressed member, far
	// smaller than the raw input -- the decompressed input never lands in memory) and
	// stat it for the length.
	dataTmp, err := os.CreateTemp(input.TmpDir, "deb-data-*")
	if err != nil {
		return nil, fmt.Errorf("create deb temp: %w", err)
	}
	dataLen, err := streamDebData(dataTmp, input.Reader, "."+installDir+fileName, input.Size, mode)
	if err != nil {
		dataTmp.Close()
		os.Remove(dataTmp.Name())
		return nil, err
	}

	debBinary := []byte("2.0\n")
	filename := fmt.Sprintf("%s_%s_%s.deb", pkgName, version, arch)

	r := streamPipe(func(w io.Writer) error {
		defer func() {
			dataTmp.Close()
			os.Remove(dataTmp.Name())
		}()
		if _, err := io.WriteString(w, "!<arch>\n"); err != nil {
			return err
		}
		if err := writeArMember(w, "debian-binary", bytes.NewReader(debBinary), int64(len(debBinary))); err != nil {
			return err
		}
		if err := writeArMember(w, "control.tar.gz", bytes.NewReader(controlTar), int64(len(controlTar))); err != nil {
			return err
		}
		if _, err := dataTmp.Seek(0, io.SeekStart); err != nil {
			return err
		}
		return writeArMember(w, "data.tar.gz", dataTmp, dataLen)
	})

	return &Output{
		Reader:   r,
		Filename: filename,
		Size:     SizeUnknown,
	}, nil
}

// streamDebData writes a single-entry tar.gz (the artifact, at name) to f and returns the
// number of compressed bytes written.
func streamDebData(f *os.File, r io.Reader, name string, size, mode int64) (int64, error) {
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{Name: name, Size: size, Mode: mode}); err != nil {
		return 0, fmt.Errorf("write data tar header: %w", err)
	}
	if _, err := io.Copy(tw, r); err != nil {
		return 0, fmt.Errorf("write data: %w", err)
	}
	if err := tw.Close(); err != nil {
		return 0, err
	}
	if err := gw.Close(); err != nil {
		return 0, err
	}
	fi, err := f.Stat()
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
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

// writeArMember writes one ar member: the fixed 60-byte header (with the body size),
// the body streamed from body, and a newline pad when the body length is odd.
func writeArMember(w io.Writer, name string, body io.Reader, size int64) error {
	header := fmt.Sprintf("%-16s%-12d%-6d%-6d%-8s%-10d`\n",
		name, 0, 0, 0, "100644", size)
	if _, err := io.WriteString(w, header); err != nil {
		return err
	}
	if _, err := io.Copy(w, body); err != nil {
		return err
	}
	if size%2 != 0 {
		if _, err := io.WriteString(w, "\n"); err != nil {
			return err
		}
	}
	return nil
}

func debArch(a db.Arch) string {
	switch a {
	case db.ArchAMD64:
		return "amd64"
	case db.ArchARM64:
		return "arm64"
	case db.Arch386:
		return "i386"
	case db.ArchARM:
		return "armhf"
	default:
		return string(a)
	}
}

// DebPackageName converts a buildhost project name into a valid Debian package
// name. Project names may be slash-namespaced (e.g. "myrepo/server") and may
// contain underscores; neither '/' nor '_' is permitted in a Debian package
// name (Policy 5.6.7 allows only lower-case letters, digits, '+', '-' and '.'),
// so both are folded to '-'. buildhost project names are already validated to be
// lower-case and to start with an alphanumeric, so the result always satisfies
// the package-name grammar. A plain single-segment name (no '/' or '_') is
// returned unchanged, so existing packages keep their names. The same value is
// used for the Packages index, the deb's control Package field, the pool
// filename, and the installed binary, so apt and dpkg always agree.
func DebPackageName(project string) string {
	return strings.NewReplacer("/", "-", "_", "-").Replace(project)
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
