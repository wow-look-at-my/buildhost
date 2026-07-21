package apt

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/repackage"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

// seedAptProject creates a published release with one linux/amd64 binary
// artifact under a fresh project and returns all three rows.
func seedAptProject(t *testing.T, d *db.DB, store *storage.Filesystem, name, body string, createService bool) (*db.Project, *db.Release, *db.Artifact) {
	t.Helper()
	ctx := context.Background()

	proj := &db.Project{Name: name, Description: "a described app", Homepage: "https://example.test", Versioning: db.VersioningSemver, CreateService: createService}
	require.NoError(t, d.CreateProject(ctx, proj))
	if createService {
		require.NoError(t, d.SetProjectCreateService(ctx, proj.ID, true))
	}
	rel := &db.Release{ProjectID: proj.ID, Version: "1.2.3", VersionNum: 1002003, GitBranch: db.LatestBranch}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	key, size, err := store.Put(ctx, strings.NewReader(body))
	require.NoError(t, err)
	a := &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}
	require.NoError(t, d.CreateArtifact(ctx, a))
	return proj, rel, a
}

func getPackages(t *testing.T, h *Handler, projectName string, proj *db.Project) *httptest.ResponseRecorder {
	t.Helper()
	sub := "dists/stable/main/binary-amd64/Packages"
	req := httptest.NewRequest("GET", "/"+projectName+"/"+sub, nil)
	req = withRoute(req, proj, route{project: projectName, subPath: sub})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestDebGenerationDeterministic pins the precondition the deb digest cache
// (and apt's own hash verification of pool downloads against the signed index)
// relies on: two independent generations of the same artifact yield identical
// bytes, regardless of wall clock and of the request base URL. The >1s sleep
// would expose any second-granularity timestamp leaking into the ar, tar, or
// gzip headers. Variants cover the create_service materialization (extra
// control/data members) and a slash-namespaced project (folded package name).
func TestDebGenerationDeterministic(t *testing.T) {
	cases := []struct {
		name    string
		project string
		service bool
	}{
		{name: "plain", project: "myapp", service: false},
		{name: "create-service", project: "myapp-svc", service: true},
		{name: "slash-namespaced", project: "myrepo/server", service: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h, d, store := setupTest(t)
			ctx := context.Background()
			proj, rel, a := seedAptProject(t, d, store, tc.project, "deterministic-binary-bytes", tc.service)

			gen := func(baseURL string) []byte {
				t.Helper()
				out, err := h.Gen.Generate(ctx, repackage.FormatDeb, *proj, *rel, *a, baseURL)
				require.NoError(t, err)
				data, err := io.ReadAll(out.Reader)
				require.NoError(t, err)
				require.NoError(t, out.Reader.Close())
				return data
			}

			first := gen("https://alpha.example")
			time.Sleep(1100 * time.Millisecond)
			second := gen("https://beta.example")
			require.Equal(t, first, second)
		})
	}
}

func TestServePackages_CachesDebDigest(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()
	proj, rel, a := seedAptProject(t, d, store, "myapp", "binary-bytes", false)

	// No cache row before the first fetch.
	_, _, _, _, _, err := d.GetPackagedArtifact(ctx, a.ID, "deb")
	require.ErrorIs(t, err, db.ErrNotFound)

	rec := getPackages(t, h, "myapp", proj)
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()

	// The first fetch computed the digest and stored it, and the stored digest
	// matches an independent generation of the same artifact -- i.e. the exact
	// deb payload the pool download serves. The row records the SOURCE blob key
	// (no deb is stored) plus the generated payload size, and its metadata
	// carries the input fingerprint.
	out, err := h.Gen.Generate(ctx, repackage.FormatDeb, *proj, *rel, *a, "https://elsewhere.example")
	require.NoError(t, err)
	payload, err := io.ReadAll(out.Reader)
	require.NoError(t, err)
	require.NoError(t, out.Reader.Close())
	want := fmt.Sprintf("%x", sha256.Sum256(payload))

	cachedKey, cachedSize, cachedSHA, _, cachedMeta, err := d.GetPackagedArtifact(ctx, a.ID, "deb")
	require.NoError(t, err)
	assert.Equal(t, want, cachedSHA)
	assert.Equal(t, int64(len(payload)), cachedSize)
	assert.Equal(t, a.StorageKey, cachedKey)
	assert.Equal(t, debDigestFingerprint(proj, a), debMetadataFingerprint(cachedMeta))
	assert.Contains(t, body, fmt.Sprintf("SHA256: %s\n", want))
	assert.Contains(t, body, fmt.Sprintf("Size: %d\n", len(payload)))

	// A second fetch reads the cached digest instead of regenerating: poison
	// the row with a sentinel (keeping the fingerprint current) and the served
	// index must carry the sentinel.
	sentinel := strings.Repeat("42", 32)
	meta, err := json.Marshal(debMetadata{Inputs: debDigestFingerprint(proj, a)})
	require.NoError(t, err)
	require.NoError(t, d.CreatePackagedArtifact(ctx, a.ID, "deb", a.StorageKey, cachedSize, sentinel, "x.deb", string(meta)))
	assert.Contains(t, getPackages(t, h, "myapp", proj).Body.String(), fmt.Sprintf("SHA256: %s\n", sentinel))
}

