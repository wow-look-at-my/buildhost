package uploads

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
)

const (
	// SessionParam names a completed upload session on any upload endpoint:
	SessionParam = "upload_session"
	SHA256Param  = "upload_sha256"
	// SHA256Header is the header equivalent of SHA256Param.
	SHA256Header = "X-Upload-SHA256"
)

// ResolveSessionBody makes every existing upload endpoint accept a chunked
// upload session in place of a request body. It runs between authentication
// and routing: a mutating request carrying ?upload_session=<id> (and an empty
// body) has its Body swapped for the session's spool file, so the endpoint's
// own routing, project auth, size caps, and storage logic all run unchanged --
// they just read the spool instead of the network. On a 2xx response the
// session is consumed (spool deleted); on failure it is kept so the client can
// retry the finalize, abort it, or let the TTL sweep collect it.
func ResolveSessionBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get(SessionParam)
		if id == "" || !mutatingMethod(r.Method) || strings.HasPrefix(r.URL.Path, "/api/v1/uploads") {
			// Not a finalize. The /api/v1/uploads exclusion keeps the session
			next.ServeHTTP(w, r)
			return
		}

		owner := requireOwner(w, r)
		if owner == "" {
			return
		}
		store := activeStore(w)
		if store == nil {
			return
		}
		sess, err := store.Get(id, owner)
		if err != nil {
			jsonError(w, http.StatusNotFound, ErrNotFound.Error())
			return
		}
		if r.ContentLength != 0 {
			jsonError(w, http.StatusBadRequest, "request body must be empty when finalizing an upload session")
			return
		}

		spool, size, err := store.BeginFinalize(sess)
		if err != nil {
			jsonError(w, http.StatusConflict, ErrBusy.Error())
			return
		}
		defer spool.Close()

		if want := expectedSHA256(r); want != "" {
			got, err := spoolSHA256(spool)
			if err != nil {
				store.EndFinalize(sess)
				jsonError(w, http.StatusInternalServerError, "failed to hash upload")
				return
			}
			if !strings.EqualFold(want, got) {
				// The spool does not contain what the client thinks it sent. Keep
				store.EndFinalize(sess)
				jsonError(w, http.StatusBadRequest, "sha256 mismatch: upload is "+got)
				return
			}
		}

		// Swap the spool in as the request body and run the real endpoint.
		r.Body = spool
		r.ContentLength = size

		consumed := false
		defer func() {
			// Runs even if the handler panics (recovery middleware is outside
			if consumed {
				store.Remove(sess)
			} else {
				store.EndFinalize(sess)
			}
		}()

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		consumed = rec.status >= 200 && rec.status < 300
	})
}

func mutatingMethod(m string) bool {
	return m == http.MethodPost || m == http.MethodPut || m == http.MethodPatch
}

// expectedSHA256 returns the client-declared digest (query param or header),
// or "" when none was sent.
func expectedSHA256(r *http.Request) string {
	if v := r.URL.Query().Get(SHA256Param); v != "" {
		return v
	}
	return r.Header.Get(SHA256Header)
}

// spoolSHA256 hashes the spool and rewinds it for the handler.
func spoolSHA256(f io.ReadSeeker) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (rw *statusRecorder) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *statusRecorder) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}
