package oci

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/repackage"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

// fakeAPE builds a payload with the prologue an APE carries: the magic the
// detector matches, and the printf call per architecture that the trampoline
// uses to write a real ELF header over its own copy. Reading a header out of
// that prologue is how the image ships an ELF, so the fixture has to carry one.
func fakeAPE() []byte {
	var b strings.Builder
	b.WriteString("MZqFpD='\n")
	for _, m := range []uint16{0x3e, 0xb7} { // EM_X86_64, EM_AARCH64
		b.WriteString("    printf '" + shellOctal(elf64Header(m)) + "' >&7\n")
	}
	// The payload the trampoline would have staged. Its content is irrelevant
	// here: nothing in these tests executes it.
	b.WriteString(strings.Repeat("payload\n", 64))
	return []byte(b.String())
}

// elf64Header is a 64-byte ELF64 header for machine.
func elf64Header(machine uint16) []byte {
	h := make([]byte, 64)
	copy(h, "\x7fELF\x02\x01\x01\x00")
	binary.LittleEndian.PutUint16(h[16:], 2) // ET_EXEC
	binary.LittleEndian.PutUint16(h[18:], machine)
	binary.LittleEndian.PutUint32(h[20:], 1) // EV_CURRENT
	return h
}

// shellOctal renders b as the escaped body of a single-quoted printf argument.
func shellOctal(b []byte) string {
	var out strings.Builder
	for _, c := range b {
		fmt.Fprintf(&out, `\%03o`, c)
	}
	return out.String()
}

// apeShellCache returns a shell cache pre-seeded on disk, so an APE image
// synthesizes with no registry reachable.
func apeShellCache(t *testing.T) *repackage.ShellCache {
	t.Helper()
	dir := t.TempDir()
	images := map[db.Arch]string{}
	for i, arch := range []db.Arch{db.ArchAMD64, db.ArchARM64} {
		digest := strings.Repeat(fmt.Sprintf("%d", i+1), 64)
		images[arch] = "sha256:" + digest
		seeded := filepath.Join(dir, string(arch), digest)
		require.NoError(t, os.MkdirAll(seeded, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(seeded, "busybox"), []byte("#!/bin/sh\nfake\n"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(seeded, "applets"), []byte("sh\nbusybox\n"), 0o644))
	}
	return &repackage.ShellCache{Dir: dir, Images: images}
}

// setupAPETest is setupTest with a shell cache, which an APE image needs.
func setupAPETest(t *testing.T) (*Handler, *db.DB, *storage.Filesystem) {
	t.Helper()
	h, d, store := setupTest(t)
	h.Gen = repackage.NewGenerator(store, d, t.TempDir(), repackage.WithShellCache(apeShellCache(t)))
	return h, d, store
}

// publishAPE publishes one APE artifact covering platforms, through the same
// multi-platform path a real `PUT .../artifacts/ape?platforms=...` takes.
func publishAPE(t *testing.T, ctx context.Context, d *db.DB, store *storage.Filesystem, proj *db.Project, platforms []db.Platform) *db.Release {
	t.Helper()
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000, GitBranch: db.LatestBranch}
	require.NoError(t, d.CreateRelease(ctx, rel))

	payload := fakeAPE()
	key, size, err := store.Put(ctx, strings.NewReader(string(payload)))
	require.NoError(t, err)
	require.NoError(t, d.CreateMultiPlatformArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, Kind: db.KindBinary, StorageKey: key,
		Size: size, SHA256: key, Filename: proj.Name, ExeFormat: "ape",
	}, platforms))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))
	return rel
}

