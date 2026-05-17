package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wow-look-at-my/buildhost/internal/model"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestUploadArtifact_Success(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &model.Project{Name: "artproj", Versioning: model.VersioningSemver}
	require.NoError(t, h.DB.CreateProject(ctx, proj))
	rel := &model.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, h.DB.CreateRelease(ctx, rel))

	req := httptest.NewRequest("POST", "/api/projects/artproj/releases/1.0.0/artifacts/linux/amd64", strings.NewReader("binary-data"))
	req.SetPathValue("project", "artproj")
	req.SetPathValue("version", "1.0.0")
	req.SetPathValue("os", "linux")
	req.SetPathValue("arch", "amd64")
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.UploadArtifact(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	var a model.Artifact
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &a))
	assert.Equal(t, model.OSLinux, a.OS)
	assert.Equal(t, model.ArchAMD64, a.Arch)
	assert.Equal(t, model.KindBinary, a.Kind)
	assert.Greater(t, a.Size, int64(0))
}

func TestUploadArtifact_NoAuth(t *testing.T) {
	h := setupTestHandler(t)

	req := httptest.NewRequest("POST", "/api/projects/p/releases/1/artifacts/linux/amd64", strings.NewReader("x"))
	req.SetPathValue("project", "p")
	req.SetPathValue("version", "1")
	req.SetPathValue("os", "linux")
	req.SetPathValue("arch", "amd64")
	rec := httptest.NewRecorder()
	h.UploadArtifact(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestUploadArtifact_ProjectNotFound(t *testing.T) {
	h := setupTestHandler(t)

	req := httptest.NewRequest("POST", "/api/projects/missing/releases/1.0.0/artifacts/linux/amd64", strings.NewReader("x"))
	req.SetPathValue("project", "missing")
	req.SetPathValue("version", "1.0.0")
	req.SetPathValue("os", "linux")
	req.SetPathValue("arch", "amd64")
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.UploadArtifact(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestUploadArtifact_ReleaseNotFound(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &model.Project{Name: "rnf", Versioning: model.VersioningSemver}
	require.NoError(t, h.DB.CreateProject(ctx, proj))

	req := httptest.NewRequest("POST", "/api/projects/rnf/releases/9.9.9/artifacts/linux/amd64", strings.NewReader("x"))
	req.SetPathValue("project", "rnf")
	req.SetPathValue("version", "9.9.9")
	req.SetPathValue("os", "linux")
	req.SetPathValue("arch", "amd64")
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.UploadArtifact(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestUploadArtifact_InvalidOS(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &model.Project{Name: "ostest", Versioning: model.VersioningSemver}
	require.NoError(t, h.DB.CreateProject(ctx, proj))
	rel := &model.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, h.DB.CreateRelease(ctx, rel))

	req := httptest.NewRequest("POST", "/api/projects/ostest/releases/1.0.0/artifacts/bados/amd64", strings.NewReader("x"))
	req.SetPathValue("project", "ostest")
	req.SetPathValue("version", "1.0.0")
	req.SetPathValue("os", "bados")
	req.SetPathValue("arch", "amd64")
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.UploadArtifact(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid os")
}

func TestUploadArtifact_InvalidArch(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &model.Project{Name: "archtest", Versioning: model.VersioningSemver}
	require.NoError(t, h.DB.CreateProject(ctx, proj))
	rel := &model.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, h.DB.CreateRelease(ctx, rel))

	req := httptest.NewRequest("POST", "/api/projects/archtest/releases/1.0.0/artifacts/linux/badarch", strings.NewReader("x"))
	req.SetPathValue("project", "archtest")
	req.SetPathValue("version", "1.0.0")
	req.SetPathValue("os", "linux")
	req.SetPathValue("arch", "badarch")
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.UploadArtifact(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid arch")
}

func TestUploadArtifact_InvalidKind(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &model.Project{Name: "kindtest", Versioning: model.VersioningSemver}
	require.NoError(t, h.DB.CreateProject(ctx, proj))
	rel := &model.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, h.DB.CreateRelease(ctx, rel))

	req := httptest.NewRequest("POST", "/api/projects/kindtest/releases/1.0.0/artifacts/linux/amd64?kind=badkind", strings.NewReader("x"))
	req.SetPathValue("project", "kindtest")
	req.SetPathValue("version", "1.0.0")
	req.SetPathValue("os", "linux")
	req.SetPathValue("arch", "amd64")
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.UploadArtifact(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid kind")
}

func TestUploadArtifact_PublishedRelease(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &model.Project{Name: "pubtest", Versioning: model.VersioningSemver}
	require.NoError(t, h.DB.CreateProject(ctx, proj))
	rel := &model.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, h.DB.CreateRelease(ctx, rel))
	require.NoError(t, h.DB.PublishRelease(ctx, rel.ID))

	req := httptest.NewRequest("POST", "/api/projects/pubtest/releases/1.0.0/artifacts/linux/amd64", strings.NewReader("x"))
	req.SetPathValue("project", "pubtest")
	req.SetPathValue("version", "1.0.0")
	req.SetPathValue("os", "linux")
	req.SetPathValue("arch", "amd64")
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.UploadArtifact(rec, req)

	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "already published")
}

func TestUploadArtifact_KindFromHeader(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &model.Project{Name: "hdrtest", Versioning: model.VersioningSemver}
	require.NoError(t, h.DB.CreateProject(ctx, proj))
	rel := &model.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, h.DB.CreateRelease(ctx, rel))

	req := httptest.NewRequest("POST", "/api/projects/hdrtest/releases/1.0.0/artifacts/linux/amd64", strings.NewReader("lib-data"))
	req.SetPathValue("project", "hdrtest")
	req.SetPathValue("version", "1.0.0")
	req.SetPathValue("os", "linux")
	req.SetPathValue("arch", "amd64")
	req.Header.Set("X-Artifact-Kind", "library")
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.UploadArtifact(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	var a model.Artifact
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &a))
	assert.Equal(t, model.KindLibrary, a.Kind)
}

func TestUploadArtifact_DuplicateOSArch(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &model.Project{Name: "duptest", Versioning: model.VersioningSemver}
	require.NoError(t, h.DB.CreateProject(ctx, proj))
	rel := &model.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, h.DB.CreateRelease(ctx, rel))

	key, size, err := h.Store.Put(ctx, strings.NewReader("first"))
	require.NoError(t, err)
	require.NoError(t, h.DB.CreateArtifact(ctx, &model.Artifact{
		ReleaseID: rel.ID, OS: model.OSLinux, Arch: model.ArchAMD64,
		Kind: model.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}))

	req := httptest.NewRequest("POST", "/api/projects/duptest/releases/1.0.0/artifacts/linux/amd64", strings.NewReader("second"))
	req.SetPathValue("project", "duptest")
	req.SetPathValue("version", "1.0.0")
	req.SetPathValue("os", "linux")
	req.SetPathValue("arch", "amd64")
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.UploadArtifact(rec, req)

	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "artifact already exists")
}

