package static

// Integration tests for artifact serving through the unified /file endpoint:
// raw and repackage formats, zstd passthrough, ETags, version resolution
// against a real DB + filesystem store.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

func setupIntegration(t *testing.T) (*staticHandler, *db.DB, *storage.Filesystem) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })

	store, err := storage.NewFilesystem(t.TempDir(), true)
	require.NoError(t, err)

	h := &staticHandler{DB: d, Store: store, TmpDir: t.TempDir()}
	return h, d, store
}

func withProject(r *http.Request, p *db.Project) *http.Request {
	ctx := auth.WithProject(r.Context(), p)
	return r.WithContext(ctx)
}

func TestServe_RawFormat_Success(t *testing.T) {
	t.Serial()
	h, d, store := setupIntegration(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	key, size, err := store.Put(ctx, strings.NewReader("hello-binary"))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}))

	req := httptest.NewRequest("GET", "/file?arch=amd64&fmt=raw&os=linux&project=myapp&v=1.0.0", nil)
	req = withProject(req, proj)
	rec := httptest.NewRecorder()
	h.Serve(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "hello-binary")
	assert.NotEmpty(t, rec.Header().Get("ETag"))
	assert.Equal(t, "public, max-age=31536000, immutable", rec.Header().Get("Cache-Control"))
}

func TestServe_RawFormat_ZstdPassthrough(t *testing.T) {
	t.Serial()
	h, d, store := setupIntegration(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	content := "hello-binary-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	key, size, err := store.Put(ctx, strings.NewReader(content))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}))

	// A client that accepts zstd gets the stored blob passed through untouched.
	req := httptest.NewRequest("GET", "/file?arch=amd64&debug=1&fmt=raw&os=linux&project=myapp&v=1.0.0", nil)
	req.Header.Set("Accept-Encoding", "zstd")
	req = withProject(req, proj)
	rec := httptest.NewRecorder()
	h.Serve(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "zstd", rec.Header().Get("Content-Encoding"))
	assert.Equal(t, "Accept-Encoding", rec.Header().Get("Vary"))

	body := rec.Body.Bytes()
	assert.Equal(t, fmt.Sprintf("%d", len(body)), rec.Header().Get("Content-Length"))
	// The body is a real zstd stream that decodes to the artifact: the server
	zr, err := zstd.NewReader(bytes.NewReader(body))
	require.NoError(t, err)
	defer zr.Close()
	got, err := io.ReadAll(zr)
	require.NoError(t, err)
	assert.Equal(t, content, string(got))
}

func TestServe_RawFormat_IdentityWhenZstdNotAccepted(t *testing.T) {
	t.Serial()
	h, d, store := setupIntegration(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	content := "plain-identity-binary"
	key, size, err := store.Put(ctx, strings.NewReader(content))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}))

	// A client that does not list zstd gets the decompressed bytes; Vary is still
	req := httptest.NewRequest("GET", "/file?arch=amd64&debug=1&fmt=raw&os=linux&project=myapp&v=1.0.0", nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	req = withProject(req, proj)
	rec := httptest.NewRecorder()
	h.Serve(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get("Content-Encoding"))
	assert.Equal(t, "Accept-Encoding", rec.Header().Get("Vary"))
	assert.Equal(t, content, rec.Body.String())
}

func TestServe_DockerArtifact_NotServed(t *testing.T) {
	t.Serial()
	h, d, store := setupIntegration(t)
	ctx := context.Background()

	proj := &db.Project{Name: "ollama", Versioning: db.VersioningAuto}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1", VersionNum: 1}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	key, size, err := store.Put(ctx, strings.NewReader("oci-manifest-json"))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindDocker, StorageKey: key, Size: size, SHA256: key,
	}))

	// A docker image is OCI-only; /static must not serve it as a raw download.
	req := httptest.NewRequest("GET", "/file?arch=amd64&fmt=raw&os=linux&project=ollama&v=1", nil)
	req = withProject(req, proj)
	rec := httptest.NewRecorder()
	h.Serve(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServe_ETag_NotModified(t *testing.T) {
	t.Serial()
	h, d, store := setupIntegration(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	key, size, err := store.Put(ctx, strings.NewReader("binary"))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}))

	req := httptest.NewRequest("GET", "/file?arch=amd64&fmt=raw&os=linux&project=myapp&v=1.0.0", nil)
	req = withProject(req, proj)
	rec := httptest.NewRecorder()
	h.Serve(rec, req)
	etag := rec.Header().Get("ETag")
	require.NotEmpty(t, etag)

	req2 := httptest.NewRequest("GET", "/file?arch=amd64&fmt=raw&os=linux&project=myapp&v=1.0.0", nil)
	req2 = withProject(req2, proj)
	req2.Header.Set("If-None-Match", etag)
	rec2 := httptest.NewRecorder()
	h.Serve(rec2, req2)
	assert.Equal(t, http.StatusNotModified, rec2.Code)
}

