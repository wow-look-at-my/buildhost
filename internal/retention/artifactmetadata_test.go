package retention

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDeleter records what retention asked it to retract, and can fail on
// demand.
type fakeDeleter struct {
	calls []string
	err   error
}

func (f *fakeDeleter) MarkDeleted(_ context.Context, githubRepo, project, version, sha256Hex string) error {
	f.calls = append(f.calls, githubRepo+" "+project+" "+version+" "+sha256Hex)
	return f.err
}

// Evicting a release makes its artifacts unfetchable, so their storage records
// must be retracted -- otherwise the org's linked artifacts page keeps
// asserting buildhost holds bytes it just deleted.
func TestRun_EnforceMarksEvictedRecordsDeleted(t *testing.T) {
	t.Serial()
	d, store, p := setup(t)
	ctx := context.Background()
	require.NoError(t, d.SetProjectGitHubRepo(ctx, p.ID, "wow-look-at-my/proj"))

	putRelease(t, d, store, p.ID, "v1", 1, "master", "one")
	putRelease(t, d, store, p.ID, "v2", 2, "master", "two")
	putRelease(t, d, store, p.ID, "v3", 3, "master", "three")

	fake := &fakeDeleter{}
	ret := New(d, store, Config{KeepN: 1, RecencyGuard: 24 * time.Hour, Enforce: true}).WithRecordDeleter(fake)
	ret.clock = futureClock()

	rep, err := ret.Run(ctx)
	require.NoError(t, err)

	require.NotEqual(t, 0, len(rep.EvictedReleases), "expected releases past keep-N to be evicted")
	assert.Equal(t, len(rep.EvictedReleases), rep.RecordsMarkedDeleted,
		"every evicted release's artifact must have its record retracted")
	assert.Equal(t, 0, rep.RecordsUnmarked)
	assert.Empty(t, rep.RecordErrors)
	assert.Equal(t, len(rep.EvictedReleases), len(fake.calls))
	for _, c := range fake.calls {
		assert.Contains(t, c, "wow-look-at-my/proj")
	}
}

func TestRun_NoDeleterReportsUnmarkedRatherThanSilentlySkipping(t *testing.T) {
	t.Serial()
	d, store, p := setup(t)
	ctx := context.Background()
	require.NoError(t, d.SetProjectGitHubRepo(ctx, p.ID, "wow-look-at-my/proj"))

	putRelease(t, d, store, p.ID, "v1", 1, "master", "one")
	putRelease(t, d, store, p.ID, "v2", 2, "master", "two")

	ret := New(d, store, Config{KeepN: 1, RecencyGuard: 24 * time.Hour, Enforce: true})
	ret.clock = futureClock()

	rep, err := ret.Run(ctx)
	require.NoError(t, err)

	require.NotEqual(t, 0, len(rep.EvictedReleases))
	assert.Equal(t, 0, rep.RecordsMarkedDeleted)
	assert.Equal(t, len(rep.EvictedReleases), rep.RecordsUnmarked,
		"an unconfigured deleter must be counted, not treated as nothing-to-do")
	require.NotEmpty(t, rep.RecordErrors)
	assert.Contains(t, rep.RecordErrors[0], "no artifact-metadata record deleter configured")
}

// A failing sink is counted and its reason surfaced; eviction itself still
// succeeds, because the bytes are already gone and refusing to GC over a
// GitHub outage would be worse.
func TestRun_DeleterFailureCountedAndReported(t *testing.T) {
	t.Serial()
	d, store, p := setup(t)
	ctx := context.Background()
	require.NoError(t, d.SetProjectGitHubRepo(ctx, p.ID, "wow-look-at-my/proj"))

	putRelease(t, d, store, p.ID, "v1", 1, "master", "one")
	putRelease(t, d, store, p.ID, "v2", 2, "master", "two")

	fake := &fakeDeleter{err: assert.AnError}
	ret := New(d, store, Config{KeepN: 1, RecencyGuard: 24 * time.Hour, Enforce: true}).WithRecordDeleter(fake)
	ret.clock = futureClock()

	rep, err := ret.Run(ctx)
	require.NoError(t, err, "a record failure must not abort the eviction")

	assert.Equal(t, 0, rep.RecordsMarkedDeleted)
	assert.Equal(t, len(rep.EvictedReleases), rep.RecordsUnmarked)
	require.NotEmpty(t, rep.RecordErrors)
}