// --- Publish tests ---

func TestPublishRelease_Success(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &model.Project{Name: "pubrel", Versioning: model.VersioningSemver}
	require.NoError(t, h.DB.CreateProject(ctx, proj))
	rel := &model.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, h.DB.CreateRelease(ctx, rel))

	key, size, err := h.Store.Put(ctx, strings.NewReader("binary"))
	require.NoError(t, err)
	require.NoError(t, h.DB.CreateArtifact(ctx, &model.Artifact{
		ReleaseID: rel.ID, OS: model.OSLinux, Arch: model.ArchAMD64,
		Kind: model.KindAssets, StorageKey: key, Size: size, SHA256: key,
	}))

	req := httptest.NewRequest("POST", "/api/projects/pubrel/releases/1.0.0/publish", nil)
	req.SetPathValue("project", "pubrel")
	req.SetPathValue("version", "1.0.0")
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.PublishRelease(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var got model.Release
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.True(t, got.Published)
}

func TestPublishRelease_NoAuth(t *testing.T) {
	h := setupTestHandler(t)

	req := httptest.NewRequest("POST", "/api/projects/p/releases/1.0.0/publish", nil)
	req.SetPathValue("project", "p")
	req.SetPathValue("version", "1.0.0")
	rec := httptest.NewRecorder()
	h.PublishRelease(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestPublishRelease_ProjectNotFound(t *testing.T) {
	h := setupTestHandler(t)

	req := httptest.NewRequest("POST", "/api/projects/missing/releases/1.0.0/publish", nil)
	req.SetPathValue("project", "missing")
	req.SetPathValue("version", "1.0.0")
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.PublishRelease(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestPublishRelease_ReleaseNotFound(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &model.Project{Name: "rnfpub", Versioning: model.VersioningSemver}
	require.NoError(t, h.DB.CreateProject(ctx, proj))

	req := httptest.NewRequest("POST", "/api/projects/rnfpub/releases/9.9.9/publish", nil)
	req.SetPathValue("project", "rnfpub")
	req.SetPathValue("version", "9.9.9")
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.PublishRelease(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestPublishRelease_NoArtifacts(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &model.Project{Name: "noart", Versioning: model.VersioningSemver}
	require.NoError(t, h.DB.CreateProject(ctx, proj))
	rel := &model.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, h.DB.CreateRelease(ctx, rel))

	req := httptest.NewRequest("POST", "/api/projects/noart/releases/1.0.0/publish", nil)
	req.SetPathValue("project", "noart")
	req.SetPathValue("version", "1.0.0")
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.PublishRelease(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "no artifacts uploaded")
}

func TestPublishRelease_AlreadyPublished(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &model.Project{Name: "alreadypub", Versioning: model.VersioningSemver}
	require.NoError(t, h.DB.CreateProject(ctx, proj))
	rel := &model.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, h.DB.CreateRelease(ctx, rel))
	require.NoError(t, h.DB.PublishRelease(ctx, rel.ID))

	req := httptest.NewRequest("POST", "/api/projects/alreadypub/releases/1.0.0/publish", nil)
	req.SetPathValue("project", "alreadypub")
	req.SetPathValue("version", "1.0.0")
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.PublishRelease(rec, req)

	assert.Equal(t, http.StatusConflict, rec.Code)
}

// --- Security tests: project-scoped token isolation for artifacts ---

func TestUploadArtifact_ProjectScopedCannotUploadToDifferentProject(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	projA := &model.Project{Name: "art-sec-a", Versioning: model.VersioningSemver}
	require.NoError(t, h.DB.CreateProject(ctx, projA))
	projB := &model.Project{Name: "art-sec-b", Versioning: model.VersioningSemver}
	require.NoError(t, h.DB.CreateProject(ctx, projB))
	rel := &model.Release{ProjectID: projB.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, h.DB.CreateRelease(ctx, rel))

	// Token is scoped to project A, but tries to upload to project B
	req := httptest.NewRequest("POST", "/api/projects/art-sec-b/releases/1.0.0/artifacts/linux/amd64", strings.NewReader("malicious-payload"))
	req.SetPathValue("project", "art-sec-b")
	req.SetPathValue("version", "1.0.0")
	req.SetPathValue("os", "linux")
	req.SetPathValue("arch", "amd64")
	req = req.WithContext(projectWriteToken(req.Context(), projA.ID))
	rec := httptest.NewRecorder()
	h.UploadArtifact(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "not authorized")
}

func TestUploadArtifact_ProjectScopedCanUploadToOwnProject(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &model.Project{Name: "art-sec-own", Versioning: model.VersioningSemver}
	require.NoError(t, h.DB.CreateProject(ctx, proj))
	rel := &model.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, h.DB.CreateRelease(ctx, rel))

	// Token is scoped to the same project -- should succeed
	req := httptest.NewRequest("POST", "/api/projects/art-sec-own/releases/1.0.0/artifacts/linux/amd64", strings.NewReader("legit-binary"))
	req.SetPathValue("project", "art-sec-own")
	req.SetPathValue("version", "1.0.0")
	req.SetPathValue("os", "linux")
	req.SetPathValue("arch", "amd64")
	req = req.WithContext(projectWriteToken(req.Context(), proj.ID))
	rec := httptest.NewRecorder()
	h.UploadArtifact(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
}

// --- Security tests: filename sanitization ---

func TestSanitizeFilename_PathTraversal(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"unix path traversal", "../../../etc/passwd", "passwd"},
		{"windows path traversal", `..\..\windows\system32\config`, "config"},
		{"absolute unix path", "/etc/shadow", "shadow"},
		{"absolute windows path", `C:\Windows\System32\cmd.exe`, "cmd.exe"},
		{"nested traversal", "foo/../../bar/baz", "baz"},
		{"directory only", "../", ""},
		{"dot only", ".", ""},
		{"slash only", "/", ""},
		{"normal filename preserved", "my-binary-v1.0.0", "my-binary-v1.0.0"},
		{"filename with spaces", "my binary.tar.gz", "my binary.tar.gz"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := sanitizeFilename(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestSanitizeFilename_ControlCharacters(t *testing.T) {
	// Control characters should be stripped
	input := "file\x00name\x1f.bin"
	result := sanitizeFilename(input)
	assert.Equal(t, "filename.bin", result)
	// DEL character (0x7f)
	input = "test\x7ffile"
	result = sanitizeFilename(input)
	assert.Equal(t, "testfile", result)
}

func TestSanitizeFilename_TruncatesLongNames(t *testing.T) {
	// Filenames longer than 255 should be truncated
	longName := strings.Repeat("a", 300)
	result := sanitizeFilename(longName)
	assert.Equal(t, 255, len(result))
}

func TestUploadArtifact_FilenameHeaderSanitized(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &model.Project{Name: "fname-test", Versioning: model.VersioningSemver}
	require.NoError(t, h.DB.CreateProject(ctx, proj))
	rel := &model.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, h.DB.CreateRelease(ctx, rel))

	// Attempt path traversal via X-Artifact-Filename header
	req := httptest.NewRequest("POST", "/api/projects/fname-test/releases/1.0.0/artifacts/linux/amd64", strings.NewReader("binary-content"))
	req.SetPathValue("project", "fname-test")
	req.SetPathValue("version", "1.0.0")
	req.SetPathValue("os", "linux")
	req.SetPathValue("arch", "amd64")
	req.Header.Set("X-Artifact-Filename", "../../../etc/shadow")
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.UploadArtifact(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	var a model.Artifact
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &a))
	// The path traversal must be stripped, leaving only the base filename
	assert.Equal(t, "shadow", a.Filename)
	assert.NotContains(t, a.Filename, "..")
	assert.NotContains(t, a.Filename, "/")
}

func TestUploadArtifact_FilenameHeaderAbsolutePathSanitized(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &model.Project{Name: "fname-abs", Versioning: model.VersioningSemver}
	require.NoError(t, h.DB.CreateProject(ctx, proj))
	rel := &model.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, h.DB.CreateRelease(ctx, rel))

	req := httptest.NewRequest("POST", "/api/projects/fname-abs/releases/1.0.0/artifacts/darwin/arm64", strings.NewReader("data"))
	req.SetPathValue("project", "fname-abs")
	req.SetPathValue("version", "1.0.0")
	req.SetPathValue("os", "darwin")
	req.SetPathValue("arch", "arm64")
	req.Header.Set("X-Artifact-Filename", "/usr/local/bin/evil")
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.UploadArtifact(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	var a model.Artifact
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &a))
	assert.Equal(t, "evil", a.Filename)
}
