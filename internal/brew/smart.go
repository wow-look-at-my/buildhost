package brew

// Smart-HTTP git serving (protocol v0 upload-pack) for the generated Homebrew
// tap, layered on the SAME per-lineage history store the dumb-HTTP path serves
// (taphistory.go): the advertisement reads the lineage's refs/heads/main tip,
// and the pack is assembled on the fly by walking parent/tree links through
// the lineage's loose objects. Nothing is materialized per build -- the
// append-only store IS the source of truth for both protocols, so they can
// never describe different repositories.
//
// Consistency across a publish landing mid-clone: the client names the commit
// it wants ("want <sha>" -- the sha the advertisement gave it), and the store
// is append-only, so even after the tip advances the wanted commit's whole
// closure is still on disk and is exactly what gets packed. While a smart
// request is streaming, its lineage directory is additionally pinned against
// the disk-cap eviction (acquireTapLineage), so a pack can never be truncated
// by a concurrent RemoveAll.
//
// Negotiation is deliberately stateless and minimal: the server never ACKs a
// common commit -- each flush-terminated batch of haves is answered with NAK
// (the client keeps negotiating until it sends "done"), and the final response
// is NAK plus a self-contained pack of every object reachable from the want
// (bounded by the requested deepen depth). Correct first; the objects are
// KB-scale formula texts, so the redundancy is noise.

import (
	"bufio"
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
	"os"
	"strconv"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
)

// ServeTapInfoRefs handles GET git.{domain}/brew/tap.git/info/refs -- the
// literal route that outscores the {path...} dumb file route. With
// ?service=git-upload-pack it answers the smart ref advertisement; without a
// service parameter it falls through to the exact dumb-HTTP file serving
// (#159's deployed behavior, byte-for-byte), so dumb clients are untouched.
func (h *Handler) ServeTapInfoRefs(w http.ResponseWriter, r *http.Request) {
	h.serveTapInfoRefs(w, r)
}

// ServeTapUploadPack handles POST git.{domain}/brew/tap.git/git-upload-pack --
// the smart fetch on the public tap.
func (h *Handler) ServeTapUploadPack(w http.ResponseWriter, r *http.Request) {
	h.serveTapUploadPack(w, r)
}

// RedirectTapInfoRefs / RedirectTapUploadPack are the smart endpoints under
// brew.{domain}/tap.git, with RedirectTap's exact credential semantics: an
// anonymous request is redirected to the public tap on the git subdomain
// (query string preserved, so the smart handshake continues there), while a
// credentialed one is served in place -- clients drop credentials across a
// cross-host redirect, so redirecting would silently downgrade to public.
func (h *Handler) RedirectTapInfoRefs(w http.ResponseWriter, r *http.Request) {
	if auth.TokenFrom(r.Context()) == nil {
		h.RedirectTap(w, r) // anonymous: the same 301 every /tap.git path gets
		return
	}
	h.serveTapInfoRefs(w, r)
}

func (h *Handler) RedirectTapUploadPack(w http.ResponseWriter, r *http.Request) {
	if auth.TokenFrom(r.Context()) == nil {
		h.RedirectTap(w, r)
		return
	}
	h.serveTapUploadPack(w, r)
}