func TestServe_VersionNotFound(t *testing.T) {
	t.Serial()
	h, d, _ := setupIntegration(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	req := httptest.NewRequest("GET", "/file?arch=amd64&fmt=raw&os=linux&project=myapp&v=9.9.9", nil)
	req = withProject(req, proj)
	rec := httptest.NewRecorder()
	h.Serve(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServe_ArtifactNotFound(t *testing.T) {
	t.Serial()
	h, d, _ := setupIntegration(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	req := httptest.NewRequest("GET", "/file?arch=amd64&fmt=raw&os=linux&project=myapp&v=1.0.0", nil)
	req = withProject(req, proj)
	rec := httptest.NewRecorder()
	h.Serve(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServe_VersionResolution_StripV(t *testing.T) {
	t.Serial()
	h, d, store := setupIntegration(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "2.0.0", VersionNum: 2000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	key, size, err := store.Put(ctx, strings.NewReader("bin"))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}))

	req := httptest.NewRequest("GET", "/file?arch=amd64&fmt=raw&os=linux&project=myapp&v=v2.0.0", nil)
	req = withProject(req, proj)
	rec := httptest.NewRecorder()
	h.Serve(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestServe_VersionResolution_StripDotZeroZero(t *testing.T) {
	t.Serial()
	h, d, store := setupIntegration(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp"}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "5", VersionNum: 5}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	key, size, err := store.Put(ctx, strings.NewReader("bin"))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}))

	req := httptest.NewRequest("GET", "/file?arch=amd64&fmt=raw&os=linux&project=myapp&v=5.0.0", nil)
	req = withProject(req, proj)
	rec := httptest.NewRecorder()
	h.Serve(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestServe_AnyOSArch(t *testing.T) {
	t.Serial()
	h, d, _ := setupIntegration(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	req := httptest.NewRequest("GET", "/file?arch=any&fmt=raw&os=any&project=myapp&v=1.0.0", nil)
	req = withProject(req, proj)
	rec := httptest.NewRecorder()
	h.Serve(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code, "raw format requires an artifact, so os=any should fail")
}

func TestServe_DebugSymbolsHeader(t *testing.T) {
	t.Serial()
	h, d, store := setupIntegration(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	key, size, err := store.Put(ctx, strings.NewReader("not-an-elf"))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}))

	req := httptest.NewRequest("GET", "/file?arch=amd64&fmt=raw&os=linux&project=myapp&v=1.0.0", nil)
	req = withProject(req, proj)
	rec := httptest.NewRecorder()
	h.Serve(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	hdr := rec.Header().Get("X-Debug-Symbols")
	assert.Contains(t, []string{"available", "unavailable"}, hdr, "should indicate symbol availability")
}

func TestServe_SymbolsFormat_NoStrip(t *testing.T) {
	t.Serial()
	h, d, store := setupIntegration(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	key, size, err := store.Put(ctx, strings.NewReader("not-elf"))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}))

	req := httptest.NewRequest("GET", "/file?arch=amd64&fmt=symbols&os=linux&project=myapp&v=1.0.0", nil)
	req = withProject(req, proj)
	rec := httptest.NewRecorder()
	h.Serve(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServe_RepackageFormat(t *testing.T) {
	t.Serial()
	h, d, store := setupIntegration(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	key, size, err := store.Put(ctx, strings.NewReader("binary-data"))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}))

	RegisterRepackageFmt("tar.gz")

	req := httptest.NewRequest("GET", "/file?arch=amd64&fmt=tar.gz&os=linux&project=myapp&v=1.0.0", nil)
	req = withProject(req, proj)
	rec := httptest.NewRecorder()
	h.Serve(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.NotEmpty(t, rec.Body.Bytes())
}

// A binary artifact that is not an ELF must be served EXACTLY as uploaded, and
// the same immutable URL must return the same bytes every time.
//
// Regression: buildhost strips binaries at download time wherever strip and
// objcopy exist (only the distroless production image lacks them). Those tools
// go through BFD, which accepts PE/COFF, so a Cosmopolitan APE binary -- what
// go-toolchain ships on Linux -- was not rejected but rewritten: roughly half
// the bytes, corrupt, and different on every request. That broke `brew install`
func TestServe_NonELFBinary_ServedVerbatimAndStable(t *testing.T) {
	t.Serial()
	h, d, store := setupIntegration(t)
	ctx := context.Background()

	// A REAL PE32+ binary, which is what a Cosmopolitan APE looks like to BFD.
	ape := buildPEArtifact(t)

	proj := &db.Project{Name: "apeapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	key, size, err := store.Put(ctx, bytes.NewReader(ape))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}))

	RegisterRepackageFmt("tar.gz")

	get := func(format string) []byte {
		t.Helper()
		req := httptest.NewRequest("GET", "/file?arch=amd64&fmt="+format+"&os=linux&project=apeapp&v=1.0.0", nil)
		req = withProject(req, proj)
		rec := httptest.NewRecorder()
		h.Serve(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		return rec.Body.Bytes()
	}

	raw := get("raw")
	assert.Equal(t, ape, raw, "a non-ELF binary must be served byte-for-byte as uploaded")
	assert.Equal(t, raw, get("raw"), "repeated downloads of an immutable artifact must be identical")

	// The repackage path opens the same (optionally stripped) stream, so it
	assert.Equal(t, get("tar.gz"), get("tar.gz"), "tar.gz generation must be reproducible")
}

// buildPEArtifact returns a real PE32+ binary to stand in for a Cosmopolitan
// APE artifact -- the shape strip/objcopy accept and rewrite.
func buildPEArtifact(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(src, []byte("package main\nfunc main() {}\n"), 0o644))

	out := filepath.Join(dir, "fixture.exe")
	cmd := exec.Command("go", "build", "-o", out, src)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=windows", "GOARCH=amd64")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot build a PE fixture (no go toolchain?): %s: %s", err, b)
	}
	data, err := os.ReadFile(out)
	require.NoError(t, err)
	return data
}
