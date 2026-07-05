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
	"os"
	"sort"
	"strconv"
	"strings"

	mmap "github.com/wow-look-at-my/go-mmap"

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

// ServeTap serves one file of the dumb-HTTP git tap from the current on-disk
// snapshot (built at most once per tapCacheTTL per base URL, see tapcache.go)
// by memory-mapping it -- never buffering the file on the heap and never
// rebuilding the whole tap per object request. Serving every request of one
// `brew update` from a single immutable snapshot also keeps refs and loose
// objects mutually consistent when a publish lands mid-update.
func (h *Handler) ServeTap(w http.ResponseWriter, r *http.Request) {
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

	w.Header().Set("Cache-Control", "no-cache")
	if strings.HasPrefix(path, "objects/") {
		w.Header().Set("Content-Type", "application/x-git-loose-object")
	} else {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	serveSnapshotFile(w, f, info.Size())
}

// serveSnapshotFile streams one opened snapshot file by memory-mapping it --
// the tap serving rule: cached content is disk files served via mmap, never
// heap-buffered. Content-Length is set from the file size; any other headers
// (Content-Type, Cache-Control) must be set by the caller first.
func serveSnapshotFile(w http.ResponseWriter, f *os.File, size int64) {
	// A zero-length file (objects/info/packs) has nothing to map -- mmap
	// rejects an empty region, same as the storage layer's empty-blob case.
	if size == 0 {
		w.Header().Set("Content-Length", "0")
		return
	}

	m, err := mmap.MapRegion(int(f.Fd()), size, mmap.ProtRead, mmap.MapShared, 0)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	_ = m.Advise(mmap.AdvSequential)
	rc := mmap.NewReader(m) // Close unmaps
	defer rc.Close()
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	io.Copy(w, rc)
}

// ServeUploadPackInfoRefs answers the smart-HTTP ref advertisement
// (GET /info/refs?service=git-upload-pack) so `brew tap` / `git clone` can
// talk to the tap URL directly. The advertisement bytes are materialized in
// the same on-disk snapshot ServeTap serves (see tapcache.go) and
// mmap-streamed from it, so the smart and dumb paths always describe one
// consistent build and nothing is rebuilt (or held on the heap) per request.
func (h *Handler) ServeUploadPackInfoRefs(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("service") != "git-upload-pack" {
		http.Error(w, "unsupported service", http.StatusForbidden)
		return
	}

	f, _, err := h.openTapSnapshotFile(r, tapAdvertisementFile)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
	w.Header().Set("Cache-Control", "no-cache")
	serveSnapshotFile(w, f, info.Size())
}

// ServeUploadPack answers the smart-HTTP fetch (POST /git-upload-pack) with
// the snapshot's pre-built whole-tap packfile. There is no real negotiation:
// the tap is a single synthetic commit, so every fetch gets the full pack
// (NAK, no common objects); only the shallow handshake (`--depth`, which git
// completes before sending "done") is answered explicitly. The pack is
// mmap-streamed from the snapshot -- raw, or framed into side-band-64k data
// packets -- never heap-buffered.
func (h *Handler) ServeUploadPack(w http.ResponseWriter, r *http.Request) {
	body, err := readUploadPackRequest(r)
	if err != nil {
		http.Error(w, "bad upload-pack request", http.StatusBadRequest)
		return
	}

	f, commitSHA, err := h.openTapSnapshotFile(r, tapPackFile)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-git-upload-pack-result")
	w.Header().Set("Cache-Control", "no-cache")
	shallow := wantsShallow(body)
	if shallow && !uploadPackDone(body) {
		w.Write(uploadPackShallowResult(commitSHA))
		return
	}
	if shallow {
		w.Write(uploadPackShallowResult(commitSHA))
	}
	w.Write(pktLineString("NAK\n"))
	streamPack(w, f, info.Size(), wantsSideBand(body))
}

// streamPack writes the packfile to w from the mmap'd snapshot file: raw
// bytes when the client didn't ask for side-band, else framed into
// side-band-64k data packets (band 1, max 65515 payload bytes per packet)
// terminated by a flush-pkt -- the same bytes the pre-cache implementation
// assembled in memory, emitted incrementally.
func streamPack(w http.ResponseWriter, f *os.File, size int64, sideBand bool) {
	if size == 0 {
		return // cannot happen: a pack always carries its header and trailer
	}
	m, err := mmap.MapRegion(int(f.Fd()), size, mmap.ProtRead, mmap.MapShared, 0)
	if err != nil {
		return // headers already sent; a truncated body fails the client's pack checksum
	}
	_ = m.Advise(mmap.AdvSequential)
	rc := mmap.NewReader(m) // Close unmaps
	defer rc.Close()

	if !sideBand {
		io.Copy(w, rc)
		return
	}
	buf := make([]byte, 65515)
	for {
		n, rerr := rc.Read(buf)
		if n > 0 {
			w.Write(pktLineBytes(append([]byte{1}, buf[:n]...)))
		}
		if rerr != nil {
			break
		}
	}
	io.WriteString(w, "0000")
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

// wantsShallow reports whether the request carries an actual depth request --
// a "deepen <n>", "deepen-since <timestamp>", or "deepen-not <ref>" pkt-line.
// Per the pack protocol the shallow section is sent ONLY in answer to such a
// request; a client that sent none expects ACK/NAK immediately. This must
// inspect whole pkt-lines: a plain (full) clone's want line ECHOES the
// advertised "deepen-since deepen-not" capability tokens, so a raw substring
// match mistook every full clone for a shallow one and git died with
// "fatal: git fetch-pack: expected ACK/NAK, got 'shallow <sha>'".
func wantsShallow(body []byte) bool {
	for _, line := range pktLines(body) {
		if bytes.HasPrefix(line, []byte("deepen")) {
			return true
		}
	}
	return false
}

// pktLines splits a pkt-line stream into its payload lines, skipping
// flush-pkts (0000) and the other zero-payload special packets; parsing
// stops at the first malformed length. Trailing newlines are kept --
// callers prefix-match.
func pktLines(body []byte) [][]byte {
	var lines [][]byte
	for len(body) >= 4 {
		n, err := strconv.ParseUint(string(body[:4]), 16, 32)
		if err != nil {
			break
		}
		if n < 4 {
			// flush-pkt (0000), delim-pkt (0001), response-end (0002).
			body = body[4:]
			continue
		}
		if uint64(len(body)) < n {
			break
		}
		lines = append(lines, body[4:n])
		body = body[n:]
	}
	return lines
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
