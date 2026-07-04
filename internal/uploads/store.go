// Package uploads implements generic chunked upload sessions, so a client can
// deliver an arbitrarily large request body to ANY existing upload endpoint in
// pieces small enough to survive a proxy's request-body cap (Cloudflare's edge
// rejects bodies over ~100 MB with a 413 that never reaches the origin).
//
// A session is a spool file under {DataDir}/tmp/uploads plus an in-memory
// record bound to the identity that created it. Chunks are appended at an
// explicit offset (verified, so uploads are resumable), and the finished spool
// is consumed by re-issuing the original upload request with an empty body and
// ?upload_session=<id> -- middleware swaps the spool in as the request body, so
// every existing endpoint's routing, auth, and storage logic runs unchanged.
//
// Sessions live in memory (plus one spool file each), like the OCI blob upload
// store. This is fine for the single-container deployment model; a container
// swap mid-session fails the next chunk cleanly and the client restarts.
package uploads

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	// ErrNotFound is returned for a session id that does not exist or is not
	// owned by the caller -- deliberately the same error for both, so session
	// ids never leak across identities.
	ErrNotFound = errors.New("upload session not found")
	// ErrOffsetMismatch is returned when an append's offset is not the session's
	// current committed size. The caller should re-read the size and resume.
	ErrOffsetMismatch = errors.New("offset mismatch")
	// ErrBusy is returned when the session is being finalized.
	ErrBusy = errors.New("upload session busy")
	// ErrTooLarge is returned when an append would push the session past the
	// configured maximum upload size.
	ErrTooLarge = errors.New("upload exceeds maximum size")
)

// Session is one in-progress chunked upload.
type Session struct {
	id      string
	owner   string
	path    string // spool file location
	created time.Time

	mu   sync.Mutex // serializes append/finalize/remove for this session
	file *os.File   // write handle, offset always == size
	size int64      // committed bytes
	busy bool       // a finalize currently owns the spool
}

// ID returns the session's identifier (crypto-random 128-bit hex).
func (s *Session) ID() string { return s.id }

// Size returns the number of committed bytes.
func (s *Session) Size() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.size
}

// Expires returns when the session becomes eligible for sweeping.
func (s *Session) Expires(ttl time.Duration) time.Time { return s.created.Add(ttl) }

// Store tracks in-progress upload sessions.
type Store struct {
	dir     string
	maxSize int64
	ttl     time.Duration

	mu       sync.Mutex
	sessions map[string]*Session
}

// NewStore creates a session store spooling to dir. Leftover spool files from
// a previous process (no session can reference them) are removed.
func NewStore(dir string, maxSize int64, ttl time.Duration) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create upload spool dir: %w", err)
	}
	// Orphan cleanup: every live spool belongs to an in-memory session, and this
	// store starts empty, so anything already in the dir is a crashed leftover.
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	return &Store{dir: dir, maxSize: maxSize, ttl: ttl, sessions: map[string]*Session{}}, nil
}

// MaxSize returns the configured cap on a session's total assembled size.
func (s *Store) MaxSize() int64 { return s.maxSize }

// TTL returns how long an idle session lives before SweepExpired removes it.
func (s *Store) TTL() time.Duration { return s.ttl }

// Create opens a new session owned by owner. It opportunistically sweeps
// expired sessions so long-lived servers stay clean even without the janitor.
func (s *Store) Create(owner string) (*Session, error) {
	s.SweepExpired()

	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("generate session id: %w", err)
	}
	id := hex.EncodeToString(buf)

	path := filepath.Join(s.dir, id+".spool")
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create upload spool: %w", err)
	}

	sess := &Session{id: id, owner: owner, path: path, created: time.Now(), file: f}
	s.mu.Lock()
	s.sessions[id] = sess
	s.mu.Unlock()
	return sess, nil
}

// Get returns the session with the given id if it exists AND belongs to owner.
// A missing session and someone else's session both return ErrNotFound.
func (s *Store) Get(id, owner string) (*Session, error) {
	s.mu.Lock()
	sess := s.sessions[id]
	s.mu.Unlock()
	if sess == nil || sess.owner != owner {
		return nil, ErrNotFound
	}
	return sess, nil
}

