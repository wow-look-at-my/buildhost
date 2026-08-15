package brew

// Packfile assembly for the smart-HTTP tap (smart.go): the response pack is
// built on the fly by walking parent/tree links from the wanted commit
// through the lineage's loose objects -- see smart.go for the protocol layer
// and the consistency story.

import (
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/wow-look-at-my/go-containers/set"
)

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
	seen := set.Of(tip)
	inShallow := set.New[string]()

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
				inShallow.Add(c.sha)
			}
			continue
		}
		for _, p := range parents {
			if seen.Add(p) {
				queue = append(queue, qnode{sha: p, dist: c.dist + 1})
			}
		}
	}

	for _, cs := range clientShallow {
		if seen.Contains(cs) && !inShallow.Contains(cs) {
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
	seen := set.New[string](len(commits) * 4)
	var entries []string
	add := func(sha string) {
		seen.Add(sha)
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
			if seen.Contains(treeSHA) {
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
				if seen.Contains(ref.sha) {
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
