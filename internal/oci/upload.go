package oci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/storage"
)

// errBlobTooLarge is returned when an upload exceeds the configured per-blob cap.
var errBlobTooLarge = errors.New("blob exceeds maximum size")

// uploadSession is an in-progress OCI blob upload. Bytes are streamed to a temp
// file under the data dir (never /tmp) and hashed incrementally so the final
// digest can be verified against the client-supplied digest.
type uploadSession struct {
	uuid    string
	file    *os.File
	hasher  hash.Hash
	written int64
	created time.Time
}

// uploadStore tracks in-progress blob uploads. Sessions live in memory plus a
// temp file each; only finalized blobs reach content-addressed storage. This is
// fine for the single-container deployment model; horizontal scaling would need
// sticky routing or shared session state.
type uploadStore struct {
	mu       sync.Mutex
	dir      string
	maxBlob  int64
	sessions map[string]*uploadSession
}

func newUploadStore(dir string, maxBlob int64) *uploadStore {
	os.MkdirAll(dir, 0o755) // best-effort; start() surfaces a clear error otherwise
	return &uploadStore{dir: dir, maxBlob: maxBlob, sessions: map[string]*uploadSession{}}
}

// start opens a new upload session backed by a fresh temp file. It opportunistically
// sweeps abandoned sessions so no background goroutine is needed.
func (s *uploadStore) start() (*uploadSession, error) {
	s.sweep(2 * time.Hour)
	f, err := os.CreateTemp(s.dir, "upload-*")
	if err != nil {
		return nil, fmt.Errorf("create upload temp: %w", err)
	}
	sess := &uploadSession{
		uuid:    filepath.Base(f.Name()),
		file:    f,
		hasher:  sha256.New(),
		created: time.Now(),
	}
	s.mu.Lock()
	s.sessions[sess.uuid] = sess
	s.mu.Unlock()
	return sess, nil
}

func (s *uploadStore) get(uuid string) *uploadSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[uuid]
}

// remove discards a session and its temp file.
func (s *uploadStore) remove(sess *uploadSession) {
	s.mu.Lock()
	delete(s.sessions, sess.uuid)
	s.mu.Unlock()
	if sess.file != nil {
		name := sess.file.Name()
		sess.file.Close()
		os.Remove(name)
	}
}

// appendChunk streams r into the session, enforcing the per-blob cap.
func (s *uploadStore) appendChunk(sess *uploadSession, r io.Reader) (int64, error) {
	capped := &cappedReader{r: r, remaining: s.maxBlob - sess.written}
	n, err := io.Copy(io.MultiWriter(sess.file, sess.hasher), capped)
	sess.written += n
	if capped.exceeded {
		return n, errBlobTooLarge
	}
	return n, err
}

// finalize verifies the accumulated digest against expectedDigest, then stores
// the blob in content-addressed storage (whose key equals the digest hex).
func (s *uploadStore) finalize(ctx context.Context, store storage.Storage, sess *uploadSession, expectedDigest string) (digest string, size int64, err error) {
	defer s.remove(sess)

	gotDigest := "sha256:" + hex.EncodeToString(sess.hasher.Sum(nil))
	if expectedDigest != "" && expectedDigest != gotDigest {
		return "", 0, fmt.Errorf("digest mismatch: expected %s, got %s", expectedDigest, gotDigest)
	}
	if _, err := sess.file.Seek(0, io.SeekStart); err != nil {
		return "", 0, fmt.Errorf("rewind upload: %w", err)
	}
	key, n, err := store.Put(ctx, sess.file)
	if err != nil {
		return "", 0, fmt.Errorf("store blob: %w", err)
	}
	if "sha256:"+key != gotDigest {
		return "", 0, fmt.Errorf("storage key %s does not match digest %s", key, gotDigest)
	}
	return gotDigest, n, nil
}

// sweep removes sessions older than maxAge (abandoned uploads).
func (s *uploadStore) sweep(maxAge time.Duration) {
	cutoff := time.Now().Add(-maxAge)
	s.mu.Lock()
	var stale []*uploadSession
	for _, sess := range s.sessions {
		if sess.created.Before(cutoff) {
			stale = append(stale, sess)
		}
	}
	s.mu.Unlock()
	for _, sess := range stale {
		s.remove(sess)
	}
}

// cappedReader reads from r up to a byte budget, after which it reports an
// error rather than returning more data.
type cappedReader struct {
	r         io.Reader
	remaining int64
	exceeded  bool
}

func (c *cappedReader) Read(p []byte) (int, error) {
	if c.remaining < 0 {
		c.exceeded = true
		return 0, errBlobTooLarge
	}
	if int64(len(p)) > c.remaining+1 {
		p = p[:c.remaining+1]
	}
	n, err := c.r.Read(p)
	c.remaining -= int64(n)
	if c.remaining < 0 {
		c.exceeded = true
		return n, errBlobTooLarge
	}
	return n, err
}
