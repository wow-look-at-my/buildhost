package goproxy

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	modzip "golang.org/x/mod/zip"
)

const githubAPI = "https://api.github.com"

// upstreamGitHub is the Upstream name reported on an *Error from this source.
const upstreamGitHub = "github"

// githubSource fetches module content straight from the GitHub REST API. There
// is deliberately no `go` binary and no `git` subprocess behind this: every
// failure is an HTTP status we can classify honestly (see errors.go), rather
// than "exit status 128" from a child process whose credential plumbing is
// invisible.
type githubSource struct {
	client *http.Client
	// tokenFor resolves the bearer for a repo; injected so tests can drive the
	// credential without touching global auth state.
	tokenFor func(ctx context.Context, owner, repo string) string
	// tarballLimit caps a repository tarball read (guards a hostile or runaway
	// upstream); 0 means modzip's own module size limit is the only bound.
	tarballLimit int64
	// tmpDir is where tarballs are expanded and zips assembled; "" uses the OS
	// default.
	tmpDir string
}

func newGitHubSource(client *http.Client, tmpDir string) *githubSource {
	return &githubSource{
		client:       client,
		tokenFor:     auth.BearerForRepo,
		tarballLimit: 2 << 30,
		tmpDir:       tmpDir,
	}
}

type tagRef struct {
	Name string
	// SHA is the ref's object sha. For an annotated tag that is the tag OBJECT,
	// not the commit -- Annotated says which, and commitSHA dereferences it.
	SHA       string
	Annotated bool
}

// do issues an authenticated GitHub API request and classifies the response.
// Every non-2xx becomes a typed *Error here, which is the single place upstream
// status is interpreted -- so no caller can accidentally turn a 403 into a 404.
func (g *githubSource) do(ctx context.Context, owner, repo, method, url, mod, ver, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, upstreamErr(mod, ver, upstreamGitHub, 0, "building request", err)
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	tok := g.tokenFor(ctx, owner, repo)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, upstreamErr(mod, ver, upstreamGitHub, 0, "request failed", err)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, nil
	}
	defer resp.Body.Close()
	detail := githubMessage(resp)

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, unauthorizedErr(mod, ver, upstreamGitHub, resp.StatusCode,
			credentialNote(tok)+"; "+detail)

	case resp.StatusCode == http.StatusForbidden && isRateLimited(resp, detail):
		// A rate limit is a 403 but is emphatically not an authorization problem:
		// it clears on its own, and reporting it as one sends whoever reads it
		// after a credential that was never at fault.
		return nil, upstreamErr(mod, ver, upstreamGitHub, resp.StatusCode,
			"rate limited: "+detail+resetNote(resp), nil)

	case resp.StatusCode == http.StatusForbidden:
		return nil, unauthorizedErr(mod, ver, upstreamGitHub, resp.StatusCode,
			credentialNote(tok)+"; "+detail)

	case resp.StatusCode == http.StatusNotFound:
		// GitHub answers 404 both for a repository that does not exist and for one
		// the caller may not see -- it will not confirm a private repo's existence
		// to someone unauthorized. With no credential at all the two are strictly
		// indistinguishable, and calling it "not found" is the exact laundering
		// that makes a credential failure look like a typo in go.mod. So: no
		// credential means unauthorized, not missing.
		if tok == "" {
			return nil, unauthorizedErr(mod, ver, upstreamGitHub, resp.StatusCode,
				"the proxy has NO GitHub credential, so a private repository is "+
					"indistinguishable from a missing one")
		}
		return nil, notFoundErr(mod, ver, upstreamGitHub, resp.StatusCode,
			detail+" (the proxy's credential is presented; if the repository does exist, "+
				"that credential is not authorized for it)")

	case resp.StatusCode >= 500:
		return nil, upstreamErr(mod, ver, upstreamGitHub, resp.StatusCode, detail, nil)
	}
	return nil, upstreamErr(mod, ver, upstreamGitHub, resp.StatusCode, detail, nil)
}