// Append streams r onto the end of the spool. offset must equal the session's
// current committed size (ErrOffsetMismatch otherwise, with the actual size
// returned so the client can resume). Partially transferred bytes are
// committed -- a chunk interrupted mid-body advances the size by what landed,
// and the client resumes from the size reported by a status read. An append
// that would exceed the store's maximum size is rolled back entirely and
// returns ErrTooLarge. The returned size is the committed size after the call.
func (s *Store) Append(sess *Session, offset int64, r io.Reader) (int64, error) {
	sess.mu.Lock()
	defer sess.mu.Unlock()

	if sess.busy {
		return sess.size, ErrBusy
	}
	if offset != sess.size {
		return sess.size, ErrOffsetMismatch
	}

	capped := &cappedReader{r: r, remaining: s.maxSize - sess.size}
	n, err := io.Copy(sess.file, capped)
	sess.size += n
	if capped.exceeded {
		// Roll the whole chunk back so the session stays usable at a valid size
		// (and the client gets a clean over-cap error instead of a corrupt tail).
		if terr := sess.file.Truncate(offset); terr == nil {
			if _, serr := sess.file.Seek(offset, io.SeekStart); serr == nil {
				sess.size = offset
			}
		}
		return sess.size, ErrTooLarge
	}
	if err != nil {
		return sess.size, fmt.Errorf("write chunk: %w", err)
	}
	return sess.size, nil
}

// BeginFinalize marks the session busy and returns an independent read handle
// on the spool plus its size. The caller MUST close the handle and then call
// either Remove (consumed) or EndFinalize (retryable failure -- the session
// stays resumable). While busy, appends and other finalizes get ErrBusy.
func (s *Store) BeginFinalize(sess *Session) (*os.File, int64, error) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.busy {
		return nil, 0, ErrBusy
	}
	f, err := os.Open(sess.path)
	if err != nil {
		return nil, 0, fmt.Errorf("open upload spool: %w", err)
	}
	sess.busy = true
	return f, sess.size, nil
}

// EndFinalize releases a BeginFinalize without consuming the session.
func (s *Store) EndFinalize(sess *Session) {
	sess.mu.Lock()
	sess.busy = false
	sess.mu.Unlock()
}

// Remove discards a session and deletes its spool file. Open read handles from
// BeginFinalize survive the unlink until closed.
func (s *Store) Remove(sess *Session) {
	s.mu.Lock()
	delete(s.sessions, sess.id)
	s.mu.Unlock()

	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.file != nil {
		sess.file.Close()
		sess.file = nil
	}
	os.Remove(sess.path)
}

// SweepExpired removes sessions older than the store TTL. A session busy in a
// finalize is skipped (its spool is in use) and picked up on a later sweep.
func (s *Store) SweepExpired() {
	cutoff := time.Now().Add(-s.ttl)
	s.mu.Lock()
	var stale []*Session
	for _, sess := range s.sessions {
		sess.mu.Lock()
		busy := sess.busy
		sess.mu.Unlock()
		if !busy && sess.created.Before(cutoff) {
			stale = append(stale, sess)
		}
	}
	s.mu.Unlock()
	for _, sess := range stale {
		s.Remove(sess)
	}
}

// cappedReader reads from r up to a byte budget, flagging (and stopping at)
// the first read that would exceed it. Same pattern as the OCI blob cap.
type cappedReader struct {
	r         io.Reader
	remaining int64
	exceeded  bool
}

func (c *cappedReader) Read(p []byte) (int, error) {
	if c.remaining < 0 {
		c.exceeded = true
		return 0, ErrTooLarge
	}
	if int64(len(p)) > c.remaining+1 {
		p = p[:c.remaining+1]
	}
	n, err := c.r.Read(p)
	c.remaining -= int64(n)
	if c.remaining < 0 {
		c.exceeded = true
		return n, ErrTooLarge
	}
	return n, err
}
