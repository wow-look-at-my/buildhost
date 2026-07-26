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

	// The deb materialization of the packaging-agnostic create_service
	// project setting. Only for the binary kind (the unit's ExecStart is the
	// /usr/bin install path); with the flag off both members are
	// byte-identical to before the setting existed.
	withService := input.Project.CreateService && input.Artifact.Kind == db.KindBinary

	controlEntries := []tarEntry{{
		Name: "./control",
		Data: []byte(controlContent),
		Mode: 0o644,
	}}
	if withService {
		controlEntries = append(controlEntries,
			tarEntry{Name: "./postinst", Data: []byte(debPostinst(pkgName)), Mode: 0o755},
			tarEntry{Name: "./prerm", Data: []byte(debPrerm(pkgName)), Mode: 0o755},
		)
	}
	controlTar, err := buildTarGZ(controlEntries)
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

	// A Cosmopolitan APE binary cannot be run from a root-owned /usr/bin entry
	// by a non-root user -- it assimilates itself on first run and needs the
	// write bit. Install it under /usr/lib and put a launcher on $PATH that
	// maintains a per-user writable copy (see debAPELauncher). Detection is a
	// byte check on the artifact's own header; nothing is executed, and a
	// non-APE binary keeps the previous layout byte-for-byte.
	artifactReader := input.Reader
	var launcher []byte
	if input.Artifact.Kind == db.KindBinary {
		isAPE, rest, err := peekAPE(artifactReader)
		if err != nil {
			return nil, fmt.Errorf("inspect artifact: %w", err)
		}
		artifactReader = rest
		if isAPE {
			installDir = fmt.Sprintf("/usr/lib/%s/", pkgName)
			launcher = []byte(debAPELauncher(pkgName, version))
		}
	}

	var extraEntries []tarEntry
	if launcher != nil {
		extraEntries = append(extraEntries, tarEntry{
			Name: "./usr/bin/" + pkgName,
			Data: launcher,
			Mode: 0o755,
		})
	}
	if withService {
		extraEntries = append(extraEntries, tarEntry{
			Name: "." + DebServiceUnitPath(pkgName),
			Data: []byte(DebServiceUnit(pkgName, firstNonEmpty(input.Project.Description, pkgName))),
			Mode: 0o644,
		})
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
	// dpkg creates no leading directories of its own, so a package installing
	// outside /usr/bin must carry its directory entry (see tarEntry.Dir).
	var preEntries []tarEntry
	if installDir != "/usr/bin/" {
		preEntries = append(preEntries, tarEntry{Name: "." + installDir, Mode: 0o755, Dir: true})
	}

	dataLen, err := streamDebData(dataTmp, artifactReader, "."+installDir+fileName, input.Size, mode, preEntries, extraEntries)
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

// streamDebData writes the data tar.gz (the artifact at name, then any extra
// small entries such as the create_service systemd unit) to f and returns the
// number of compressed bytes written. An empty extra list produces the exact
// single-entry member of before, so flag-off debs stay byte-identical.
func streamDebData(f *os.File, r io.Reader, name string, size, mode int64, pre, extra []tarEntry) (int64, error) {
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	for _, e := range pre {
		if err := tw.WriteHeader(debTarHeader(e)); err != nil {
			return 0, fmt.Errorf("write data tar header %q: %w", e.Name, err)
		}
		if !e.Dir {
			if _, err := tw.Write(e.Data); err != nil {
				return 0, fmt.Errorf("write data entry %q: %w", e.Name, err)
			}
		}
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Size: size, Mode: mode}); err != nil {
		return 0, fmt.Errorf("write data tar header: %w", err)
	}
	if _, err := io.Copy(tw, r); err != nil {
		return 0, fmt.Errorf("write data: %w", err)
	}
	for _, e := range extra {
		if err := tw.WriteHeader(debTarHeader(e)); err != nil {
			return 0, fmt.Errorf("write data tar header %q: %w", e.Name, err)
		}
		if !e.Dir {
			if _, err := tw.Write(e.Data); err != nil {
				return 0, fmt.Errorf("write data entry %q: %w", e.Name, err)
			}
		}
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

// DebServiceUnitPath is where the create_service systemd USER unit lands in
// the installed filesystem (and, "."-prefixed, in the deb's data tar).
func DebServiceUnitPath(pkgName string) string {
	return "/usr/lib/systemd/user/" + pkgName + ".service"
}

// DebServiceUnit renders the systemd USER unit shipped when the project
// declares create_service. A user unit -- not a system one -- because the
// declared binaries are per-user (often GUI) apps: it is ordered after and
// bound to graphical-session.target (systemd.special(7): graphical user
// services carry PartOf=graphical-session.target so they stop with the
// session, and enablement attaches them via
// WantedBy=graphical-session.target), and Restart=on-failure is the
// crash-only policy (restart on non-zero exit or signal, stay stopped on a
// clean exit -- the brew materialization's KeepAlive {SuccessfulExit:false}
// twin). The package's postinst auto-enables it at install (debPostinst);
// `systemctl --user enable --now <pkg>` remains the manual override.
func DebServiceUnit(pkgName, description string) string {
	return fmt.Sprintf(`[Unit]
Description=%s
After=graphical-session.target
PartOf=graphical-session.target

[Service]
ExecStart=/usr/bin/%s
Restart=on-failure

[Install]
WantedBy=graphical-session.target
`, sanitizeControlField(description), pkgName)
}

type tarEntry struct {
	Name string
	Data []byte
	Mode int64
	// Dir marks a directory entry. dpkg does not create leading directories
	// implicitly: a data.tar that names ./usr/lib/<pkg>/<file> without also
	// carrying ./usr/lib/<pkg>/ fails to unpack with "unable to create
	// ... No such file or directory". Every install path below /usr/bin needs
	// its own directory entry, written before the files inside it.
	Dir bool
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

// debPostinst is the maintainer script that sets the create_service unit up
// automatically at install. `systemctl --global enable` is pure symlink
// manipulation under /etc/systemd/user (no running manager needed; works in
// chroots/containers), attaching the unit to every user's
// graphical-session.target -- so the service starts at each user's NEXT
// graphical login. The immediate start is strictly best-effort for the
// sudo-invoking user's already-running session (`systemctl --user -M user@`
// needs systemd >= 248 and a live user session; apt runs as root, which has
// no user manager of its own). Every action is guarded and ||-true'd: a
// missing systemctl or a failed start must never leave the package
// unconfigured.
func debPostinst(pkgName string) string {
	return fmt.Sprintf(`#!/bin/sh
set -e
if [ "$1" = "configure" ] && command -v systemctl >/dev/null 2>&1; then
    systemctl --global enable %s.service >/dev/null 2>&1 || true
    if [ -n "${SUDO_USER:-}" ] && [ "${SUDO_USER}" != "root" ]; then
        systemctl --user -M "${SUDO_USER}@" start %s.service >/dev/null 2>&1 || true
    fi
fi
`, pkgName, pkgName)
}

// debPrerm undoes the postinst enablement when the package is removed. The
// unit file itself is removed by dpkg with the package; running instances end
// with the owning session (PartOf=graphical-session.target) or at its next
// logout -- prerm deliberately does not reach into user sessions to kill them.
func debPrerm(pkgName string) string {
	return fmt.Sprintf(`#!/bin/sh
set -e
if [ "$1" = "remove" ] && command -v systemctl >/dev/null 2>&1; then
    systemctl --global disable %s.service >/dev/null 2>&1 || true
fi
`, pkgName)
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

// debTarHeader renders a tarEntry as a tar header, distinguishing directories
// (no payload, TypeDir) from regular files.
func debTarHeader(e tarEntry) *tar.Header {
	if e.Dir {
		return &tar.Header{Name: e.Name, Typeflag: tar.TypeDir, Mode: e.Mode}
	}
	return &tar.Header{Name: e.Name, Size: int64(len(e.Data)), Mode: e.Mode}
}
