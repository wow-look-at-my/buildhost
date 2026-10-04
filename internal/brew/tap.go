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
// re-roots all subsequent requests on the redirect target).
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
func (h *Handler) ServePrivateTap(w http.ResponseWriter, r *http.Request) {
	if auth.TokenFrom(r.Context()) == nil {
		w.Header().Set("Www-Authenticate", `Basic realm="buildhost"`)
		w.Header().Set("Cache-Control", "private, no-store")
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	h.serveTapFile(w, r)
}

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
// projects that credential can read.
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
func (h *Handler) buildTapFiles(r *http.Request) (map[string][]byte, error) {
	visible, err := h.tapVisibleProjects(r)
	if err != nil {
		return nil, err
	}
<<<<<<< HEAD
	files := map[string][]byte{
		repackage.BrewPrivateStrategyPath: []byte(repackage.BrewPrivateStrategy),
	}
	budget := tapInlineDigestBudget
	var formulas []string
=======
	files := map[string][]byte{}
>>>>>>> origin/master
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
		out, err := h.formulaForRelease(r.Context(), project, *release, artifacts, auth.RequestRootURL(r), formulaLatest)
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
		files[repackage.BrewFormulaPath(project.Name)] = data
<<<<<<< HEAD
		formulas = append(formulas, project.Name)

		if err := h.addVersionedFormulas(r, project, files, &budget); err != nil {
			return nil, err
		}
=======
>>>>>>> origin/master
	}
	addRootAliases(files, formulas)

	return files, nil
}

<<<<<<< HEAD
// tapAliasDir holds Homebrew's tap aliases. Brew reads an alias only as a symlink to a formula file.
const tapAliasDir = "Aliases"

// addRootAliases makes `brew install <tap>/<root>` install the formula nested
// under a root that has no formula of its own. A repo whose only binary is
// not named after it publishes as "<repo>/<binary>", so the repo name alone
// needs this alias. A root with several nested formulas is ambiguous and gets
// no alias.
func addRootAliases(files map[string][]byte, formulas []string) {
	nested := map[string][]string{}
	for _, name := range formulas {
		for i := len(name) - 1; i > 0; i-- {
			if name[i] == '/' {
				nested[name[:i]] = append(nested[name[:i]], name)
			}
		}
	}
	for root, names := range nested {
		if len(names) != 1 {
			continue
		}
		if _, taken := files[repackage.BrewFormulaPath(root)]; taken {
			continue
		}
		files[tapAliasDir+"/"+repackage.BrewFormulaName(root)] = []byte("../" + repackage.BrewFormulaPath(names[0]))
	}
}

// addVersionedFormulas adds one name@version formula per published release on
// the project's default branch. Uncached digests are computed while budget
// lasts; a release still missing one after that is left out of this build
// and its digests are filled in the background.
func (h *Handler) addVersionedFormulas(r *http.Request, project db.Project, files map[string][]byte, budget *int) error {
	releases, err := h.DB.ListPublishedReleasesOnDefaultBranch(r.Context(), project.ID)
	if err != nil {
		return err
	}
	// Newest first: on a case-insensitive filesystem (macOS) a couple of versions that differ only by case would collide in the clone.
	seen := set.New[string]()
	for _, release := range releases {
		version := brewVersion(release)
		path := repackage.BrewVersionedFormulaPath(project.Name, version)
		if seen.Contains(strings.ToLower(path)) {
			continue
		}
		artifacts, err := h.DB.ListArtifactsByPlatform(r.Context(), release.ID)
		if err != nil {
			return err
		}
		out, err := h.formulaForRelease(r.Context(), project, release, artifacts, auth.RequestRootURL(r), formulaVersionedBudgeted, budget)
		if errors.Is(err, db.ErrNotFound) {
			continue
		}
		// A pending newer release still claims its path.
		seen.Add(strings.ToLower(path))
		if errors.Is(err, errDigestPending) {
			continue
		}
		if err != nil {
			return err
		}
		data, err := io.ReadAll(out.Reader)
		if err != nil {
			return err
		}
		files[path] = data
	}
	return nil
}

