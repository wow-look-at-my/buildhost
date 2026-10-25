package static

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/exeformat"
	"github.com/wow-look-at-my/buildhost/internal/pack"
	"github.com/wow-look-at-my/buildhost/internal/recompress"
	"github.com/wow-look-at-my/buildhost/internal/storage"
	"github.com/wow-look-at-my/buildhost/internal/strip"
)

// maxPackMemo bounds the memory of packer refusals. It is a cache of negative
// results, not state that has to be complete.
const maxPackMemo = 1024

// BinaryPacker shrinks a stripped binary into dst, reporting false when the
// result would not be a smaller copy of the same executable. *pack.Packer
// implements it.
type BinaryPacker interface {
	Pack(src, dst string) (bool, error)
}

// Shaper is the server's download-time transform set: the compressed-response
// cache, the per-IP throttle and the packer.
type Shaper struct {
	Cache    *recompress.Cache
	Throttle *recompress.Limiter
	Pack     BinaryPacker

	mu      sync.Mutex
	refused map[string]bool
}

// packer is the packer to use, allowing a nil Pack to mean the embedded tool.
func (s *Shaper) packer() BinaryPacker {
	if s == nil || s.Pack == nil {
		return pack.Default()
	}
	return s.Pack
}

// packAvailable reports whether the packer can run on this platform.
func (s *Shaper) packAvailable() bool {
	if s != nil && s.Pack != nil {
		if a, ok := s.Pack.(interface{ Available() bool }); ok {
			return a.Available()
		}
		return true
	}
	return pack.Default().Available()
}

// recompressionAllowed reports whether ip may start new compression work.
// A server without a limiter never throttles.
func (s *Shaper) recompressionAllowed(ip string) bool {
	if s == nil || s.Throttle == nil {
		return true
	}
	return s.Throttle.Allow(ip)
}

// packRefused reports whether this artifact's packer attempt already failed.
// A repeated download of the same immutable URL must not pay for it again, and
// must serve identical bytes.
func (s *Shaper) packRefused(key string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refused[key]
}

func (s *Shaper) refusePack(key string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refused == nil || len(s.refused) >= maxPackMemo {
		s.refused = map[string]bool{}
	}
	s.refused[key] = true
}

// payload is the bytes a raw download serves before transfer coding.
type payload struct {
	rc   io.ReadCloser // streamed straight from storage
	path string        // a temporary file, removed when the request ends
	size int64
}

func (p *payload) Close() {
	if p.rc != nil {
		p.rc.Close()
	}
	if p.path != "" {
		os.Remove(p.path)
	}
}

// peekHead reads the leading bytes of a stored artifact.
func peekHead(ctx context.Context, store storage.Storage, key string) ([]byte, error) {
	rc, _, err := store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	buf := make([]byte, exeformat.SniffLen)
	n, err := io.ReadFull(rc, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return buf[:n], nil
}

func streamStored(ctx context.Context, store storage.Storage, key string) (*payload, error) {
	rc, size, err := store.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("artifact not found")
	}
	return &payload{rc: rc, size: size}, nil
}

// openPayload returns the bytes the raw format serves for the identity coding.
// An ELF is stripped and then packed; an APE is offered to the packer and
// served unchanged when it is refused; anything else is served as uploaded.
func (s *Shaper) openPayload(ctx context.Context, store storage.Storage, artifact db.Artifact, tmpDir string, head []byte) (*payload, error) {
	switch {
	case strip.LooksELF(bytes.NewReader(head)):
		return s.strippedPayload(ctx, store, artifact, tmpDir)
	case exeformat.Detect(head) == exeformat.APE:
		return s.apePayload(ctx, store, artifact, tmpDir)
	default:
		return streamStored(ctx, store, artifact.StorageKey)
	}
}

// strippedPayload strips the upload and packs the result. Any failure falls
// back to the bytes the previous step holds, never to an error.
func (s *Shaper) strippedPayload(ctx context.Context, store storage.Storage, artifact db.Artifact, tmpDir string) (*payload, error) {
	rc, _, err := store.Get(ctx, artifact.StorageKey)
	if err != nil {
		return nil, fmt.Errorf("artifact not found")
	}
	res, err := strip.StripFile(rc, tmpDir)
	rc.Close()
	if err != nil {
		strip.LogSkipped(ctx, artifact.StorageKey, err)
		return streamStored(ctx, store, artifact.StorageKey)
	}
	os.Remove(res.DebugPath)
	size, err := fileSize(res.StrippedPath)
	if err != nil {
		os.Remove(res.StrippedPath)
		return streamStored(ctx, store, artifact.StorageKey)
	}
	packed, ok := s.packBinary(ctx, res.StrippedPath, tmpDir)
	if !ok {
		return &payload{path: res.StrippedPath, size: size}, nil
	}
	os.Remove(res.StrippedPath)
	return packed, nil
}

// apePayload offers an APE to the packer. The result is only served when it
// still identifies as an APE; otherwise the upload is served unchanged, and
// the refusal is remembered.
func (s *Shaper) apePayload(ctx context.Context, store storage.Storage, artifact db.Artifact, tmpDir string) (*payload, error) {
	if s.packRefused(artifact.StorageKey) || !s.packAvailable() {
		return streamStored(ctx, store, artifact.StorageKey)
	}
	rc, _, err := store.Get(ctx, artifact.StorageKey)
	if err != nil {
		return nil, fmt.Errorf("artifact not found")
	}
	src, err := spool(rc, tmpDir)
	rc.Close()
	if err != nil {
		return streamStored(ctx, store, artifact.StorageKey)
	}
	packed, ok := s.packBinary(ctx, src, tmpDir)
	if !ok {
		os.Remove(src)
		s.refusePack(artifact.StorageKey)
		return streamStored(ctx, store, artifact.StorageKey)
	}
	os.Remove(src)
	return packed, nil
}

// packBinary packs src and returns the payload holding the result. A refusal
// leaves src untouched and reports false.
func (s *Shaper) packBinary(ctx context.Context, src, tmpDir string) (*payload, bool) {
	if !s.packAvailable() {
		return nil, false
	}
	dst, err := tempName(tmpDir, "packed-*")
	if err != nil {
		return nil, false
	}
	ok, err := s.packer().Pack(src, dst)
	if err != nil && !errors.Is(err, pack.ErrNotSmaller) && !errors.Is(err, pack.ErrFormatChanged) {
		// A refusal that is not the ordinary "not smaller" case is a real
		// failure and must not be silent: a packer that never runs is how a
		// download quietly stops shrinking.
		slog.WarnContext(ctx, "packing a binary was skipped", "error", err)
	}
	if !ok {
		os.Remove(dst)
		return nil, false
	}
	size, err := fileSize(dst)
	if err != nil {
		os.Remove(dst)
		return nil, false
	}
	return &payload{path: dst, size: size}, true
}

// spool copies a stream into a temporary file.
func spool(rc io.Reader, tmpDir string) (string, error) {
	f, err := os.CreateTemp(tmpDir, "pack-input-*")
	if err != nil {
		return "", err
	}
	name := f.Name()
	if _, err := io.Copy(f, rc); err != nil {
		f.Close()
		os.Remove(name)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

// tempName returns an unused path in tmpDir without creating the file.
func tempName(tmpDir, pattern string) (string, error) {
	f, err := os.CreateTemp(tmpDir, pattern)
	if err != nil {
		return "", err
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return name, nil
}

func fileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}
