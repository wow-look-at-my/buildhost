package static

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/pack"
	"github.com/wow-look-at-my/buildhost/internal/recompress"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

type rawFixture struct {
	store    storage.Storage
	artifact db.Artifact
	tmp      string
	upload   []byte
}

func newRawFixture(t *testing.T, upload []byte) *rawFixture {
	t.Helper()
	store, err := storage.NewFilesystem(t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	key, size, err := store.Put(context.Background(), bytes.NewReader(upload))
	if err != nil {
		t.Fatal(err)
	}
	return &rawFixture{
		store:    store,
		tmp:      t.TempDir(),
		upload:   upload,
		artifact: db.Artifact{StorageKey: key, Size: size, SHA256: key, Kind: db.KindBinary},
	}
}

func (f *rawFixture) get(t *testing.T, shaper *Shaper, accept, query, ip string) *httptest.ResponseRecorder {
	t.Helper()
	ctx := ServeContext{
		Project:  db.Project{Name: "fixture"},
		Artifact: f.artifact,
		Store:    f.store,
		TmpDir:   f.tmp,
		Shaper:   shaper,
	}
	req := httptest.NewRequest("GET", "/file?fmt=raw"+query, nil)
	if accept != "" {
		req.Header.Set("Accept-Encoding", accept)
	}
	if ip != "" {
		req.Header.Set("X-Forwarded-For", ip)
	}
	rec := httptest.NewRecorder()
	format := &rawFmt{}
	if err := format.Serve(rec, req, ctx); err != nil {
		t.Fatalf("serve: %v", err)
	}
	return rec
}

func decodeZstd(t *testing.T, data []byte) []byte {
	t.Helper()
	r, err := zstd.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("zstd reader: %v", err)
	}
	defer r.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("zstd decode: %v", err)
	}
	return out
}

func decodeBrotli(t *testing.T, body []byte) []byte {
	t.Helper()
	out, err := io.ReadAll(brotli.NewReader(bytes.NewReader(body)))
	if err != nil {
		t.Fatalf("brotli decode: %v", err)
	}
	return out
}

func decodeGzip(t *testing.T, body []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("gzip decode: %v", err)
	}
	defer zr.Close()
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gzip read: %v", err)
	}
	return out
}

// buildELFFixture compiles a real Linux executable to strip and pack.
func buildELFFixture(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte(
		"package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"packed-fixture\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "fixture")
	cmd := exec.Command("go", "build", "-o", out, src)
	env := make([]string, 0, len(os.Environ())+3)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GOFLAGS=") {
			continue
		}
		env = append(env, kv)
	}
	cmd.Env = append(env, "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot build an ELF fixture (no go toolchain?): %s: %s", err, b)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func testPacker(t *testing.T) *pack.Packer { return pack.New(t.TempDir()) }

// requireUPX skips a packing assertion when the host has no packer. Packing is
// optional: without upx the download is served stripped.
func requireUPX(t *testing.T) {
	t.Helper()
	if !pack.New(t.TempDir()).Available() {
		t.Skip("no upx executable: the download is served stripped")
	}
}

// The identity download is the stripped, packed binary: smaller than the
// upload, still an ELF, and the packer's signature is on it.
func TestRawIdentityIsPacked(t *testing.T) {
	t.Serial()
	requireUPX(t)
	f := newRawFixture(t, buildELFFixture(t))
	sh := &Shaper{Pack: testPacker(t)}

	rec := f.get(t, sh, "", "", "")
	assert.Empty(t, rec.Header().Get("Content-Encoding"))
	assert.Equal(t, "Accept-Encoding", rec.Header().Get("Vary"))
	assert.Equal(t, "available", rec.Header().Get("X-Debug-Symbols"))

	body := rec.Body.Bytes()
	assert.Less(t, len(body), len(f.upload), "the identity download must be smaller than the upload")
	assert.True(t, bytes.HasPrefix(body, []byte{0x7f, 'E', 'L', 'F'}), "the served file must remain an ELF")
	assert.True(t, bytes.Contains(body, []byte("UPX!")), "the served file must be UPX-packed")

	again := f.get(t, sh, "", "", "")
	assert.Equal(t, body, again.Body.Bytes(), "an immutable URL must serve identical bytes")
}

