package brew

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"crypto/sha1"
	"encoding/binary"
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
	data, ok := repo.Loose[path]
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

func (h *Handler) ServeUploadPackInfoRefs(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("service") != "git-upload-pack" {
		http.Error(w, "unsupported service", http.StatusForbidden)
		return
	}

	repo, err := h.buildTapRepo(r)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(repo.Advertisement)
}

func (h *Handler) ServeUploadPack(w http.ResponseWriter, r *http.Request) {
	repo, err := h.buildTapRepo(r)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	body, err := readUploadPackRequest(r)
	if err != nil {
		http.Error(w, "bad upload-pack request", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/x-git-upload-pack-result")
	w.Header().Set("Cache-Control", "no-cache")
	shallow := wantsShallow(body)
	if shallow && !uploadPackDone(body) {
		w.Write(uploadPackShallowResult(repo.CommitSHA))
		return
	}
	w.Write(uploadPackResult(repo.CommitSHA, repo.Pack, wantsSideBand(body), shallow))
}

func (h *Handler) buildTapRepo(r *http.Request) (*tapRepo, error) {
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

type tapRepo struct {
	CommitSHA     string
	Advertisement []byte
	Pack          []byte
	Loose         map[string][]byte
}

type gitObject struct {
	Kind string
	Type byte
	Body []byte
	SHA  string
}

func buildGitRepo(files map[string][]byte) *tapRepo {
	looseObjects := map[string][]byte{}
	var packObjects []gitObject
	rootEntries := []gitTreeEntry{}
	formulaEntries := []gitTreeEntry{}

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		blob := addGitObject(looseObjects, "blob", files[name])
		packObjects = append(packObjects, blob)
		formulaName := strings.TrimPrefix(name, "Formula/")
		formulaEntries = append(formulaEntries, gitTreeEntry{Mode: "100644", Name: formulaName, SHA: blob.SHA})
	}

	formulaTree := addGitObject(looseObjects, "tree", gitTree(formulaEntries))
	packObjects = append(packObjects, formulaTree)
	rootEntries = append(rootEntries, gitTreeEntry{Mode: "40000", Name: "Formula", SHA: formulaTree.SHA})
	rootTree := addGitObject(looseObjects, "tree", gitTree(rootEntries))
	packObjects = append(packObjects, rootTree)

	commitBody := []byte(fmt.Sprintf("tree %s\nauthor buildhost <buildhost@localhost> 0 +0000\ncommitter buildhost <buildhost@localhost> 0 +0000\n\nUpdate Homebrew tap\n", rootTree.SHA))
	commit := addGitObject(looseObjects, "commit", commitBody)
	packObjects = append([]gitObject{commit}, packObjects...)

	repo := map[string][]byte{
		"HEAD":               []byte("ref: refs/heads/main\n"),
		"refs/heads/main":    []byte(commit.SHA + "\n"),
		"info/refs":          []byte(commit.SHA + "\trefs/heads/main\n"),
		"objects/info/packs": []byte(""),
	}
	for sha, data := range looseObjects {
		repo["objects/"+sha[:2]+"/"+sha[2:]] = data
	}
	return &tapRepo{
		CommitSHA:     commit.SHA,
		Advertisement: uploadPackAdvertisement(commit.SHA),
		Pack:          buildPackfile(packObjects),
		Loose:         repo,
	}
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

func addGitObject(objects map[string][]byte, kind string, body []byte) gitObject {
	raw := append([]byte(fmt.Sprintf("%s %d\x00", kind, len(body))), body...)
	sum := sha1.Sum(raw)
	sha := hex.EncodeToString(sum[:])

	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	zw.Write(raw)
	zw.Close()
	objects[sha] = compressed.Bytes()
	return gitObject{Kind: kind, Type: gitObjectType(kind), Body: body, SHA: sha}
}

func gitObjectType(kind string) byte {
	switch kind {
	case "commit":
		return 1
	case "tree":
		return 2
	case "blob":
		return 3
	default:
		panic("unsupported git object type: " + kind)
	}
}

func buildPackfile(objects []gitObject) []byte {
	var buf bytes.Buffer
	buf.WriteString("PACK")
	binary.Write(&buf, binary.BigEndian, uint32(2))
	binary.Write(&buf, binary.BigEndian, uint32(len(objects)))
	for _, obj := range objects {
		buf.Write(packObjectHeader(obj.Type, len(obj.Body)))
		var compressed bytes.Buffer
		zw := zlib.NewWriter(&compressed)
		zw.Write(obj.Body)
		zw.Close()
		buf.Write(compressed.Bytes())
	}
	sum := sha1.Sum(buf.Bytes())
	buf.Write(sum[:])
	return buf.Bytes()
}

func packObjectHeader(typeCode byte, size int) []byte {
	first := byte(size&0x0f) | typeCode<<4
	size >>= 4
	if size != 0 {
		first |= 0x80
	}
	out := []byte{first}
	for size != 0 {
		b := byte(size & 0x7f)
		size >>= 7
		if size != 0 {
			b |= 0x80
		}
		out = append(out, b)
	}
	return out
}

func uploadPackAdvertisement(commitSHA string) []byte {
	var buf bytes.Buffer
	buf.Write(pktLineString("# service=git-upload-pack\n"))
	buf.WriteString("0000")
	buf.Write(pktLineString(commitSHA + " HEAD\x00multi_ack multi_ack_detailed thin-pack side-band side-band-64k ofs-delta shallow deepen-since deepen-not symref=HEAD:refs/heads/main agent=buildhost\n"))
	buf.Write(pktLineString(commitSHA + " refs/heads/main\n"))
	buf.WriteString("0000")
	return buf.Bytes()
}

func readUploadPackRequest(r *http.Request) ([]byte, error) {
	reader := r.Body
	if strings.EqualFold(r.Header.Get("Content-Encoding"), "gzip") {
		gr, err := gzip.NewReader(r.Body)
		if err != nil {
			return nil, err
		}
		defer gr.Close()
		reader = gr
	}
	defer r.Body.Close()
	return io.ReadAll(io.LimitReader(reader, 10<<20))
}

func wantsSideBand(body []byte) bool {
	return bytes.Contains(body, []byte("side-band"))
}

func wantsShallow(body []byte) bool {
	return bytes.Contains(body, []byte("deepen"))
}

func uploadPackDone(body []byte) bool {
	return bytes.Contains(body, []byte("done"))
}

func uploadPackShallowResult(commitSHA string) []byte {
	var buf bytes.Buffer
	buf.Write(pktLineString("shallow " + commitSHA + "\n"))
	buf.WriteString("0000")
	return buf.Bytes()
}

func uploadPackResult(commitSHA string, pack []byte, sideBand bool, shallow bool) []byte {
	var buf bytes.Buffer
	if shallow {
		buf.Write(uploadPackShallowResult(commitSHA))
	}
	buf.Write(pktLineString("NAK\n"))
	if !sideBand {
		buf.Write(pack)
		return buf.Bytes()
	}
	for len(pack) > 0 {
		n := len(pack)
		if n > 65515 {
			n = 65515
		}
		payload := append([]byte{1}, pack[:n]...)
		buf.Write(pktLineBytes(payload))
		pack = pack[n:]
	}
	buf.WriteString("0000")
	return buf.Bytes()
}

func pktLineString(s string) []byte {
	return pktLineBytes([]byte(s))
}

func pktLineBytes(payload []byte) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "%04x", len(payload)+4)
	buf.Write(payload)
	return buf.Bytes()
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