// ServePrivateTapInfoRefs / ServePrivateTapUploadPack are the smart endpoints
// under brew.{domain}/private/tap.git, with ServePrivateTap's exact credential
// semantics: anonymous requests get the 401 Basic challenge (git only sends
// URL-embedded credentials after a challenge), credentialed ones are served a
// tap scoped to the credential -- the lineage key already carries the scope.
func (h *Handler) ServePrivateTapInfoRefs(w http.ResponseWriter, r *http.Request) {
	if auth.TokenFrom(r.Context()) == nil {
		h.ServePrivateTap(w, r) // the 401 Basic challenge
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
// (there is no receive-pack -- the tap is read-only by construction).
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
// smart and dumb clients always see one repository.
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
// the current tip rather than erroring -- the tap advertises exactly one ref,
// so that is the only thing a well-formed client can mean.
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
	// so every failure happens before headers are written.
	commits, shallow, unshallow, err := walkCommits(root, target, req.depth, req.clientShallow)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-git-upload-pack-result")
	setTapCacheControl(w, r)

	// A response to a deepen request always carries the shallow section (the
	// exchange is stateless, so it is repeated on the final round too); a
	// request WITHOUT deepen must never see one -- a plain clone's want line
	// merely echoing shallow capabilities used to be mistaken for a depth
	// request, and git died with "expected ACK/NAK, got 'shallow <sha>'".
	if req.hasDeepen {
		writeShallowSection(w, shallow, unshallow)
		if !req.done {
			// The depth negotiation round: the client sends wants + deepen
			// first and expects only the shallow section back.
			return
		}
	} else if !req.done {
		// A flush-terminated batch of haves mid-negotiation: we never ACK
		// (stateless NAK negotiation), so answer NAK and let the client keep
		// going until it sends done. Appending the pack here would corrupt
		// the next round once a client has >1 batch of haves to advertise --
		// which real history now makes possible.
		w.Write(pktLineString("NAK\n"))
		return
	}

	w.Write(pktLineString("NAK\n"))

	entries, err := collectPackEntries(root, commits)
	if err != nil {
		// Headers are sent; abandoning the body fails the client's pack
		// checksum, the same contract the dumb path has for mid-stream errors.
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
// same one serveTapFile applies): a credentialed response depends on the
// Authorization header and must never be shared-cached.
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
// commits carry zero timestamps, so time-based deepening would be nonsense,
// and a capability we do not advertise is one a compliant client never sends.
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
// The tap advertises exactly one ref, so anything beyond a handful is a
// hostile body; the cap bounds the per-want disk probes.
const maxUploadPackRefs = 32

type uploadPackRequest struct {
	wants         []string
	clientShallow []string
	depth         int  // parsed "deepen <n>"; 0 = unbounded
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
			// Capabilities ride the first want line's tail; scanning every
			// want line is harmless (shas are hex and can't collide).
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
// a "deepen <n>", "deepen-since <timestamp>", or "deepen-not <ref>" pkt-line.
// Per the pack protocol the shallow section is sent ONLY in answer to such a
// request; a client that sent none expects ACK/NAK immediately. This must
// inspect whole pkt-lines: a plain (full) clone's want line ECHOES advertised
// deepen-* capability tokens, so a raw substring match mistook every full
// clone for a shallow one and git died with "fatal: git fetch-pack: expected
// ACK/NAK, got 'shallow <sha>'".
func wantsShallow(body []byte) bool {
	return parseUploadPackRequest(body).hasDeepen
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

// writeShallowSection emits the deepen response: the commits that became
// shallow (their parents were cut by the requested depth), the client-shallow
// commits this pack un-shallows (their parents ARE included now, e.g. a
// --depth increase or --unshallow), then the section's flush.
func writeShallowSection(w io.Writer, shallow, unshallow []string) {
	for _, sha := range shallow {
		w.Write(pktLineString("shallow " + sha + "\n"))
	}
	for _, sha := range unshallow {
		w.Write(pktLineString("unshallow " + sha + "\n"))
	}
	io.WriteString(w, "0000")
}

// commitNode is one walked commit and its root tree.
type commitNode struct {
	sha  string
	tree string
}

// walkCommits walks the lineage's commit graph from tip, bounded by depth
// (0 = every commit reachable -- the full-history clone). It returns the
// included commits, the shallow boundary (included commits whose parents the
// depth cut off), and which of the client's shallow points this response
// un-shallows. The lineage's history is a single-parent chain in practice,
// but the walk is a general BFS so nothing breaks if that ever changes.
func walkCommits(root *os.Root, tip string, depth int, clientShallow []string) (commits []commitNode, shallow, unshallow []string, err error) {
	type qnode struct {
		sha  string
		dist int
	}
	queue := []qnode{{sha: tip, dist: 1}}
	seen := map[string]bool{tip: true}
	inShallow := map[string]bool{}

	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]

		kind, body, rerr := readLooseObject(root, c.sha)
		if rerr != nil {
			return nil, nil, nil, rerr
		}
		if kind != "commit" {
			return nil, nil, nil, fmt.Errorf("object %s is a %s, expected commit", c.sha, kind)
		}
		tree, parents, perr := parseCommitHeaders(body)
		if perr != nil {
			return nil, nil, nil, perr
		}
		commits = append(commits, commitNode{sha: c.sha, tree: tree})

		if depth > 0 && c.dist >= depth {
			if len(parents) > 0 {
				shallow = append(shallow, c.sha)
				inShallow[c.sha] = true
			}
			continue
		}
		for _, p := range parents {
			if !seen[p] {
				seen[p] = true
				queue = append(queue, qnode{sha: p, dist: c.dist + 1})
			}
		}
	}

	for _, cs := range clientShallow {
		if seen[cs] && !inShallow[cs] {
			unshallow = append(unshallow, cs)
		}
	}
	return commits, shallow, unshallow, nil
}

// collectPackEntries expands the walked commits into the full object list for
// the pack: every commit, then each commit's tree closure, deduplicated -- the
// lineage reuses unchanged trees and blobs across commits, so shared objects
// are packed once. Every object is verified readable here, BEFORE the pack
// header is on the wire.
func collectPackEntries(root *os.Root, commits []commitNode) ([]string, error) {
	seen := make(map[string]bool, len(commits)*4)
	var entries []string
	add := func(sha string) {
		seen[sha] = true
		entries = append(entries, sha)
	}
	for _, c := range commits {
		add(c.sha)
	}
	for _, c := range commits {
		stack := []string{c.tree}
		for len(stack) > 0 {
			treeSHA := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if seen[treeSHA] {
				continue
			}
			kind, body, err := readLooseObject(root, treeSHA)
			if err != nil {
				return nil, err
			}
			if kind != "tree" {
				return nil, fmt.Errorf("object %s is a %s, expected tree", treeSHA, kind)
			}
			add(treeSHA)
			refs, err := parseTreeEntries(body)
			if err != nil {
				return nil, err
			}
			for _, ref := range refs {
				if seen[ref.sha] {
					continue
				}
				if ref.isTree {
					stack = append(stack, ref.sha)
					continue
				}
				if !looseObjectExists(root, ref.sha) {
					return nil, fmt.Errorf("blob %s missing from lineage", ref.sha)
				}
				add(ref.sha)
			}
		}
	}
	return entries, nil
}

// writePack streams a version-2 packfile of the given loose objects: header,
// one non-delta zlib entry per object (each read from the lineage and
// re-deflated one at a time -- per-request memory stays bounded by the
// largest single object, KB-scale formula text), then the SHA-1 trailer.
func writePack(dst io.Writer, root *os.Root, entries []string) error {
	sum := sha1.New()
	w := io.MultiWriter(dst, sum)

	if _, err := io.WriteString(w, "PACK"); err != nil {
		return err
	}
	if err := binary.Write(w, binary.BigEndian, uint32(2)); err != nil {
		return err
	}
	if err := binary.Write(w, binary.BigEndian, uint32(len(entries))); err != nil {
		return err
	}
	for _, sha := range entries {
		kind, body, err := readLooseObject(root, sha)
		if err != nil {
			return err
		}
		typeCode, err := packTypeCode(kind)
		if err != nil {
			return err
		}
		if _, err := w.Write(packObjectHeader(typeCode, len(body))); err != nil {
			return err
		}
		zw := zlib.NewWriter(w)
		if _, err := zw.Write(body); err != nil {
			zw.Close()
			return err
		}
		if err := zw.Close(); err != nil {
			return err
		}
	}
	// The trailer is the digest of everything before it -- written to dst
	// only, never back through the hash.
	_, err := dst.Write(sum.Sum(nil))
	return err
}

// packTypeCode maps a loose object kind to its packfile type code.
func packTypeCode(kind string) (byte, error) {
	switch kind {
	case "commit":
		return 1, nil
	case "tree":
		return 2, nil
	case "blob":
		return 3, nil
	case "tag":
		return 4, nil
	default:
		return 0, fmt.Errorf("unsupported git object type %q", kind)
	}
}

// packObjectHeader encodes a pack entry header: type in bits 4-6 of the first
// byte, the uncompressed size in little-endian base-128 varint form.
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

// readLooseObject reads one loose object from the lineage through the
// sandboxed root and returns its kind and decompressed body. Tap objects are
// KB-scale (formula texts and ~200-byte commits/trees), so whole-body reads
// are the bounded case here.
func readLooseObject(root *os.Root, sha string) (kind string, body []byte, err error) {
	f, err := root.Open(looseObjectPath(sha))
	if err != nil {
		return "", nil, err
	}
	defer f.Close()
	zr, err := zlib.NewReader(f)
	if err != nil {
		return "", nil, err
	}
	defer zr.Close()
	raw, err := io.ReadAll(zr)
	if err != nil {
		return "", nil, err
	}
	head, rest, ok := bytes.Cut(raw, []byte{0})
	if !ok {
		return "", nil, fmt.Errorf("malformed loose object %s", sha)
	}
	kind, _, ok = strings.Cut(string(head), " ")
	if !ok {
		return "", nil, fmt.Errorf("malformed loose object header %s", sha)
	}
	return kind, rest, nil
}

// looseObjectExists probes a loose object's presence without decompressing it.
func looseObjectExists(root *os.Root, sha string) bool {
	f, err := root.Open(looseObjectPath(sha))
	if err != nil {
		return false
	}
	f.Close()
	return true
}

func looseObjectPath(sha string) string {
	return "objects/" + sha[:2] + "/" + sha[2:]
}

// parseCommitHeaders extracts the tree and parent shas from a commit body.
func parseCommitHeaders(body []byte) (tree string, parents []string, err error) {
	for _, line := range strings.Split(string(body), "\n") {
		if line == "" {
			break // headers end at the blank line before the message
		}
		if t, ok := strings.CutPrefix(line, "tree "); ok && validLooseSHA(t) {
			tree = t
		}
		if p, ok := strings.CutPrefix(line, "parent "); ok && validLooseSHA(p) {
			parents = append(parents, p)
		}
	}
	if tree == "" {
		return "", nil, errors.New("commit carries no tree header")
	}
	return tree, parents, nil
}

type treeEntryRef struct {
	sha    string
	isTree bool
}

// parseTreeEntries decodes a tree object body: "<mode> <name>\x00" followed by
// the 20 raw sha bytes, repeated.
func parseTreeEntries(body []byte) ([]treeEntryRef, error) {
	var out []treeEntryRef
	rest := body
	for len(rest) > 0 {
		nul := bytes.IndexByte(rest, 0)
		if nul < 0 || len(rest) < nul+1+20 {
			return nil, errors.New("malformed tree object")
		}
		mode, _, ok := strings.Cut(string(rest[:nul]), " ")
		if !ok {
			return nil, errors.New("malformed tree entry")
		}
		out = append(out, treeEntryRef{
			sha:    hex.EncodeToString(rest[nul+1 : nul+1+20]),
			isTree: mode == "40000",
		})
		rest = rest[nul+1+20:]
	}
	return out, nil
}

// sideBandMaxData is the largest payload of one side-band-64k data packet:
// the 65520-byte pkt-line ceiling minus the 4-byte length and 1-byte band.
const sideBandMaxData = 65515

// sideBandWriter frames raw pack bytes into side-band-64k band-1 data packets.
// The caller terminates the stream with a flush-pkt after the last write.
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
