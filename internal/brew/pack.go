package brew

// Packfile assembly for the smart-HTTP tap (smart.go): the response pack is
// built on the fly by walking parent/tree links from the wanted commit

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

// walkCommits walks the lineage's commit graph from tip, bounded by depth.
// A commit in have is one the client already holds: the walk neither
// includes it nor goes past it, and returns it in boundary.
func walkCommits(root *os.Root, tip string, depth int, clientShallow []string, have set.Set[string]) (commits []commitNode, shallow, unshallow, boundary []string, err error) {
	type qnode struct {
		sha  string
		dist int
	}
	var queue []qnode
	seen := set.Of(tip)
	inShallow := set.New[string]()
	if have.Contains(tip) {
		boundary = append(boundary, tip)
	} else {
		queue = append(queue, qnode{sha: tip, dist: 1})
	}

	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]

		kind, body, rerr := readLooseObject(root, c.sha)
		if rerr != nil {
			return nil, nil, nil, nil, rerr
		}
		if kind != "commit" {
			return nil, nil, nil, nil, fmt.Errorf("object %s is a %s, expected commit", c.sha, kind)
		}
		tree, parents, perr := parseCommitHeaders(body)
		if perr != nil {
			return nil, nil, nil, nil, perr
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
			if !seen.Add(p) {
				continue
			}
			if have.Contains(p) {
				boundary = append(boundary, p)
				continue
			}
			queue = append(queue, qnode{sha: p, dist: c.dist + 1})
		}
	}

	for _, cs := range clientShallow {
		if seen.Contains(cs) && !inShallow.Contains(cs) {
			unshallow = append(unshallow, cs)
		}
	}
	return commits, shallow, unshallow, boundary, nil
}

// reachesAny reports whether a commit in have is tip or one of its ancestors.
// The walk stops at the first one it meets.
func reachesAny(root *os.Root, tip string, have set.Set[string]) (bool, error) {
	queue := []string{tip}
	seen := set.Of(tip)
	for len(queue) > 0 {
		sha := queue[0]
		queue = queue[1:]
		if have.Contains(sha) {
			return true, nil
		}
		kind, body, err := readLooseObject(root, sha)
		if err != nil {
			return false, err
		}
		if kind != "commit" {
			return false, fmt.Errorf("object %s is a %s, expected commit", sha, kind)
		}
		_, parents, err := parseCommitHeaders(body)
		if err != nil {
			return false, err
		}
		for _, p := range parents {
			if seen.Add(p) {
				queue = append(queue, p)
			}
		}
	}
	return false, nil
}

// collectPackEntries expands the walked commits into the full object list for
// the pack. Every commit, then each commit's tree closure, deduplicated -- the
// lineage reuses unchanged trees and blobs across commits, so shared objects
// go in once. The tree closure of each boundary commit is left out: the client
// already holds it.
func collectPackEntries(root *os.Root, commits []commitNode, boundary []string) ([]string, error) {
	seen := set.New[string](len(commits) * 4)
	for _, sha := range boundary {
		if err := markTreeClosure(root, sha, &seen); err != nil {
			return nil, err
		}
	}
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

// markTreeClosure adds the tree of commitSHA and every tree and blob under it
// to seen. A subtree already in seen is not read again.
func markTreeClosure(root *os.Root, commitSHA string, seen *set.Set[string]) error {
	kind, body, err := readLooseObject(root, commitSHA)
	if err != nil {
		return err
	}
	if kind != "commit" {
		return fmt.Errorf("object %s is a %s, expected commit", commitSHA, kind)
	}
	tree, _, err := parseCommitHeaders(body)
	if err != nil {
		return err
	}
	stack := []string{tree}
	for len(stack) > 0 {
		treeSHA := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if !seen.Add(treeSHA) {
			continue
		}
		kind, body, err := readLooseObject(root, treeSHA)
		if err != nil {
			return err
		}
		if kind != "tree" {
			return fmt.Errorf("object %s is a %s, expected tree", treeSHA, kind)
		}
		refs, err := parseTreeEntries(body)
		if err != nil {
			return err
		}
		for _, ref := range refs {
			if ref.isTree {
				stack = append(stack, ref.sha)
				continue
			}
			seen.Add(ref.sha)
		}
	}
	return nil
}

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

// parseTreeEntries decodes a tree object body.
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