// fetchIndex GETs the release's manifest by tag.
func fetchIndex(t *testing.T, h *Handler, proj *db.Project) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/v2/"+proj.Name+"/manifests/latest", nil)
	req = withRoute(req, proj, route{project: proj.Name, action: "manifests", reference: "latest"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// indexPlatforms reads the "os/arch" of every child of an image index.
func indexPlatforms(t *testing.T, body []byte) []string {
	t.Helper()
	var index struct {
		Manifests []indexEntry `json:"manifests"`
	}
	require.NoError(t, json.Unmarshal(body, &index))
	out := make([]string, 0, len(index.Manifests))
	for _, m := range index.Manifests {
		out = append(out, m.Platform.OS+"/"+m.Platform.Architecture)
	}
	return out
}

// TestAPEIndexCoversEveryPlatform is the regression test for the missing
// canonical slot: an APE covering three platforms served an index of TWO. The
// one it dropped was linux/amd64 -- the artifact's own canonical platform, the
// only one actually uploaded, and the one every consumer on the deployment
// needs. `docker pull` answered "no matching manifest for linux/amd64 in the
// manifest list entries" while the release JSON listed all three.
func TestAPEIndexCoversEveryPlatform(t *testing.T) {
	t.Serial()
	h, d, store := setupAPETest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "apeapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	publishAPE(t, ctx, d, store, proj, []db.Platform{
		{OS: db.OSLinux, Arch: db.ArchAMD64},
		{OS: db.OSDarwin, Arch: db.ArchARM64},
		{OS: db.OSWindows, Arch: db.ArchAMD64},
	})

	rec := fetchIndex(t, h, proj)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body)
	// linux/amd64 is the platform that went missing, and it is the only one an
	// image can serve: a rootfs and a shell built for linux, stamped darwin or
	// windows, is a platform entry nothing can pull and run.
	assert.ElementsMatch(t, []string{"linux/amd64", "linux/arm64"}, indexPlatforms(t, rec.Body.Bytes()))
}

// TestAPEIndexCoversEveryPlatform_Narrower runs the same assertion for a
// two-platform and a one-platform APE, so a fix that appends the canonical
// entry unconditionally cannot pass by luck: here it would duplicate it.
func TestAPEIndexCoversEveryPlatform_Narrower(t *testing.T) {
	t.Serial()
	for _, tc := range []struct {
		name      string
		platforms []db.Platform
		want      []string
	}{
		{
			name: "two platforms",
			platforms: []db.Platform{
				{OS: db.OSLinux, Arch: db.ArchAMD64},
				{OS: db.OSDarwin, Arch: db.ArchARM64},
			},
			want: []string{"linux/amd64", "darwin/arm64"},
		},
		{
			name:      "one platform",
			platforms: []db.Platform{{OS: db.OSLinux, Arch: db.ArchAMD64}},
			want:      []string{"linux/amd64"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, d, store := setupAPETest(t)
			ctx := context.Background()
			proj := &db.Project{Name: "apeapp", Versioning: db.VersioningSemver}
			require.NoError(t, d.CreateProject(ctx, proj))
			publishAPE(t, ctx, d, store, proj, tc.platforms)

			rec := fetchIndex(t, h, proj)
			require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body)
			if len(tc.want) == 1 {
				// A single-platform release serves the image manifest itself,
				// so there is no index to read platforms out of. The config is
				// where the platform lives, and the pull-path test covers it.
				assert.Equal(t, "application/vnd.oci.image.manifest.v1+json", rec.Header().Get("Content-Type"))
				return
			}
			assert.ElementsMatch(t, tc.want, indexPlatforms(t, rec.Body.Bytes()))
			assert.Contains(t, indexPlatforms(t, rec.Body.Bytes()), "linux/amd64")
		})
	}
}

// TestAPEIndexFailsLoudlyWhenAChildCannotBeSynthesized covers the drop that
// used to be silent. A platform the release covers and the registry cannot
// serve must fail the request: a 200 carrying a short index moves the failure
// to the client's platform matcher, which reports a missing platform and no
// cause. An APE image needs a shell layer, so a generator with no shell cache
// fails exactly the linux child and no other -- which is the shape of the
// production defect.
func TestAPEIndexFailsLoudlyWhenAChildCannotBeSynthesized(t *testing.T) {
	t.Serial()
	h, d, store := setupAPETest(t)
	ctx := context.Background()

	// No shell cache: GenerateForPlatform fails for the linux slot alone.
	h.Gen = repackage.NewGenerator(store, d, t.TempDir())

	proj := &db.Project{Name: "apeapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	publishAPE(t, ctx, d, store, proj, []db.Platform{
		{OS: db.OSLinux, Arch: db.ArchAMD64},
		{OS: db.OSDarwin, Arch: db.ArchARM64},
	})

	rec := fetchIndex(t, h, proj)
	require.Equal(t, http.StatusInternalServerError, rec.Code,
		"a platform that cannot be synthesized must fail the pull, not shorten the index")
	assert.Contains(t, rec.Body.String(), "linux/amd64", "the error must name the platform that failed")
}