// A host without a packer serves the stripped binary: complete, smaller than
// the upload and identical on a repeat.
func TestRawWithoutAPackerIsStillComplete(t *testing.T) {
	t.Serial()
	t.Setenv("PATH", t.TempDir())
	t.Setenv(pack.EnvOverride, "")
	f := newRawFixture(t, buildELFFixture(t))
	sh := &Shaper{Pack: testPacker(t)}

	rec := f.get(t, sh, "", "", "")
	requireStatus(t, rec)
	body := rec.Body.Bytes()
	assert.True(t, bytes.HasPrefix(body, []byte{0x7f, 'E', 'L', 'F'}))
	assert.Less(t, len(body), len(f.upload), "the stripped download must still be smaller")
	assert.NotContains(t, string(body), "UPX!", "packing must not have run")

	again := f.get(t, sh, "", "", "")
	assert.Equal(t, body, again.Body.Bytes())
}

// br and gzip decode to the same payload, and that payload is the packed
// binary rather than the raw upload.
func TestRawBrotliAndGzipDecodeIdentically(t *testing.T) {
	t.Serial()
	f := newRawFixture(t, buildELFFixture(t))
	sh := &Shaper{Pack: testPacker(t)}

	identity := f.get(t, sh, "", "", "").Body.Bytes()

	br := f.get(t, sh, "br", "", "")
	assert.Equal(t, "br", br.Header().Get("Content-Encoding"))
	assert.Equal(t, "Accept-Encoding", br.Header().Get("Vary"))
	brotliBody := decodeBrotli(t, br.Body.Bytes())
	assert.Equal(t, identity, brotliBody, "brotli must decode to the served payload")

	gz := f.get(t, sh, "gzip", "", "")
	assert.Equal(t, "gzip", gz.Header().Get("Content-Encoding"))
	assert.Equal(t, brotliBody, decodeGzip(t, gz.Body.Bytes()), "gzip and brotli must decode identically")
}

// A zstd client gets the stored stream passed through, whatever the artifact
// is: no stripping, no packing, no work.
func TestRawZstdPassthroughWins(t *testing.T) {
	t.Serial()
	f := newRawFixture(t, buildELFFixture(t))
	sh := &Shaper{Pack: testPacker(t)}

	rec := f.get(t, sh, "zstd, br, gzip", "", "")
	assert.Equal(t, "zstd", rec.Header().Get("Content-Encoding"))
	assert.Equal(t, f.upload, decodeZstd(t, rec.Body.Bytes()))
}

// A cached variant answers every client, and the second request rewrites
// nothing.
func TestRawCacheServesRepeatsWithoutRewriting(t *testing.T) {
	t.Serial()
	f := newRawFixture(t, buildELFFixture(t))
	cache, err := recompress.NewCache(t.TempDir(), 8<<20, 16, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	sh := &Shaper{Cache: cache, Pack: testPacker(t)}

	first := f.get(t, sh, "br", "", "")
	requireStatus(t, first)
	assert.Equal(t, "br", first.Header().Get("Content-Encoding"))

	entries := cacheFiles(t, sh.Cache.Dir())
	if len(entries) != 1 {
		t.Fatalf("want one cache entry, got %d: %v", len(entries), entries)
	}
	info, err := os.Stat(filepath.Join(cache.Dir(), entries[0]))
	if err != nil {
		t.Fatal(err)
	}
	stamp := info.ModTime()

	time.Sleep(10 * time.Millisecond)
	second := f.get(t, sh, "br", "", "")
	assert.Equal(t, first.Body.String(), second.Body.String())
	assert.Equal(t, entries, cacheFiles(t, sh.Cache.Dir()), "a hit must not add an entry")
	info, err = os.Stat(filepath.Join(cache.Dir(), entries[0]))
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, stamp, info.ModTime(), "a hit must not rewrite the entry")
}

