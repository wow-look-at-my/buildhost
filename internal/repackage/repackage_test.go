package repackage

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/wow-look-at-my/buildhost/internal/model"
)

var testBinary = []byte("#!/bin/sh\necho hello\n")

func makeInput() Input {
	return Input{
		Project: model.Project{
			Name:        "testapp",
			Description: "A test application",
			Homepage:    "https://example.com",
			License:     "MIT",
		},
		Release: model.Release{
			Version:    "v1.2.3",
			VersionNum: 1,
		},
		Artifact: model.Artifact{
			OS:   model.OSLinux,
			Arch: model.ArchAMD64,
			Kind: model.KindBinary,
		},
		Binary:  bytes.NewReader(testBinary),
		BaseURL: "https://builds.example.com",
	}
}

// --- Applicability tests ---

func TestTarGZApplicable(t *testing.T) {
	rp := &TarGZ{}
	for _, kind := range []model.Kind{model.KindBinary, model.KindLibrary, model.KindAssets, model.KindArchive} {
		for _, os := range []model.OS{model.OSLinux, model.OSDarwin, model.OSWindows, model.OSFreeBSD} {
			a := model.Artifact{OS: os, Kind: kind}
			if !rp.Applicable(a) {
				t.Errorf("TarGZ.Applicable(os=%s, kind=%s) = false, want true", os, kind)
			}
		}
	}
}

func TestTarXZApplicable(t *testing.T) {
	rp := &TarXZ{}
	for _, kind := range []model.Kind{model.KindBinary, model.KindLibrary, model.KindAssets, model.KindArchive} {
		for _, os := range []model.OS{model.OSLinux, model.OSDarwin, model.OSWindows, model.OSFreeBSD} {
			a := model.Artifact{OS: os, Kind: kind}
			if !rp.Applicable(a) {
				t.Errorf("TarXZ.Applicable(os=%s, kind=%s) = false, want true", os, kind)
			}
		}
	}
}

func TestTarZSTApplicable(t *testing.T) {
	rp := &TarZST{}
	for _, kind := range []model.Kind{model.KindBinary, model.KindLibrary, model.KindAssets, model.KindArchive} {
		for _, os := range []model.OS{model.OSLinux, model.OSDarwin, model.OSWindows, model.OSFreeBSD} {
			a := model.Artifact{OS: os, Kind: kind}
			if !rp.Applicable(a) {
				t.Errorf("TarZST.Applicable(os=%s, kind=%s) = false, want true", os, kind)
			}
		}
	}
}

func TestZipApplicable(t *testing.T) {
	rp := &Zip{}
	for _, kind := range []model.Kind{model.KindBinary, model.KindLibrary, model.KindAssets, model.KindArchive} {
		for _, os := range []model.OS{model.OSLinux, model.OSDarwin, model.OSWindows, model.OSFreeBSD} {
			a := model.Artifact{OS: os, Kind: kind}
			if !rp.Applicable(a) {
				t.Errorf("Zip.Applicable(os=%s, kind=%s) = false, want true", os, kind)
			}
		}
	}
}

func TestDebApplicable(t *testing.T) {
	rp := &Deb{}

	linuxArtifact := model.Artifact{OS: model.OSLinux, Kind: model.KindBinary}
	if !rp.Applicable(linuxArtifact) {
		t.Error("Deb.Applicable(linux) = false, want true")
	}

	for _, os := range []model.OS{model.OSDarwin, model.OSWindows, model.OSFreeBSD} {
		a := model.Artifact{OS: os, Kind: model.KindBinary}
		if rp.Applicable(a) {
			t.Errorf("Deb.Applicable(os=%s) = true, want false", os)
		}
	}
}

func TestBrewApplicable(t *testing.T) {
	rp := &Brew{}

	// Linux and Darwin binaries are applicable
	for _, os := range []model.OS{model.OSLinux, model.OSDarwin} {
		a := model.Artifact{OS: os, Kind: model.KindBinary}
		if !rp.Applicable(a) {
			t.Errorf("Brew.Applicable(os=%s, kind=binary) = false, want true", os)
		}
	}

	// Assets kind is not applicable even on linux/darwin
	for _, os := range []model.OS{model.OSLinux, model.OSDarwin} {
		a := model.Artifact{OS: os, Kind: model.KindAssets}
		if rp.Applicable(a) {
			t.Errorf("Brew.Applicable(os=%s, kind=assets) = true, want false", os)
		}
	}

	// Windows and FreeBSD are not applicable
	for _, os := range []model.OS{model.OSWindows, model.OSFreeBSD} {
		a := model.Artifact{OS: os, Kind: model.KindBinary}
		if rp.Applicable(a) {
			t.Errorf("Brew.Applicable(os=%s) = true, want false", os)
		}
	}
}

