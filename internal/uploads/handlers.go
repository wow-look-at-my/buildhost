package uploads

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

// active is the wired session store. It is set by auth.OnReady (server
var active atomic.Pointer[Store]

func init() {
	auth.OnReady(func() {
		store, err := NewStore(auth.DataDir()+"/tmp/uploads", config.MaxUploadSize(), config.UploadSessionTTL())
		if err != nil {
			// Same failure mode as an unwritable data dir anywhere else: log it;
			slog.Error("upload session store unavailable", "err", err)
			active.Store(nil)
		} else {
			active.Store(store)
		}
	})

	// Registration stays OUT of the OnReady callback: OnReady fires only from
	auth.HandleRawPrimary("POST /api/v1/uploads", handleCreate)
	auth.HandleRawPrimary("GET /api/v1/uploads/{id}", handleStatus)
	auth.HandleRawPrimary("PATCH /api/v1/uploads/{id}", handleAppend)
	auth.HandleRawPrimary("DELETE /api/v1/uploads/{id}", handleAbort)
}

// StartJanitor sweeps expired sessions in the background until ctx is done.
func StartJanitor(ctx context.Context) {
	go func() {
		t := time.NewTicker(15 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if s := active.Load(); s != nil {
					s.SweepExpired()
				}
			}
		}
	}()
}

func jsonResponse(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	jsonResponse(w, status, map[string]string{"error": msg})
}

// ownerIdentity derives the stable identity a session is bound to. DB tokens
func ownerIdentity(t *db.APIToken) string {
	return strconv.FormatInt(t.ID, 10) + ":" + t.Name
}

// requireOwner authenticates the request for session use: any credential with
// write scope (the same requirement every upload endpoint enforces). It writes
// the error response and returns "" when unauthorized.
func requireOwner(w http.ResponseWriter, r *http.Request) string {
	t := auth.TokenFrom(r.Context())
	if t == nil || !t.HasScope("write") {
		jsonError(w, http.StatusUnauthorized, "authentication required")
		return ""
	}
	return ownerIdentity(t)
}

func activeStore(w http.ResponseWriter) *Store {
	s := active.Load()
	if s == nil {
		jsonError(w, http.StatusServiceUnavailable, "upload sessions unavailable")
	}
	return s
}

type sessionResponse struct {
	ID        string `json:"id"`
	Size      int64  `json:"size"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

func handleCreate(w http.ResponseWriter, r *http.Request) {
	owner := requireOwner(w, r)
	if owner == "" {
		return
	}
	store := activeStore(w)
	if store == nil {
		return
	}
	sess, err := store.Create(owner)
	if err != nil {
		slog.ErrorContext(r.Context(), "create upload session", "err", err)
		jsonError(w, http.StatusInternalServerError, "failed to create upload session")
		return
	}
	jsonResponse(w, http.StatusCreated, sessionResponse{
		ID:        sess.ID(),
		Size:      0,
		ExpiresAt: sess.Expires(store.TTL()).UTC().Format(time.RFC3339),
	})
}

// getSession authenticates the caller and resolves {id} to their session,
func getSession(w http.ResponseWriter, r *http.Request) (*Store, *Session) {
	owner := requireOwner(w, r)
	if owner == "" {
		return nil, nil
	}
	store := activeStore(w)
	if store == nil {
		return nil, nil
	}
	sess, err := store.Get(r.PathValue("id"), owner)
	if err != nil {
		jsonError(w, http.StatusNotFound, ErrNotFound.Error())
		return nil, nil
	}
	return store, sess
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	_, sess := getSession(w, r)
	if sess == nil {
		return
	}
	jsonResponse(w, http.StatusOK, sessionResponse{ID: sess.ID(), Size: sess.Size()})
}

func handleAppend(w http.ResponseWriter, r *http.Request) {
	store, sess := getSession(w, r)
	if sess == nil {
		return
	}

	offsetStr := r.URL.Query().Get("offset")
	if offsetStr == "" {
		offsetStr = r.Header.Get("Upload-Offset")
	}
	offset, err := strconv.ParseInt(offsetStr, 10, 64)
	if err != nil || offset < 0 {
		jsonError(w, http.StatusBadRequest, "offset query parameter (or Upload-Offset header) required")
		return
	}

	size, err := store.Append(sess, offset, r.Body)
	switch {
	case err == nil:
		jsonResponse(w, http.StatusOK, sessionResponse{ID: sess.ID(), Size: size})
	case errors.Is(err, ErrOffsetMismatch), errors.Is(err, ErrBusy):
		jsonResponse(w, http.StatusConflict, map[string]any{"error": err.Error(), "size": size})
	case errors.Is(err, ErrTooLarge):
		jsonResponse(w, http.StatusRequestEntityTooLarge, map[string]any{
			"error": err.Error(), "size": size, "max_size": store.MaxSize(),
		})
	default:
		slog.ErrorContext(r.Context(), "append upload chunk", "session", sess.ID(), "err", err)
		jsonResponse(w, http.StatusInternalServerError, map[string]any{
			"error": "failed to store chunk", "size": size,
		})
	}
}

func handleAbort(w http.ResponseWriter, r *http.Request) {
	store, sess := getSession(w, r)
	if sess == nil {
		return
	}
	store.Remove(sess)
	w.WriteHeader(http.StatusNoContent)
}
