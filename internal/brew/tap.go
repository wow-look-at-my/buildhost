package brew

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/wow-look-at-my/go-containers/set"
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
// the request's credential (public projects only when anonymous. Plus the
// private projects the credential can read otherwise), and a credentialed
// response is marked uncacheable for shared caches. Its body depends on the
// Authorization header. The live deployment sits behind a CDN.
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

// tapVisibleProjects computes the projects the request may see in a tap. Those
// are every public project, plus -- when the request carries a credential --
// the private projects that credential can read.
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
	files, _, err := h.buildTap(r)
	return files, err
}

// buildTap returns the tap's files and the formula name of each project in it.
func (h *Handler) buildTap(r *http.Request) (map[string][]byte, map[string]string, error) {
	visible, err := h.tapVisibleProjects(r)
	if err != nil {
		return nil, nil, err
	}
	type latest struct {
		project   db.Project
		release   db.Release
		artifacts []db.PlatformArtifact
	}
	var candidates []latest
	rendered := map[string][]byte{}
	for _, project := range visible {
		release, err := h.DB.GetLatestRelease(r.Context(), project.ID)
		if err != nil {
			if errors.Is(err, db.ErrNotFound) {
				continue
			}
			return nil, nil, err
		}
		artifacts, err := h.DB.ListArtifactsByPlatform(r.Context(), release.ID)
		if err != nil {
			return nil, nil, err
		}
		data, err := h.renderFormula(r, project, *release, artifacts, repackage.BrewFormulaName(project.Name), formulaLatest)
		if errors.Is(err, db.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		candidates = append(candidates, latest{project, *release, artifacts})
		rendered[project.Name] = data
	}

	projects := make([]string, 0, len(candidates))
	for _, c := range candidates {
		projects = append(projects, c.project.Name)
	}
	names := tapFormulaNames(projects)

	files := map[string][]byte{}
	renames := map[string]string{}
	for _, c := range candidates {
		name := names[c.project.Name]
		data := rendered[c.project.Name]
		if folded := repackage.BrewFormulaName(c.project.Name); name != folded {
			renames[folded] = name
			if data, err = h.renderFormula(r, c.project, c.release, c.artifacts, name, formulaLatest); err != nil {
				return nil, nil, err
			}
		}
		files["Formula/"+name+".rb"] = data
	}
	if len(renames) > 0 {
		body, err := json.MarshalIndent(renames, "", "  ")
		if err != nil {
			return nil, nil, err
		}
		files[tapRenamesFile] = append(body, '\n')
	}
	return files, names, nil
}

// tapRenamesFile maps an old formula name to its current one. brew install resolves an old name through it.
const tapRenamesFile = "formula_renames.json"

// renderFormula renders one formula under the tap name formula.
func (h *Handler) renderFormula(r *http.Request, project db.Project, release db.Release, artifacts []db.PlatformArtifact, formula string, mode formulaMode) ([]byte, error) {
	out, err := h.formulaForRelease(r.Context(), project, release, artifacts, auth.RequestRootURL(r), formula, mode)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(out.Reader)
}

// tapFormulaNames maps each project with a formula to its tap formula name. The name is the project's folded name, except for the only formula under a root that
// has no formula of its own. That formula takes the topmost such root's folded name. A repo whose one binary is not named after it publishes as
// "<repo>/<binary>", and installs as "<repo>". A root name that another formula already folds to stays with that formula.
func tapFormulaNames(projects []string) map[string]string {
	own := set.New[string]()
	folded := set.New[string]()
	nested := map[string]int{}
	for _, p := range projects {
		own.Add(p)
		folded.Add(repackage.BrewFormulaName(p))
		for i := 1; i < len(p); i++ {
			if p[i] == '/' {
				nested[p[:i]]++
			}
		}
	}
	names := make(map[string]string, len(projects))
	for _, p := range projects {
		names[p] = repackage.BrewFormulaName(p)
		for i := 1; i < len(p); i++ {
			if p[i] != '/' {
				continue
			}
			root := p[:i]
			if own.Contains(root) || nested[root] != 1 || folded.Contains(repackage.BrewFormulaName(root)) {
				continue
			}
			names[p] = repackage.BrewFormulaName(root)
			break
		}
	}
	return names
}

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
// names is the formula name of each project in the tap; any other project keeps its folded name.
func (h *Handler) tapHistory(r *http.Request, names map[string]string) ([]tapHistoryFormula, error) {
	visible, err := h.tapVisibleProjects(r)
	if err != nil {
		return nil, err
	}
	var history []tapHistoryFormula
	for _, project := range visible {
		name, ok := names[project.Name]
		if !ok {
			name = repackage.BrewFormulaName(project.Name)
		}
		releases, err := h.DB.ListPublishedReleasesOnDefaultBranch(r.Context(), project.ID)
		if err != nil {
			return nil, err
		}
		for _, release := range releases {
			artifacts, err := h.DB.ListArtifactsByPlatform(r.Context(), release.ID)
			if err != nil {
				return nil, err
			}
			data, err := h.renderFormula(r, project, release, artifacts, name, formulaHistory)
			if errors.Is(err, db.ErrNotFound) || errors.Is(err, errDigestPending) {
				continue
			}
			if err != nil {
				return nil, err
			}
			history = append(history, tapHistoryFormula{path: "Formula/" + name + ".rb", data: data, releaseID: release.ID})
		}
	}
	sort.Slice(history, func(i, j int) bool { return history[i].releaseID < history[j].releaseID })
	return history, nil
}

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

// tree adds the trees for blobs (slash-separated paths relative to this tree
// -> blob sha), at any depth, and returns the tree's sha.
func (o gitObjects) tree(blobs map[string]string) string {
	var entries []gitTreeEntry
	subdirs := map[string]map[string]string{}
	for name, sha := range blobs {
		dir, rest, nested := strings.Cut(name, "/")
		if !nested {
			entries = append(entries, gitTreeEntry{Mode: "100644", Name: name, SHA: sha})
			continue
		}
		if subdirs[dir] == nil {
			subdirs[dir] = map[string]string{}
		}
		subdirs[dir][rest] = sha
	}
	for dir, sub := range subdirs {
		entries = append(entries, gitTreeEntry{Mode: "40000", Name: dir, SHA: o.tree(sub)})
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
