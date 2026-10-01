package goproxy

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

type fakeGitHub struct {
	Private       bool
	DefaultBranch string
	// Tags maps tag name -> commit sha.
	Tags  map[string]string
	Files map[string]string
	// TreeFiles maps repo-relative path -> contents, served in the tarball.
	TreeFiles map[string]string
	Status    int
	// Body is the response body used with Status.
	Body        string
	RateLimited bool

	server *httptest.Server
	calls  int
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{
		DefaultBranch: "master",
		Tags:          map[string]string{},
		Files:         map[string]string{},
		TreeFiles:     map[string]string{},
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeGitHub) URL() string { return f.server.URL }

func (f *fakeGitHub) handle(w http.ResponseWriter, r *http.Request) {
	f.calls++

	if f.Status != 0 {
		if f.RateLimited {
			w.Header().Set("X-RateLimit-Remaining", "0")
		}
		w.WriteHeader(f.Status)
		_, _ = w.Write([]byte(f.Body))
		return
	}

	if f.Private && r.Header.Get("Authorization") == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}

	p := r.URL.Path
	switch {
	case strings.Contains(p, "/git/matching-refs/tags"):
		if r.URL.Query().Get("page") != "1" {
			writeJSON(w, http.StatusOK, []any{})
			return
		}
		out := []map[string]any{}
		for name, sha := range f.Tags {
			out = append(out, map[string]any{
				"ref":    "refs/tags/" + name,
				"object": map[string]any{"sha": sha, "type": "commit"},
			})
		}
		writeJSON(w, http.StatusOK, out)

	case strings.Contains(p, "/tarball/"):
		f.writeTarball(w)

	case strings.Contains(p, "/contents/"):
		_, rest, _ := strings.Cut(p, "/contents/")
		key := r.URL.Query().Get("ref") + ":" + rest
		content, ok := f.Files[key]
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{
			"content":  base64.StdEncoding.EncodeToString([]byte(content)),
			"encoding": "base64",
		})

	case strings.Contains(p, "/commits/"):
		_, ref, _ := strings.Cut(p, "/commits/")
		sha := ref
		if s, ok := f.Tags[ref]; ok {
			sha = s
		}
		if ref == f.DefaultBranch {
			sha = "headshaaaaaa0000000000000000000000000000"
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"sha": sha,
			"commit": map[string]any{
				"committer": map[string]any{"date": "2026-01-02T03:04:05Z"},
			},
		})

	default: // GET /repos/{owner}/{repo}
		writeJSON(w, http.StatusOK, map[string]any{"default_branch": f.DefaultBranch})
	}
}

func (f *fakeGitHub) writeTarball(w http.ResponseWriter) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for path, content := range f.TreeFiles {
		hdr := &tar.Header{
			Name:     "owner-repo-abc1234/" + path,
			Mode:     0o644,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
			ModTime:  time.Unix(0, 0),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			panic(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			panic(err)
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	w.Header().Set("Content-Type", "application/gzip")
	_, _ = w.Write(buf.Bytes())
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// newTestService wires a Service against the fake upstream. token is what the
// credential resolver hands back -- "" models a proxy with no GitHub credential
// at all, which is the deployed failure this package was written for.
func newTestService(t *testing.T, fake *fakeGitHub, token string, privatePrefixes []string) *Service {
	t.Helper()

	d, err := db.Open(filepath.Join(t.TempDir(), "goproxy.db"))
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })

	store, err := storage.NewFilesystem(t.TempDir(), true)
	require.NoError(t, err)

	s := newService(Config{PrivatePrefixes: privatePrefixes, Upstream: ""}, d, store, t.TempDir())
	s.github.api = fake.URL()
	s.github.tokenFor = func(context.Context, string, string) string { return token }
	return s
}

func seedModule(f *fakeGitHub, modPath, dir, tag, sha, goMod string) {
	f.Tags[tag] = sha
	p := "go.mod"
	if dir != "" {
		p = dir + "/go.mod"
	}
	// go.mod is read at HEAD (to resolve the module root) and at the commit.
	for _, ref := range []string{"HEAD", sha, "headshaaaaaa0000000000000000000000000000"} {
		f.Files[ref+":"+p] = goMod
	}
	f.TreeFiles[p] = goMod
	src := "package lib\n"
	if dir != "" {
		f.TreeFiles[dir+"/lib.go"] = src
	} else {
		f.TreeFiles["lib.go"] = src
	}
	_ = fmt.Sprint(modPath)
}