// The allowance is per IP, and a denied request is still a complete,
// untransformed download rather than an error or a short body.
func TestRawThrottleFallsBackToIdentity(t *testing.T) {
	t.Serial()
	f := newRawFixture(t, buildELFFixture(t))
	limiter := recompress.NewLimiter(recompress.LimiterConfig{
		PerIP: 1, Window: time.Minute, LoadCeiling: 1,
	}, func() float64 { return 0 })
	sh := &Shaper{Throttle: limiter, Pack: testPacker(t)}

	first := f.get(t, sh, "br", "", "203.0.113.9")
	requireStatus(t, first)
	assert.Equal(t, "br", first.Header().Get("Content-Encoding"))

	second := f.get(t, sh, "br", "", "203.0.113.9")
	requireStatus(t, second)
	assert.Empty(t, second.Header().Get("Content-Encoding"), "the allowance is spent")
	assert.NotEmpty(t, second.Body.Bytes())
	assert.Equal(t, strconv.Itoa(len(second.Body.Bytes())), second.Header().Get("Content-Length"))

	other := f.get(t, sh, "br", "", "203.0.113.10")
	requireStatus(t, other)
	assert.Equal(t, "br", other.Header().Get("Content-Encoding"), "another client still compresses")
}

// A machine over its load ceiling starts no new recompression, and every
// download is still complete.
func TestRawLoadCeilingStopsRecompression(t *testing.T) {
	t.Serial()
	f := newRawFixture(t, buildELFFixture(t))
	limiter := recompress.NewLimiter(recompress.LimiterConfig{
		PerIP: 100, Window: time.Minute, LoadCeiling: 1,
	}, func() float64 { return 4 })
	sh := &Shaper{Throttle: limiter, Pack: testPacker(t)}

	for _, accept := range []string{"br", "gzip"} {
		rec := f.get(t, sh, accept, "", "203.0.113.9")
		requireStatus(t, rec)
		assert.Empty(t, rec.Header().Get("Content-Encoding"))
		assert.NotEmpty(t, rec.Body.Bytes())
	}
}

// An APE is offered to the packer once. A refusal serves the upload unchanged,
// and a repeat download serves the same bytes without trying again.
func TestRawAPERefusalIsStable(t *testing.T) {
	t.Serial()
	ape := append([]byte("MZqFpD"), bytes.Repeat([]byte{0x90}, 8192)...)
	f := newRawFixture(t, ape)
	counting := &countingPacker{}
	sh := &Shaper{Pack: counting}

	first := f.get(t, sh, "", "", "")
	second := f.get(t, sh, "", "", "")
	assert.Equal(t, ape, first.Body.Bytes(), "a refused APE is served byte for byte")
	assert.Equal(t, first.Body.Bytes(), second.Body.Bytes())
	assert.Equal(t, 1, counting.attempts, "the refusal is remembered")
}

func TestRawDebugReturnsTheUpload(t *testing.T) {
	t.Serial()
	f := newRawFixture(t, buildELFFixture(t))
	sh := &Shaper{Pack: testPacker(t)}

	rec := f.get(t, sh, "", "&debug=1", "")
	requireStatus(t, rec)
	assert.Equal(t, f.upload, rec.Body.Bytes())
}

func TestNegotiate(t *testing.T) {
	t.Serial()
	assert.Equal(t, codingZstd, Negotiate("zstd, br, gzip"))
	assert.Equal(t, codingBr, Negotiate("gzip, deflate, br"))
	assert.Equal(t, codingGzip, Negotiate("gzip, deflate"))
	assert.Equal(t, codingIdentity, Negotiate(""))
	assert.Equal(t, codingIdentity, Negotiate("identity"))
	assert.Equal(t, codingIdentity, Negotiate("*"), "a wildcard does not name an encoding")
	assert.Equal(t, codingGzip, Negotiate("br;q=0, gzip"))
	assert.Equal(t, codingIdentity, Negotiate("br;q=0"))
}

type countingPacker struct {
	attempts int
}

func (c *countingPacker) Pack(_, _ string) (bool, error) {
	c.attempts++
	return false, nil
}

func requireStatus(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
}

func cacheFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Type().IsRegular() {
			names = append(names, e.Name())
		}
	}
	return names
}