// TestAPEIndexFailsLoudlyWhenAChildIsNotLinked covers the other silent drop:
// BlobBelongsToProject answering false left no log line at all, so a cache-key
// collision between two platforms was invisible from every surface.
func TestAPEIndexFailsLoudlyWhenAChildIsNotLinked(t *testing.T) {
	t.Serial()
	h, d, store := setupAPETest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "apeapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	publishAPE(t, ctx, d, store, proj, []db.Platform{
		{OS: db.OSLinux, Arch: db.ArchAMD64},
		{OS: db.OSDarwin, Arch: db.ArchARM64},
	})

	// A generator with no database link never records the manifest it just
	// synthesized, so the membership check answers false for every child --
	// the same answer a cache-key collision between two platforms produces.
	h.Gen = repackage.NewGenerator(store, nil, t.TempDir(), repackage.WithShellCache(apeShellCache(t)))

	rec := fetchIndex(t, h, proj)
	assert.Equal(t, http.StatusInternalServerError, rec.Code,
		"an unlinked child must fail the pull, not drop out of the index")
	assert.Contains(t, rec.Body.String(), "not linked to this project")
}

// TestAPEPullPathResolvesLinuxAMD64 walks what `docker pull` actually does on a
// linux/amd64 host: match a child by platform, fetch that manifest, fetch its
// config blob, and read the platform back off the config. That is the assertion
// that would have caught the missing canonical slot.
func TestAPEPullPathResolvesLinuxAMD64(t *testing.T) {
	t.Serial()
	h, d, store := setupAPETest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "apeapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	publishAPE(t, ctx, d, store, proj, []db.Platform{
		{OS: db.OSLinux, Arch: db.ArchAMD64},
		{OS: db.OSDarwin, Arch: db.ArchARM64},
		{OS: db.OSWindows, Arch: db.ArchAMD64},
	})

	rec := fetchIndex(t, h, proj)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body)

	var index struct {
		Manifests []indexEntry `json:"manifests"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &index))

	var child indexEntry
	for _, m := range index.Manifests {
		if m.Platform.OS == "linux" && m.Platform.Architecture == "amd64" {
			child = m
		}
	}
	require.NotEmpty(t, child.Digest, "no child matched linux/amd64, which is what the puller reports")

	manifest := getBlobbish(t, h, proj, "manifests", child.Digest)
	var m struct {
		Config struct {
			Digest string `json:"digest"`
		} `json:"config"`
		Layers []struct {
			Digest string `json:"digest"`
		} `json:"layers"`
	}
	require.NoError(t, json.Unmarshal(manifest, &m))

	var config struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
	}
	require.NoError(t, json.Unmarshal(getBlobbish(t, h, proj, "blobs", m.Config.Digest), &config))
	assert.Equal(t, "linux", config.OS)
	assert.Equal(t, "amd64", config.Architecture)
	assert.Len(t, m.Layers, 3, "essentials, shell and binary")
}

// getBlobbish fetches a manifest or blob by digest and requires a 200.
func getBlobbish(t *testing.T, h *Handler, proj *db.Project, action, digest string) []byte {
	t.Helper()
	req := httptest.NewRequest("GET", "/v2/"+proj.Name+"/"+action+"/"+digest, nil)
	req = withRoute(req, proj, route{project: proj.Name, action: action, reference: digest})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "%s %s must resolve", action, digest)
	return rec.Body.Bytes()
}

// TestNonAPEIndexIsNotDoubleListed guards the ordinary per-platform case: a
// project that uploads a real build per platform gets one index entry per
// artifact, and the fix for the APE case must not add a second entry for the
// row's own (os, arch).
func TestNonAPEIndexIsNotDoubleListed(t *testing.T) {
	t.Serial()
	h, d, store := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	publishMultiArch(t, ctx, d, store, proj, "1.0.0", 1000000)

	rec := fetchIndex(t, h, proj)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body)
	assert.ElementsMatch(t, []string{"linux/amd64", "linux/arm64"}, indexPlatforms(t, rec.Body.Bytes()))
}