func buildGitObjects(files map[string][]byte, parent string) (objects map[string][]byte, commitSHA, rootTreeSHA string) {
	objects = map[string][]byte{}
	rootTreeSHA = writeGitTree(objects, files, true, gitModeFile)
=======
// tapHistoryFormula is one past release's formula, at the path the tap's
// latest formula for that project uses.
type tapHistoryFormula struct {
	path      string
	data      []byte
	releaseID int64
}

// tapHistory renders every published default-branch release of every visible
// project, oldest first. A release whose digests are not cached yet is left
// out and queued on the background filler: a tap request never hashes one.
func (h *Handler) tapHistory(r *http.Request) ([]tapHistoryFormula, error) {
	visible, err := h.tapVisibleProjects(r)
	if err != nil {
		return nil, err
	}
	var history []tapHistoryFormula
	for _, project := range visible {
		releases, err := h.DB.ListPublishedReleasesOnDefaultBranch(r.Context(), project.ID)
		if err != nil {
			return nil, err
		}
		for _, release := range releases {
			artifacts, err := h.DB.ListArtifactsByPlatform(r.Context(), release.ID)
			if err != nil {
				return nil, err
			}
			out, err := h.formulaForRelease(r.Context(), project, release, artifacts, auth.RequestRootURL(r), formulaHistory)
			if errors.Is(err, db.ErrNotFound) || errors.Is(err, errDigestPending) {
				continue
			}
			if err != nil {
				return nil, err
			}
			data, err := io.ReadAll(out.Reader)
			if err != nil {
				return nil, err
			}
			history = append(history, tapHistoryFormula{path: repackage.BrewFormulaPath(project.Name), data: data, releaseID: release.ID})
		}
	}
	sort.Slice(history, func(i, j int) bool { return history[i].releaseID < history[j].releaseID })
	return history, nil
}
>>>>>>> origin/master

// gitObjects collects the loose objects of the commits one refresh appends.
type gitObjects map[string][]byte

// blobs adds each file's blob and returns the path -> blob sha map.
func (o gitObjects) blobs(files map[string][]byte) map[string]string {
	shas := make(map[string]string, len(files))
	for path, body := range files {
		shas[path] = addGitObject(o, "blob", body)
	}
	return shas
}

// commit adds the tree for blobs and a commit of it with parent, and returns
// the commit and tree shas.
func (o gitObjects) commit(blobs map[string]string, parent string) (commitSHA, treeSHA string) {
	treeSHA = o.tree(blobs)
	var commit bytes.Buffer
	fmt.Fprintf(&commit, "tree %s\n", treeSHA)
	if parent != "" {
		fmt.Fprintf(&commit, "parent %s\n", parent)
	}
	commit.WriteString("author buildhost <buildhost@localhost> 0 +0000\ncommitter buildhost <buildhost@localhost> 0 +0000\n\nUpdate Homebrew tap\n")
	return addGitObject(o, "commit", commit.Bytes()), treeSHA
}

<<<<<<< HEAD
const (
	gitModeFile    = "100644"
	gitModeSymlink = "120000"
)

// writeGitTree adds the blobs and trees for files (slash-separated paths
// relative to this tree) to objects, at any depth, and returns the tree's sha.
// A file in the root's tapAliasDir is a symlink, and its body is the link target.
func writeGitTree(objects map[string][]byte, files map[string][]byte, root bool, mode string) string {
=======
// tree adds the trees for blobs (slash-separated paths relative to this tree
// -> blob sha), at any depth, and returns the tree's sha.
func (o gitObjects) tree(blobs map[string]string) string {
>>>>>>> origin/master
	var entries []gitTreeEntry
	subdirs := map[string]map[string]string{}
	for name, sha := range blobs {
		dir, rest, nested := strings.Cut(name, "/")
		if !nested {
<<<<<<< HEAD
			entries = append(entries, gitTreeEntry{Mode: mode, Name: name, SHA: addGitObject(objects, "blob", body)})
=======
			entries = append(entries, gitTreeEntry{Mode: "100644", Name: name, SHA: sha})
>>>>>>> origin/master
			continue
		}
		if subdirs[dir] == nil {
			subdirs[dir] = map[string]string{}
		}
		subdirs[dir][rest] = sha
	}
	for dir, sub := range subdirs {
<<<<<<< HEAD
		subMode := mode
		if root && dir == tapAliasDir {
			subMode = gitModeSymlink
		}
		entries = append(entries, gitTreeEntry{Mode: "40000", Name: dir, SHA: writeGitTree(objects, sub, false, subMode)})
=======
		entries = append(entries, gitTreeEntry{Mode: "40000", Name: dir, SHA: o.tree(sub)})
>>>>>>> origin/master
	}
	return addGitObject(o, "tree", gitTree(entries))
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

func addGitObject(objects gitObjects, kind string, body []byte) string {
	raw := append([]byte(fmt.Sprintf("%s %d\x00", kind, len(body))), body...)
	sum := sha1.Sum(raw)
	sha := hex.EncodeToString(sum[:])
	if _, ok := objects[sha]; ok {
		return sha
	}

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