func credentialNote(tok string) string {
	if tok == "" {
		return "the proxy presented NO credential"
	}
	return "the proxy's credential was rejected"
}

// isRateLimited distinguishes GitHub's rate-limit 403 from an authorization 403.
func isRateLimited(resp *http.Response, detail string) bool {
	if resp.Header.Get("X-RateLimit-Remaining") == "0" {
		return true
	}
	if resp.Header.Get("Retry-After") != "" {
		return true
	}
	d := strings.ToLower(detail)
	return strings.Contains(d, "rate limit") || strings.Contains(d, "secondary rate")
}

func resetNote(resp *http.Response) string {
	v := resp.Header.Get("X-RateLimit-Reset")
	if v == "" {
		return ""
	}
	sec, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return ""
	}
	return fmt.Sprintf(" (resets %s)", time.Unix(sec, 0).UTC().Format(time.RFC3339))
}

// githubMessage pulls GitHub's own error message out of a response body, so the
// detail a caller sees is the upstream's words rather than our paraphrase.
func githubMessage(resp *http.Response) string {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && payload.Message != "" {
		return payload.Message
	}
	s := strings.TrimSpace(string(body))
	if s == "" {
		return http.StatusText(resp.StatusCode)
	}
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}

// listTags returns every tag in the repo. Version selection happens in the
// caller, which knows the module's tag prefix.
func (g *githubSource) listTags(ctx context.Context, ref repoRef, mod string) ([]tagRef, error) {
	var out []tagRef
	// matching-refs returns every tag with its object SHA in one paginated call,
	// and (unlike /tags) does not resolve annotated tags for us -- which is fine,
	// versions come from the ref names.
	for page := 1; page <= 20; page++ {
		url := fmt.Sprintf("%s/repos/%s/%s/git/matching-refs/tags?per_page=100&page=%d",
			githubAPI, ref.Owner, ref.Repo, page)
		resp, err := g.do(ctx, ref.Owner, ref.Repo, http.MethodGet, url, mod, "", "application/vnd.github+json")
		if err != nil {
			return nil, err
		}
		var refs []struct {
			Ref    string `json:"ref"`
			Object struct {
				SHA  string `json:"sha"`
				Type string `json:"type"`
			} `json:"object"`
		}
		err = json.NewDecoder(resp.Body).Decode(&refs)
		resp.Body.Close()
		if err != nil {
			return nil, upstreamErr(mod, "", upstreamGitHub, 200, "decoding tag list", err)
		}
		for _, r := range refs {
			out = append(out, tagRef{
				Name:      strings.TrimPrefix(r.Ref, "refs/tags/"),
				SHA:       r.Object.SHA,
				Annotated: r.Object.Type == "tag",
			})
		}
		if len(refs) < 100 {
			break
		}
	}
	return out, nil
}

// commitSHA resolves a tag ref to the commit it names. An annotated tag's ref
// points at a tag object rather than a commit, and handing that sha to anything
// expecting a commit (the tarball endpoint especially) fails -- so it is
// dereferenced through the git tag API first.
func (g *githubSource) commitSHA(ctx context.Context, ref repoRef, t tagRef, mod, ver string) (string, error) {
	if !t.Annotated {
		return t.SHA, nil
	}
	url := fmt.Sprintf("%s/repos/%s/%s/git/tags/%s", githubAPI, ref.Owner, ref.Repo, t.SHA)
	resp, err := g.do(ctx, ref.Owner, ref.Repo, http.MethodGet, url, mod, ver, "application/vnd.github+json")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var payload struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", upstreamErr(mod, ver, upstreamGitHub, 200, "decoding annotated tag", err)
	}
	if payload.Object.SHA == "" {
		return "", upstreamErr(mod, ver, upstreamGitHub, 200, "annotated tag carried no target sha", nil)
	}
	return payload.Object.SHA, nil
}

