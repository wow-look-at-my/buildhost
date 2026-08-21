package brew

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	mmap "github.com/wow-look-at-my/go-mmap"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/repackage"
)

// RedirectTap handles brew.{domain}/tap.git. An anonymous request is
// permanently redirected to the public tap on the git subdomain, exactly as
// before. A request that carries a valid credential is served IN PLACE
// instead: clients drop credentials when following a cross-host redirect (git
// re-roots all subsequent requests on the redirect target), so redirecting an
// authenticated tap request would silently downgrade it to the public tap.
func (h *Handler) RedirectTap(w http.ResponseWriter, r *http.Request) {
	if auth.TokenFrom(r.Context()) != nil {
		h.serveTapFile(w, r)
		return
	}
	target := &url.URL{
		Scheme:   auth.RequestScheme(r),
		Host:     "git." + domainFromRequest(r),
		Path:     "/brew/tap.git" + tapSuffix(r),
		RawQuery: r.URL.RawQuery,
	}
	http.Redirect(w, r, target.String(), http.StatusMovedPermanently)
}

// ServePrivateTap handles brew.{domain}/private/tap.git -- the authenticated
// tap. An anonymous request gets a 401 Basic challenge rather than public
// content: git does NOT send URL-embedded credentials preemptively (it waits
// for a challenge), so answering 200 here would make a credentialed
// `brew tap x:TOKEN@.../private/tap.git` silently ingest the public-only tap
// and the user would never learn their token was dropped. The challenge is
// what makes the standard creds-in-URL private-tap pattern work at all.
func (h *Handler) ServePrivateTap(w http.ResponseWriter, r *http.Request) {
	if auth.TokenFrom(r.Context()) == nil {
		w.Header().Set("Www-Authenticate", `Basic realm="buildhost"`)
		w.Header().Set("Cache-Control", "private, no-store")
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	h.serveTapFile(w, r)
}

// ServeTap serves one file of the dumb-HTTP git tap on the git subdomain from
// the scope's persistent lineage store (refreshed at most once per tapCacheTTL,
// see tapcache.go/taphistory.go) by memory-mapping it -- never buffering the
// file on the heap and never rebuilding the whole tap per object request. The
// store is append-only with fast-forward-only refs, so a publish landing
// mid-`brew update` can never orphan the refs a client already fetched.
func (h *Handler) ServeTap(w http.ResponseWriter, r *http.Request) {
	h.serveTapFile(w, r)
}

// serveTapFile is the shared tap file server. The tap contents are scoped to
// the request's credential (public projects only when anonymous; plus the
// private projects the credential can read otherwise), and a credentialed
// response is marked uncacheable for shared caches -- its body depends on the
// Authorization header, and the live deployment sits behind a CDN.
func (h *Handler) serveTapFile(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(tapSuffix(r), "/")
	if path == "" {
		path = "HEAD"
	}

	f, err := h.openTapFile(r, path)
	if errors.Is(err, errTapBuild) {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err != nil {
		// Not in the snapshot (or an escaping path the os.Root refused).
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}

	if auth.TokenFrom(r.Context()) != nil {
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Vary", "Authorization")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	if strings.HasPrefix(path, "objects/") {
		w.Header().Set("Content-Type", "application/x-git-loose-object")
	} else {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}

	// A zero-length file (objects/info/packs) has nothing to map -- mmap
	// rejects an empty region, same as the storage layer's empty-blob case.
	if info.Size() == 0 {
		w.Header().Set("Content-Length", "0")
		return
	}

	m, err := mmap.MapRegion(int(f.Fd()), info.Size(), mmap.ProtRead, mmap.MapShared, 0)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	_ = m.Advise(mmap.AdvSequential)
	rc := mmap.NewReader(m) // Close unmaps
	defer rc.Close()
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	io.Copy(w, rc)
}

// tapScopeKey returns the snapshot-cache key component identifying the
// request's credential. Every anonymous request shares one scope; a DB token
// keys by its unique ID; an OIDC synthetic token (always ID -1) keys by its
// subject-derived name plus its policy project and namespace restriction, so
// two distinct OIDC identities can never collide onto one cached tap. Keying
// by credential -- not by the resulting project set -- means a cache hit costs
// no DB work and each scope keeps the "one consistent snapshot per TTL"
// property the cache exists for (a publish mid-`brew update` must not swap
// refs/objects out from under the client).
func tapScopeKey(ctx context.Context) string {
	t := auth.TokenFrom(ctx)
	if t == nil {
		return "anon"
	}
	pid := int64(0)
	if t.ProjectID != nil {
		pid = *t.ProjectID
	}
	return fmt.Sprintf("tok\x00%d\x00%s\x00%d\x00%s", t.ID, t.Name, pid, auth.OIDCProjectFrom(ctx))
}

// tapVisibleProjects computes the projects the request may see in a tap: every
// public project, plus -- when the request carries a credential -- the private
// projects that credential can read. The visibility rule is
// auth.TokenCanReadProject, the same one requireProject applies to
// single-project reads, so a private project name can never leak into a tap
// its token could not read directly. Evaluated once per snapshot BUILD; cached
// snapshots are keyed by credential (tapScopeKey), never shared across scopes.
func (h *Handler) tapVisibleProjects(r *http.Request) ([]db.Project, error) {
	projects, err := h.DB.ListProjects(r.Context())
	if err != nil {
		return nil, err
	}
	visible := make([]db.Project, 0, len(projects))
	for _, p := range projects {
		if auth.TokenCanReadProject(r.Context(), &p) {
			visible = append(visible, p)
		}
	}
	return visible, nil
}

// buildTapFiles assembles the tap's working-tree contents for the request's
// scope: one formula per visible project (folded filename) plus the private
// download strategy library.
func (h *Handler) buildTapFiles(r *http.Request) (map[string][]byte, error) {
	visible, err := h.tapVisibleProjects(r)
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{
		// Always ship the private-download strategy so the tap layout is
		// uniform across scopes; it contains no secrets and public-only taps
		// simply never reference it.
		repackage.BrewPrivateStrategyPath: []byte(repackage.BrewPrivateStrategy),
	}
	for _, project := range visible {
		release, err := h.DB.GetLatestRelease(r.Context(), project.ID)
		if err != nil {
			if errors.Is(err, db.ErrNotFound) {
				continue
			}
			return nil, err
		}
		artifacts, err := h.DB.ListArtifactsByPlatform(r.Context(), release.ID)
		if err != nil {
			return nil, err
		}
		out, err := h.formulaForRelease(r.Context(), project, *release, artifacts, auth.RequestRootURL(r))
		if err != nil {
			if errors.Is(err, db.ErrNotFound) {
				continue
			}
			return nil, err
		}
		data, err := io.ReadAll(out.Reader)
		if err != nil {
			return nil, err
		}
		files["Formula/"+tapFormulaName(project.Name)+".rb"] = data
	}

	return files, nil
}

// buildGitObjects materializes files (keyed by repo-relative path, at most one
// directory deep, e.g. "Formula/x.rb" or "lib/y.rb") as loose git objects:
// blobs, trees, and one commit. When parent is non-empty it is recorded as the
// commit's parent, so a lineage's history only ever grows forward -- the
// fast-forward guarantee `brew update` depends on (its updater rebases the
// client's clone onto origin/main; an unrelated new root commit wedges every
// client in add/add conflicts). Object contents are deterministic (zero
// timestamps, fixed identity), so identical (files, parent) inputs produce
// identical SHAs across builds, restarts, and redeploys.
func buildGitObjects(files map[string][]byte, parent string) (objects map[string][]byte, commitSHA, rootTreeSHA string) {
	objects = map[string][]byte{}
	byDir := map[string][]gitTreeEntry{}
	var rootFiles []gitTreeEntry

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		blobSHA := addGitObject(objects, "blob", files[name])
		dir, base, nested := strings.Cut(name, "/")
		if nested {
			byDir[dir] = append(byDir[dir], gitTreeEntry{Mode: "100644", Name: base, SHA: blobSHA})
		} else {
			rootFiles = append(rootFiles, gitTreeEntry{Mode: "100644", Name: name, SHA: blobSHA})
		}
	}

	rootEntries := rootFiles
	for _, dir := range sortedKeys(byDir) {
		treeSHA := addGitObject(objects, "tree", gitTree(byDir[dir]))
		rootEntries = append(rootEntries, gitTreeEntry{Mode: "40000", Name: dir, SHA: treeSHA})
	}
	rootTreeSHA = addGitObject(objects, "tree", gitTree(rootEntries))

	var commit bytes.Buffer
	fmt.Fprintf(&commit, "tree %s\n", rootTreeSHA)
	if parent != "" {
		fmt.Fprintf(&commit, "parent %s\n", parent)
	}
	commit.WriteString("author buildhost <buildhost@localhost> 0 +0000\ncommitter buildhost <buildhost@localhost> 0 +0000\n\nUpdate Homebrew tap\n")
	commitSHA = addGitObject(objects, "commit", commit.Bytes())

	return objects, commitSHA, rootTreeSHA
}

func sortedKeys(m map[string][]gitTreeEntry) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

type gitTreeEntry struct {
	Mode string
	Name string
	SHA  string
}

// gitTree serializes tree entries in git's canonical order: byte-wise by name,
// with a directory sorting as if its name carried a trailing "/" (git's tree
// comparison rule; a wrongly ordered tree fails fsck and confuses clients).
func gitTree(entries []gitTreeEntry) []byte {
	sortKey := func(e gitTreeEntry) string {
		if e.Mode == "40000" {
			return e.Name + "/"
		}
		return e.Name
	}
	sort.Slice(entries, func(i, j int) bool { return sortKey(entries[i]) < sortKey(entries[j]) })
	var buf bytes.Buffer
	for _, entry := range entries {
		buf.WriteString(entry.Mode)
		buf.WriteByte(' ')
		buf.WriteString(entry.Name)
		buf.WriteByte(0)
		raw, _ := hex.DecodeString(entry.SHA)
		buf.Write(raw)
	}
	return buf.Bytes()
}

func addGitObject(objects map[string][]byte, kind string, body []byte) string {
	raw := append([]byte(fmt.Sprintf("%s %d\x00", kind, len(body))), body...)
	sum := sha1.Sum(raw)
	sha := hex.EncodeToString(sum[:])

	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	zw.Write(raw)
	zw.Close()
	objects[sha] = compressed.Bytes()
	return sha
}

func tapSuffix(r *http.Request) string {
	if path := r.PathValue("path"); path != "" {
		return "/" + path
	}
	for _, prefix := range []string{"/private/tap.git", "/tap.git", "/brew/tap.git"} {
		if strings.HasPrefix(r.URL.Path, prefix) {
			return strings.TrimPrefix(r.URL.Path, prefix)
		}
	}
	return ""
}

func tapFormulaName(project string) string {
	return repackage.BrewFormulaName(project)
}

func domainFromRequest(r *http.Request) string {
	host := r.Host
	port := ""
	if i := strings.LastIndex(host, ":"); i >= 0 {
		port = host[i:]
		host = host[:i]
	}
	if dot := strings.IndexByte(host, '.'); dot > 0 {
		host = host[dot+1:]
	}
	return host + port
}
