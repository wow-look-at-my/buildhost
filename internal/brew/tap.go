package brew

import (
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

func (h *Handler) RedirectTap(w http.ResponseWriter, r *http.Request) {
	target := &url.URL{
		Scheme:   auth.RequestScheme(r),
		Host:     "git." + domainFromRequest(r),
		Path:     "/brew/tap.git" + tapSuffix(r),
		RawQuery: r.URL.RawQuery,
	}
	http.Redirect(w, r, target.String(), http.StatusMovedPermanently)
}

func (h *Handler) ServeTap(w http.ResponseWriter, r *http.Request) {
	repo, err := h.buildTapRepo(r)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	path := strings.TrimPrefix(tapSuffix(r), "/")
	if path == "" {
		path = "HEAD"
	}
	data, ok := repo[path]
	if !ok {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Cache-Control", "no-cache")
	if strings.HasPrefix(path, "objects/") {
		w.Header().Set("Content-Type", "application/x-git-loose-object")
	} else {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	w.Write(data)
}

func (h *Handler) buildTapRepo(r *http.Request) (map[string][]byte, error) {
	projects, err := h.DB.ListProjects(r.Context())
	if err != nil {
		return nil, err
	}

	formulas := map[string][]byte{}
	for _, project := range projects {
		if project.IsPrivate {
			continue
		}
		release, err := h.DB.GetLatestRelease(r.Context(), project.ID)
		if err != nil {
			if errors.Is(err, db.ErrNotFound) {
				continue
			}
			return nil, err
		}
		artifacts, err := h.DB.ListArtifacts(r.Context(), release.ID)
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
		formulas["Formula/"+tapFormulaName(project.Name)+".rb"] = data
	}

	return buildGitRepo(formulas), nil
}

func buildGitRepo(files map[string][]byte) map[string][]byte {
	objects := map[string][]byte{}
	rootEntries := []gitTreeEntry{}
	formulaEntries := []gitTreeEntry{}

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		blobSHA := addGitObject(objects, "blob", files[name])
		formulaName := strings.TrimPrefix(name, "Formula/")
		formulaEntries = append(formulaEntries, gitTreeEntry{Mode: "100644", Name: formulaName, SHA: blobSHA})
	}

	formulaTreeSHA := addGitObject(objects, "tree", gitTree(formulaEntries))
	rootEntries = append(rootEntries, gitTreeEntry{Mode: "40000", Name: "Formula", SHA: formulaTreeSHA})
	rootTreeSHA := addGitObject(objects, "tree", gitTree(rootEntries))

	commit := []byte(fmt.Sprintf("tree %s\nauthor buildhost <buildhost@localhost> 0 +0000\ncommitter buildhost <buildhost@localhost> 0 +0000\n\nUpdate Homebrew tap\n", rootTreeSHA))
	commitSHA := addGitObject(objects, "commit", commit)

	repo := map[string][]byte{
		"HEAD":               []byte("ref: refs/heads/main\n"),
		"refs/heads/main":    []byte(commitSHA + "\n"),
		"info/refs":          []byte(commitSHA + "\trefs/heads/main\n"),
		"objects/info/packs": []byte(""),
	}
	for sha, data := range objects {
		repo["objects/"+sha[:2]+"/"+sha[2:]] = data
	}
	return repo
}

type gitTreeEntry struct {
	Mode string
	Name string
	SHA  string
}

func gitTree(entries []gitTreeEntry) []byte {
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
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
	if strings.HasPrefix(r.URL.Path, "/tap.git") {
		return strings.TrimPrefix(r.URL.Path, "/tap.git")
	}
	if strings.HasPrefix(r.URL.Path, "/brew/tap.git") {
		return strings.TrimPrefix(r.URL.Path, "/brew/tap.git")
	}
	return ""
}

func tapFormulaName(project string) string {
	return strings.ReplaceAll(project, "/", "-")
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
