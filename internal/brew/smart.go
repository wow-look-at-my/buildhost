package brew

// Smart-HTTP git serving (protocol v0 upload-pack) for the generated Homebrew

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
)

// ServeTapInfoRefs handles GET .../info/refs on both cloneable tap URLs --
func (h *Handler) ServeTapInfoRefs(w http.ResponseWriter, r *http.Request) {
	h.serveTapInfoRefs(w, r)
}

// ServeTapUploadPack handles POST .../git-upload-pack -- the smart fetch --
func (h *Handler) ServeTapUploadPack(w http.ResponseWriter, r *http.Request) {
	h.serveTapUploadPack(w, r)
}

// ServePrivateTapInfoRefs / ServePrivateTapUploadPack are the smart endpoints
// under brew.{domain}/private/tap.git, with ServePrivateTap's exact credential
func (h *Handler) ServePrivateTapInfoRefs(w http.ResponseWriter, r *http.Request) {
	if auth.TokenFrom(r.Context()) == nil {
		h.ServePrivateTap(w, r)
		return
	}
	h.serveTapInfoRefs(w, r)
}

func (h *Handler) ServePrivateTapUploadPack(w http.ResponseWriter, r *http.Request) {
	if auth.TokenFrom(r.Context()) == nil {
		h.ServePrivateTap(w, r)
		return
	}
	h.serveTapUploadPack(w, r)
}

// serveTapInfoRefs dispatches an info/refs request by its service parameter:
// none means the dumb protocol (served exactly like every other tap file),
// git-upload-pack means the smart advertisement, anything else is refused
func (h *Handler) serveTapInfoRefs(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Query().Get("service") {
	case "":
		h.serveTapFile(w, r)
	case "git-upload-pack":
		h.serveUploadPackAdvertisement(w, r)
	default:
		http.Error(w, "unsupported service", http.StatusForbidden)
	}
}

// serveUploadPackAdvertisement answers the smart ref advertisement from the
// lineage's persisted tip -- the SAME refs/heads/main the dumb path serves, so
func (h *Handler) serveUploadPackAdvertisement(w http.ResponseWriter, r *http.Request) {
	root, release, err := h.acquireTapLineage(r)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer release()

	tip, err := tapTipFromRoot(root)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
	setTapCacheControl(w, r)
	w.Write(uploadPackAdvertisement(tip))
}

// serveTapUploadPack answers a smart fetch. The pack is built from the commit
// the CLIENT asked for (its "want" -- the sha the advertisement handed it), so
// a publish that advances the tip between the advertisement and this POST can
// never produce a ref/pack mismatch: the append-only store still holds the
// wanted commit's entire closure. An unknown or non-commit want falls back to
func (h *Handler) serveTapUploadPack(w http.ResponseWriter, r *http.Request) {
	body, err := readUploadPackRequest(r)
	if err != nil {
		http.Error(w, "bad upload-pack request", http.StatusBadRequest)
		return
	}
	req := parseUploadPackRequest(body)

	root, release, err := h.acquireTapLineage(r)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer release()

	target, err := tapTipFromRoot(root)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	for _, want := range req.wants {
		if want == target {
			break
		}
		if kind, _, err := readLooseObject(root, want); err == nil && kind == "commit" {
			target = want
			break
		}
	}

	// Walk the commit graph up front (it also computes the shallow boundary),
	commits, shallow, unshallow, err := walkCommits(root, target, req.depth, req.clientShallow)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-git-upload-pack-result")
	setTapCacheControl(w, r)

	// A response to a deepen request always carries the shallow section (the
	// exchange is stateless, so it is repeated on the final round too); a
	if req.hasDeepen {
		writeShallowSection(w, shallow, unshallow)
		if !req.done {
			// The depth negotiation round: the client sends wants + deepen
			return
		}
	} else if !req.done {
		// A flush-terminated batch of haves mid-negotiation: we never ACK
		w.Write(pktLineString("NAK\n"))
		return
	}

	w.Write(pktLineString("NAK\n"))

	entries, err := collectPackEntries(root, commits)
	if err != nil {
		// Headers are sent; abandoning the body fails the client's pack
		return
	}
	var dst io.Writer = w
	var flush func() error
	if req.sideBand {
		bw := bufio.NewWriterSize(&sideBandWriter{w: w}, 32*1024)
		dst, flush = bw, bw.Flush
	}
	if err := writePack(dst, root, entries); err != nil {
		return
	}
	if flush != nil {
		if err := flush(); err != nil {
			return
		}
	}
	if req.sideBand {
		io.WriteString(w, "0000")
	}
}

// setTapCacheControl applies the tap's credential-dependent caching rule (the
func setTapCacheControl(w http.ResponseWriter, r *http.Request) {
	if auth.TokenFrom(r.Context()) != nil {
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Vary", "Authorization")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
}

// tapTipFromRoot reads the lineage's tip commit sha through the request's
// sandboxed root -- the same file the dumb path serves as refs/heads/main.
func tapTipFromRoot(root *os.Root) (string, error) {
	f, err := root.Open("refs/heads/main")
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 128))
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(string(b))
	if !validLooseSHA(sha) {
		return "", fmt.Errorf("lineage tip is not a sha: %q", sha)
	}
	return sha, nil
}