func TestNPMApplicable(t *testing.T) {
	rp := &NPM{}

	// Binary, assets, archive are applicable
	for _, kind := range []model.Kind{model.KindBinary, model.KindAssets, model.KindArchive} {
		a := model.Artifact{OS: model.OSLinux, Kind: kind}
		if !rp.Applicable(a) {
			t.Errorf("NPM.Applicable(kind=%s) = false, want true", kind)
		}
	}

	// Library is not applicable
	a := model.Artifact{OS: model.OSLinux, Kind: model.KindLibrary}
	if rp.Applicable(a) {
		t.Error("NPM.Applicable(kind=library) = true, want false")
	}
}

func TestOCIApplicable(t *testing.T) {
	rp := &OCI{}

	// Binary and archive are applicable
	for _, kind := range []model.Kind{model.KindBinary, model.KindArchive} {
		a := model.Artifact{OS: model.OSLinux, Kind: kind}
		if !rp.Applicable(a) {
			t.Errorf("OCI.Applicable(kind=%s) = false, want true", kind)
		}
	}

	// Library and assets are not applicable
	for _, kind := range []model.Kind{model.KindLibrary, model.KindAssets} {
		a := model.Artifact{OS: model.OSLinux, Kind: kind}
		if rp.Applicable(a) {
			t.Errorf("OCI.Applicable(kind=%s) = true, want false", kind)
		}
	}
}

// --- Repackage tests ---

func TestTarGZRepackage(t *testing.T) {
	rp := &TarGZ{}
	input := makeInput()
	ctx := context.Background()

	output, err := rp.Repackage(ctx, input)
	if err != nil {
		t.Fatalf("TarGZ.Repackage: %v", err)
	}

	if output.Size == 0 {
		t.Fatal("TarGZ output has zero size")
	}
	if !strings.HasSuffix(output.Filename, ".tar.gz") {
		t.Errorf("TarGZ filename = %q, want .tar.gz suffix", output.Filename)
	}

	// Verify it contains the expected file
	data, err := io.ReadAll(output.Reader)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gzip open: %v", err)
	}
	tr := tar.NewReader(gr)
	hdr, err := tr.Next()
	if err != nil {
		t.Fatalf("tar next: %v", err)
	}
	if hdr.Name != "testapp" {
		t.Errorf("tar entry name = %q, want %q", hdr.Name, "testapp")
	}
	contents, _ := io.ReadAll(tr)
	if !bytes.Equal(contents, testBinary) {
		t.Error("tar entry contents do not match input binary")
	}
}

func TestTarXZRepackage(t *testing.T) {
	rp := &TarXZ{}
	input := makeInput()
	ctx := context.Background()

	output, err := rp.Repackage(ctx, input)
	if err != nil {
		t.Fatalf("TarXZ.Repackage: %v", err)
	}

	if output.Size == 0 {
		t.Fatal("TarXZ output has zero size")
	}
	if !strings.HasSuffix(output.Filename, ".tar.xz") {
		t.Errorf("TarXZ filename = %q, want .tar.xz suffix", output.Filename)
	}

	// Verify by decompressing with xz and reading tar
	data, err := io.ReadAll(output.Reader)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	// The xz package is used in production; here just verify non-empty output
	// with the correct xz magic bytes (0xFD, '7', 'z', 'X', 'Z', 0x00)
	if len(data) < 6 {
		t.Fatal("TarXZ output too small")
	}
	xzMagic := []byte{0xFD, '7', 'z', 'X', 'Z', 0x00}
	if !bytes.Equal(data[:6], xzMagic) {
		t.Errorf("TarXZ output does not start with xz magic bytes, got %x", data[:6])
	}
}

func TestTarZSTRepackage(t *testing.T) {
	rp := &TarZST{}
	input := makeInput()
	ctx := context.Background()

	output, err := rp.Repackage(ctx, input)
	if err != nil {
		t.Fatalf("TarZST.Repackage: %v", err)
	}

	if output.Size == 0 {
		t.Fatal("TarZST output has zero size")
	}
	if !strings.HasSuffix(output.Filename, ".tar.zst") {
		t.Errorf("TarZST filename = %q, want .tar.zst suffix", output.Filename)
	}

	// Verify zstd magic number: 0x28 0xB5 0x2F 0xFD
	data, err := io.ReadAll(output.Reader)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if len(data) < 4 {
		t.Fatal("TarZST output too small")
	}
	zstMagic := []byte{0x28, 0xB5, 0x2F, 0xFD}
	if !bytes.Equal(data[:4], zstMagic) {
		t.Errorf("TarZST output does not start with zstd magic bytes, got %x", data[:4])
	}
}

func TestZipRepackage(t *testing.T) {
	rp := &Zip{}
	input := makeInput()
	ctx := context.Background()

	output, err := rp.Repackage(ctx, input)
	if err != nil {
		t.Fatalf("Zip.Repackage: %v", err)
	}

	if output.Size == 0 {
		t.Fatal("Zip output has zero size")
	}
	if !strings.HasSuffix(output.Filename, ".zip") {
		t.Errorf("Zip filename = %q, want .zip suffix", output.Filename)
	}

	// Verify by opening with archive/zip
	data, err := io.ReadAll(output.Reader)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip open: %v", err)
	}
	if len(zr.File) != 1 {
		t.Fatalf("zip has %d files, want 1", len(zr.File))
	}
	if zr.File[0].Name != "testapp" {
		t.Errorf("zip entry name = %q, want %q", zr.File[0].Name, "testapp")
	}
	rc, err := zr.File[0].Open()
	if err != nil {
		t.Fatalf("open zip entry: %v", err)
	}
	contents, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(contents, testBinary) {
		t.Error("zip entry contents do not match input binary")
	}
}