// TestServeRelease_HashesMatchServedPackages pins the single-renderer
// invariant through the digest cache: the SHA256 lines in Release (and thus
// the clearsigned InRelease) are computed over exactly the bytes the Packages
// route serves.
func TestServeRelease_HashesMatchServedPackages(t *testing.T) {
	h, d, store := setupTest(t)
	proj, _, _ := seedAptProject(t, d, store, "myapp", "binary-bytes", false)

	pkgBody := getPackages(t, h, "myapp", proj).Body.Bytes()
	require.NotEmpty(t, pkgBody)

	req := httptest.NewRequest("GET", "/myapp/dists/stable/Release", nil)
	req = withRoute(req, proj, route{project: "myapp", subPath: "dists/stable/Release"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	wantLine := fmt.Sprintf(" %x %d main/binary-amd64/Packages", sha256.Sum256(pkgBody), len(pkgBody))
	assert.Contains(t, rec.Body.String(), wantLine)
}

// TestServePackages_RefillsOnCreateServiceFlip pins the staleness guard: the
// deb bytes bake in mutable project state (here the create_service
// materialization, flippable with no new release via the project PATCH), so a
// cached digest computed under the old inputs must be recomputed -- otherwise
// apt would verify downloads against a hash the pool no longer serves.
func TestServePackages_RefillsOnCreateServiceFlip(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()
	proj, _, a := seedAptProject(t, d, store, "myapp", "binary-bytes", false)

	first := getPackages(t, h, "myapp", proj).Body.String()
	_, _, shaOff, _, _, err := d.GetPackagedArtifact(ctx, a.ID, "deb")
	require.NoError(t, err)
	require.Contains(t, first, fmt.Sprintf("SHA256: %s\n", shaOff))

	// Flip the packaging-agnostic service setting (in the DB and on the
	// context copy the middleware would reload per request).
	require.NoError(t, d.SetProjectCreateService(ctx, proj.ID, true))
	proj.CreateService = true

	second := getPackages(t, h, "myapp", proj).Body.String()
	_, _, shaOn, _, metaOn, err := d.GetPackagedArtifact(ctx, a.ID, "deb")
	require.NoError(t, err)
	assert.NotEqual(t, shaOff, shaOn, "service materialization changes the deb bytes, so the cached digest must refill")
	assert.Contains(t, second, fmt.Sprintf("SHA256: %s\n", shaOn))
	assert.Equal(t, debDigestFingerprint(proj, a), debMetadataFingerprint(metaOn))

	// Stable again once refilled: the third fetch serves the refilled row.
	assert.Equal(t, second, getPackages(t, h, "myapp", proj).Body.String())
}

// A digest that cannot be computed (here: the stored blob is gone) must
// surface as an error on BOTH the Packages index and the Release hashes --
// never a silently wrong Size/SHA256 that apt would reject on download.
func TestServePackages_DigestErrorSurfaces(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000, GitBranch: db.LatestBranch}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))
	require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: strings.Repeat("ab", 32), Size: 6, SHA256: strings.Repeat("ab", 32),
	}))

	assert.Equal(t, http.StatusInternalServerError, getPackages(t, h, "myapp", proj).Code)

	req := httptest.NewRequest("GET", "/myapp/dists/stable/Release", nil)
	req = withRoute(req, proj, route{project: "myapp", subPath: "dists/stable/Release"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}