// defaultBranch reports the repo's default branch. Unlike
// auth.GitHubDefaultBranch (best-effort, "" on every failure) this surfaces the
// classified error: resolving @latest for a tagless module depends on it, and
// silently treating "I could not read the repo" as "no default branch" is how a
// credential failure disappears.
func (g *githubSource) defaultBranch(ctx context.Context, ref repoRef, mod string) (string, error) {
	url := fmt.Sprintf("%s/repos/%s/%s", githubAPI, ref.Owner, ref.Repo)
	resp, err := g.do(ctx, ref.Owner, ref.Repo, http.MethodGet, url, mod, "", "application/vnd.github+json")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var payload struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", upstreamErr(mod, "", upstreamGitHub, 200, "decoding repository", err)
	}
	if payload.DefaultBranch == "" {
		return "", upstreamErr(mod, "", upstreamGitHub, 200, "repository reported no default branch", nil)
	}
	return payload.DefaultBranch, nil
}

type commitInfo struct {
	SHA  string
	Time time.Time
}

func (g *githubSource) getCommit(ctx context.Context, ref repoRef, rev, mod, ver string) (commitInfo, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/commits/%s", githubAPI, ref.Owner, ref.Repo, rev)
	resp, err := g.do(ctx, ref.Owner, ref.Repo, http.MethodGet, url, mod, ver, "application/vnd.github+json")
	if err != nil {
		return commitInfo{}, err
	}
	defer resp.Body.Close()
	var payload struct {
		SHA    string `json:"sha"`
		Commit struct {
			Committer struct {
				Date time.Time `json:"date"`
			} `json:"committer"`
		} `json:"commit"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return commitInfo{}, upstreamErr(mod, ver, upstreamGitHub, 200, "decoding commit", err)
	}
	if payload.SHA == "" {
		return commitInfo{}, upstreamErr(mod, ver, upstreamGitHub, 200, "commit response carried no sha", nil)
	}
	return commitInfo{SHA: payload.SHA, Time: payload.Commit.Committer.Date.UTC()}, nil
}

// getFile reads one file at a revision. Returns (nil, nil) when the file is
// simply absent, which the caller distinguishes from a failure -- a module
// without a go.mod is legal and gets a synthesized one.
func (g *githubSource) getFile(ctx context.Context, ref repoRef, path, rev, mod, ver string) ([]byte, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/contents/%s?ref=%s",
		githubAPI, ref.Owner, ref.Repo, path, rev)
	resp, err := g.do(ctx, ref.Owner, ref.Repo, http.MethodGet, url, mod, ver, "application/vnd.github+json")
	if err != nil {
		var e *Error
		if errors.As(err, &e) && e.Kind == KindNotFound {
			return nil, nil
		}
		return nil, err
	}
	defer resp.Body.Close()
	var payload struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, upstreamErr(mod, ver, upstreamGitHub, 200, "decoding file", err)
	}
	if payload.Encoding != "base64" {
		return nil, upstreamErr(mod, ver, upstreamGitHub, 200,
			"unexpected content encoding "+payload.Encoding, nil)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(payload.Content, "\n", ""))
	if err != nil {
		return nil, upstreamErr(mod, ver, upstreamGitHub, 200, "decoding file content", err)
	}
	return raw, nil
}

// goModAt reads the module's go.mod at a revision, returning the declared module
// path alongside the bytes. Absent go.mod yields a synthesized one declaring
// modPath, which is what the module protocol requires a proxy to serve.
func (g *githubSource) goModAt(ctx context.Context, ref repoRef, rev, modPath, ver string) (content []byte, declared string, err error) {
	p := "go.mod"
	if ref.Dir != "" {
		p = ref.Dir + "/go.mod"
	}
	raw, err := g.getFile(ctx, ref, p, rev, modPath, ver)
	if err != nil {
		return nil, "", err
	}
	if raw == nil {
		return []byte("module " + modPath + "\n"), modPath, nil
	}
	return raw, modfile.ModulePath(raw), nil
}

// buildZip materializes the canonical module zip for modPath@version at rev.
//
// The zip is assembled by golang.org/x/mod/zip from an extracted tree rather
// than hand-rolled: it is the same code the go command validates against, so it
// enforces the canonical layout (the module@version/ prefix, excluded nested
// modules and vendor directories, the size limits). A hand-built zip that is
// merely close produces a checksum mismatch at every consumer.
//
// The tarball is spooled to disk and expanded to a temp tree, so peak memory is
// one buffer rather than the module's size.
func (g *githubSource) buildZip(ctx context.Context, ref repoRef, rev string, mv module.Version) (path string, size int64, err error) {
	url := fmt.Sprintf("%s/repos/%s/%s/tarball/%s", githubAPI, ref.Owner, ref.Repo, rev)
	resp, err := g.do(ctx, ref.Owner, ref.Repo, http.MethodGet, url, mv.Path, mv.Version, "application/vnd.github+json")
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	tree, err := os.MkdirTemp(g.tmpDir, "goproxy-src-")
	if err != nil {
		return "", 0, upstreamErr(mv.Path, mv.Version, upstreamGitHub, 0, "creating temp dir", err)
	}
	defer os.RemoveAll(tree)

	var body io.Reader = resp.Body
	if g.tarballLimit > 0 {
		body = io.LimitReader(resp.Body, g.tarballLimit)
	}
	if err := extractModuleTree(body, ref.Dir, tree); err != nil {
		return "", 0, upstreamErr(mv.Path, mv.Version, upstreamGitHub, 0, "extracting repository tarball", err)
	}

	f, err := os.CreateTemp(g.tmpDir, "goproxy-zip-")
	if err != nil {
		return "", 0, upstreamErr(mv.Path, mv.Version, upstreamGitHub, 0, "creating temp zip", err)
	}
	defer f.Close()
	if err := modzip.CreateFromDir(f, mv, tree); err != nil {
		os.Remove(f.Name())
		return "", 0, upstreamErr(mv.Path, mv.Version, upstreamGitHub, 0, "building module zip", err)
	}
	st, err := f.Stat()
	if err != nil {
		os.Remove(f.Name())
		return "", 0, upstreamErr(mv.Path, mv.Version, upstreamGitHub, 0, "sizing module zip", err)
	}
	return f.Name(), st.Size(), nil
}

// extractModuleTree writes the module directory out of a GitHub repository
// tarball into dest. GitHub wraps the repo in a single top-level directory whose
// name embeds a short SHA, so the first path element is stripped and only
// entries under subdir (the module root within the repo) are kept.
func extractModuleTree(r io.Reader, subdir, dest string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()

	want := ""
	if subdir != "" {
		want = strings.Trim(subdir, "/") + "/"
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		_, rest, ok := strings.Cut(hdr.Name, "/")
		if !ok || rest == "" {
			continue
		}
		rel, ok := strings.CutPrefix(rest, want)
		if !ok || rel == "" {
			continue
		}
		// modzip only reads regular files and directories; skipping everything
		// else here also drops symlinks, which must never be followed out of the
		// extraction root.
		switch hdr.Typeflag {
		case tar.TypeDir, tar.TypeReg:
		default:
			continue
		}
		out, err := safeJoin(dest, rel)
		if err != nil {
			return err
		}
		if hdr.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(out, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		if err := writeFile(out, tr, hdr.Size); err != nil {
			return err
		}
	}
	return nil
}

// safeJoin resolves rel under root, refusing anything that would escape it. A
// tarball is untrusted input; "../" entries in one are how an archive writes
// outside its extraction directory.
func safeJoin(root, rel string) (string, error) {
	out := filepath.Join(root, filepath.FromSlash(rel))
	if out != root && !strings.HasPrefix(out, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("tar entry %q escapes the extraction root", rel)
	}
	return out, nil
}

func writeFile(path string, r io.Reader, size int64) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, io.LimitReader(r, size)); err != nil {
		return err
	}
	return f.Close()
}