func TestDebRepackage(t *testing.T) {
	rp := &Deb{}
	input := makeInput()
	ctx := context.Background()

	output, err := rp.Repackage(ctx, input)
	if err != nil {
		t.Fatalf("Deb.Repackage: %v", err)
	}

	if output.Size == 0 {
		t.Fatal("Deb output has zero size")
	}
	if !strings.HasSuffix(output.Filename, ".deb") {
		t.Errorf("Deb filename = %q, want .deb suffix", output.Filename)
	}

	// Verify ar archive magic
	data, err := io.ReadAll(output.Reader)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	arMagic := "!<arch>\n"
	if len(data) < len(arMagic) {
		t.Fatalf("Deb output too small (%d bytes)", len(data))
	}
	if string(data[:len(arMagic)]) != arMagic {
		t.Errorf("Deb output does not start with ar magic, got %q", string(data[:len(arMagic)]))
	}
}

func TestBrewRepackage(t *testing.T) {
	rp := &Brew{}
	input := makeInput()
	ctx := context.Background()

	output, err := rp.Repackage(ctx, input)
	if err != nil {
		t.Fatalf("Brew.Repackage: %v", err)
	}

	if output.Size == 0 {
		t.Fatal("Brew output has zero size")
	}
	if !strings.HasSuffix(output.Filename, ".rb") {
		t.Errorf("Brew filename = %q, want .rb suffix", output.Filename)
	}

	data, err := io.ReadAll(output.Reader)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}

	body := string(data)
	if !strings.Contains(body, "class") {
		t.Error("Brew output does not contain 'class'")
	}
	if !strings.Contains(body, "Formula") {
		t.Error("Brew output does not contain 'Formula'")
	}
}

func TestNPMRepackage(t *testing.T) {
	rp := &NPM{}
	input := makeInput()
	ctx := context.Background()

	output, err := rp.Repackage(ctx, input)
	if err != nil {
		t.Fatalf("NPM.Repackage: %v", err)
	}

	if output.Size == 0 {
		t.Fatal("NPM output has zero size")
	}
	if !strings.HasSuffix(output.Filename, ".tgz") {
		t.Errorf("NPM filename = %q, want .tgz suffix", output.Filename)
	}

	// Verify it is a valid gzipped tar containing package/package.json
	data, err := io.ReadAll(output.Reader)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gzip open: %v", err)
	}
	tr := tar.NewReader(gr)

	foundPackageJSON := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		if hdr.Name == "package/package.json" {
			foundPackageJSON = true
		}
	}
	if !foundPackageJSON {
		t.Error("NPM output does not contain package/package.json")
	}
}

func TestOCIRepackage(t *testing.T) {
	rp := &OCI{}
	input := makeInput()
	ctx := context.Background()

	output, err := rp.Repackage(ctx, input)
	if err != nil {
		t.Fatalf("OCI.Repackage: %v", err)
	}

	if output.Size == 0 {
		t.Fatal("OCI output has zero size")
	}
	if !strings.HasSuffix(output.Filename, "-oci.json") {
		t.Errorf("OCI filename = %q, want -oci.json suffix", output.Filename)
	}

	data, err := io.ReadAll(output.Reader)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}

	body := string(data)
	if !strings.Contains(body, "schemaVersion") {
		t.Error("OCI output does not contain 'schemaVersion'")
	}
	if !strings.Contains(body, "application/vnd.oci.image.manifest.v1+json") {
		t.Error("OCI output does not contain OCI media type")
	}

	// Verify metadata
	if output.Metadata == nil {
		t.Fatal("OCI output has nil metadata")
	}
	if output.Metadata["os"] != "linux" {
		t.Errorf("OCI metadata os = %q, want %q", output.Metadata["os"], "linux")
	}
	if output.Metadata["arch"] != "amd64" {
		t.Errorf("OCI metadata arch = %q, want %q", output.Metadata["arch"], "amd64")
	}
}

// --- Format tests ---

func TestFormats(t *testing.T) {
	tests := []struct {
		rp     Repackager
		format Format
	}{
		{&TarGZ{}, FormatTarGZ},
		{&TarXZ{}, FormatTarXZ},
		{&TarZST{}, FormatTarZST},
		{&Zip{}, FormatZip},
		{&Deb{}, FormatDeb},
		{&Brew{}, FormatBrew},
		{&NPM{}, FormatNPM},
		{&OCI{}, FormatOCI},
	}

	for _, tt := range tests {
		if got := tt.rp.Format(); got != tt.format {
			t.Errorf("%T.Format() = %q, want %q", tt.rp, got, tt.format)
		}
	}
}