// A project with no recorded github_repo has no addressable record: nothing was
// ever posted under a known org, so there is nothing to retract and no failure
// to report either.
func TestRun_ProjectWithoutGithubRepoIsNotCounted(t *testing.T) {
	t.Serial()
	d, store, p := setup(t)
	ctx := context.Background()

	putRelease(t, d, store, p.ID, "v1", 1, "master", "one")
	putRelease(t, d, store, p.ID, "v2", 2, "master", "two")

	fake := &fakeDeleter{}
	ret := New(d, store, Config{KeepN: 1, RecencyGuard: 24 * time.Hour, Enforce: true}).WithRecordDeleter(fake)
	ret.clock = futureClock()

	rep, err := ret.Run(ctx)
	require.NoError(t, err)

	assert.Empty(t, fake.calls)
	assert.Equal(t, 0, rep.RecordsMarkedDeleted)
	assert.Equal(t, 0, rep.RecordsUnmarked)
}

// A dry run must not retract anything -- it reports the work it WOULD do, so an
// operator sees the record count before committing to the deletion.
func TestPlan_ReportsRecordsItWouldMarkWithoutCalling(t *testing.T) {
	t.Serial()
	d, store, p := setup(t)
	ctx := context.Background()
	require.NoError(t, d.SetProjectGitHubRepo(ctx, p.ID, "wow-look-at-my/proj"))

	putRelease(t, d, store, p.ID, "v1", 1, "master", "one")
	putRelease(t, d, store, p.ID, "v2", 2, "master", "two")

	fake := &fakeDeleter{}
	ret := New(d, store, Config{KeepN: 1, RecencyGuard: 24 * time.Hour}).WithRecordDeleter(fake)
	ret.clock = futureClock()

	rep, err := ret.Plan(ctx)
	require.NoError(t, err)

	assert.Empty(t, fake.calls, "a dry run must never retract a record")
	assert.Equal(t, 0, rep.RecordsMarkedDeleted)
	assert.NotEqual(t, 0, rep.RecordsUnmarked, "a dry run should report what it would mark")
}

// useTestServer points the API base at a local httptest server and gives the
// HTTP client a proxy-free transport: the default transport honours HTTP_PROXY
// from the environment, which in a sandboxed dev environment can intercept even
func useTestServer(t *testing.T, url string) func() {
	t.Helper()
	origBase, origClient := gitHubAPIBase, metadataHTTPClient
	gitHubAPIBase = url
	metadataHTTPClient = &http.Client{Transport: &http.Transport{}}
	return func() {
		gitHubAPIBase = origBase
		metadataHTTPClient = origClient
	}
}

func TestGitHubRecordDeleter_PostsDeletedStatus(t *testing.T) {
	t.Serial()
	var gotPath string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		assert.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		raw, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(raw, &body))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	restore := useTestServer(t, srv.URL)
	defer restore()

	d := &GitHubRecordDeleter{
		RegistryURL: "https://pazer.build",
		Bearer:      func(context.Context, string, string) string { return "tok" },
	}
	require.NoError(t, d.MarkDeleted(context.Background(), "wow-look-at-my/buildhost", "buildhost", "v7", "abc123"))

	assert.Equal(t, "/orgs/wow-look-at-my/artifacts/metadata/storage-record", gotPath)
	assert.Equal(t, "deleted", body["status"])
	assert.Equal(t, "sha256:abc123", body["digest"])
	assert.Equal(t, "https://pazer.build", body["registry_url"])
	assert.Equal(t, "buildhost", body["github_repository"])
}

func TestGitHubRecordDeleter_ForbiddenNamesThePermission(t *testing.T) {
	t.Serial()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message":"Resource not accessible by integration"}`))
	}))
	defer srv.Close()

	restore := useTestServer(t, srv.URL)
	defer restore()

	d := &GitHubRecordDeleter{
		RegistryURL: "https://pazer.build",
		Bearer:      func(context.Context, string, string) string { return "tok" },
	}
	err := d.MarkDeleted(context.Background(), "wow-look-at-my/buildhost", "buildhost", "v7", "abc123")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "artifact-metadata write")
}

func TestGitHubRecordDeleter_MissingConfigIsAnError(t *testing.T) {
	t.Serial()
	// No registry URL: the record cannot be identified without the same
	noURL := &GitHubRecordDeleter{Bearer: func(context.Context, string, string) string { return "tok" }}
	err := noURL.MarkDeleted(context.Background(), "o/r", "p", "v1", "abc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "BUILDHOST_PRIMARY_DOMAIN")

	// No credential: nothing to authenticate with.
	noCred := &GitHubRecordDeleter{RegistryURL: "https://pazer.build",
		Bearer: func(context.Context, string, string) string { return "" }}
	err = noCred.MarkDeleted(context.Background(), "o/r", "p", "v1", "abc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no GitHub credential")

	// Unusable github_repo: no org to post under.
	badRepo := &GitHubRecordDeleter{RegistryURL: "https://pazer.build",
		Bearer: func(context.Context, string, string) string { return "tok" }}
	err = badRepo.MarkDeleted(context.Background(), "notaslashrepo", "p", "v1", "abc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unusable github_repo")
}