// uploadPackAdvertisement renders the protocol v0 smart ref advertisement for
// the tap's single branch. Capabilities: side-band(-64k) for framed packs,
// shallow so `--depth` works, multi_ack(+detailed)/thin-pack/ofs-delta so
// modern clients negotiate normally (we still always answer NAK and send full
// packs with no deltas -- advertising them only widens what we ACCEPT).
// deepen-since/deepen-not are deliberately NOT advertised: the lineage's
func uploadPackAdvertisement(commitSHA string) []byte {
	var buf bytes.Buffer
	buf.Write(pktLineString("# service=git-upload-pack\n"))
	buf.WriteString("0000")
	buf.Write(pktLineString(commitSHA + " HEAD\x00multi_ack multi_ack_detailed thin-pack side-band side-band-64k ofs-delta shallow symref=HEAD:refs/heads/main agent=buildhost\n"))
	buf.Write(pktLineString(commitSHA + " refs/heads/main\n"))
	buf.WriteString("0000")
	return buf.Bytes()
}

// readUploadPackRequest reads the (possibly gzip-compressed -- git compresses
// larger request bodies) upload-pack request, bounded well above any sane
// negotiation size.
func readUploadPackRequest(r *http.Request) ([]byte, error) {
	reader := io.Reader(r.Body)
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

// maxUploadPackRefs caps how many want/shallow lines are honored per request.
const maxUploadPackRefs = 32

type uploadPackRequest struct {
	wants         []string
	clientShallow []string
	depth         int
	hasDeepen     bool // any deepen* pkt-line was present
	done          bool
	sideBand      bool
}

// parseUploadPackRequest decodes the pkt-line request body. Everything is
// prefix-matched per WHOLE pkt-line -- substring matching mistook a want
// line's capability echo (e.g. "deepen-not", "no-done") for protocol verbs.
func parseUploadPackRequest(body []byte) uploadPackRequest {
	var req uploadPackRequest
	for _, line := range pktLines(body) {
		s := strings.TrimSuffix(string(line), "\n")
		switch {
		case strings.HasPrefix(s, "want "):
			fields := strings.Fields(s[len("want "):])
			if len(fields) > 0 && validLooseSHA(fields[0]) && len(req.wants) < maxUploadPackRefs {
				req.wants = append(req.wants, fields[0])
			}
			for _, c := range fields[1:] {
				if c == "side-band" || c == "side-band-64k" {
					req.sideBand = true
				}
			}
		case strings.HasPrefix(s, "shallow "):
			if sha := s[len("shallow "):]; validLooseSHA(sha) && len(req.clientShallow) < maxUploadPackRefs {
				req.clientShallow = append(req.clientShallow, sha)
			}
		case strings.HasPrefix(s, "deepen"):
			req.hasDeepen = true
			if n, err := strconv.Atoi(strings.TrimPrefix(s, "deepen ")); err == nil && n > 0 {
				req.depth = n
			}
		case s == "done":
			req.done = true
		}
	}
	return req
}

// wantsShallow reports whether the request carries an actual depth request --
func wantsShallow(body []byte) bool {
	return parseUploadPackRequest(body).hasDeepen
}

// pktLines splits a pkt-line stream into its payload lines, skipping
func pktLines(body []byte) [][]byte {
	var lines [][]byte
	for len(body) >= 4 {
		n, err := strconv.ParseUint(string(body[:4]), 16, 32)
		if err != nil {
			break
		}
		if n < 4 {
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

// writeShallowSection emits the deepen response: the commits that became
// shallow (their parents were cut by the requested depth), the client-shallow
// commits this pack un-shallows (their parents ARE included now, e.g. a
func writeShallowSection(w io.Writer, shallow, unshallow []string) {
	for _, sha := range shallow {
		w.Write(pktLineString("shallow " + sha + "\n"))
	}
	for _, sha := range unshallow {
		w.Write(pktLineString("unshallow " + sha + "\n"))
	}
	io.WriteString(w, "0000")
}

const sideBandMaxData = 65515

type sideBandWriter struct {
	w io.Writer
}

func (s *sideBandWriter) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		n := min(len(p), sideBandMaxData)
		packet := make([]byte, 0, n+1)
		packet = append(packet, 1)
		packet = append(packet, p[:n]...)
		if _, err := s.w.Write(pktLineBytes(packet)); err != nil {
			return total, err
		}
		total += n
		p = p[n:]
	}
	return total, nil
}

func pktLineString(s string) []byte {
	return pktLineBytes([]byte(s))
}

func pktLineBytes(payload []byte) []byte {
	out := make([]byte, 0, len(payload)+4)
	out = fmt.Appendf(out, "%04x", len(payload)+4)
	return append(out, payload...)
}
