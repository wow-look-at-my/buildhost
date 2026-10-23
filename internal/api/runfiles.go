package api

import (
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/retention"
)

// A run file is a file one job of a CI run hands to later jobs of the same
// run. It is not a release, nothing outside the run reads it, and the run
// files janitor deletes it after the run ends.
func init() {
	auth.HandleRawPrimary("GET /api/v1/run-files", handler.ListRunFiles)
	auth.HandleRawPrimary("PUT /api/v1/run-files/{name}", handler.PutRunFile)
	auth.HandleRawPrimary("GET /api/v1/run-files/{name}", handler.GetRunFile)
}

var validRunFileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type runFileResponse struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// runKey returns the run the request's OIDC token names. The request also
// names the run, and a request that names another run is refused rather than
// served from the token's run. It writes the error and returns false.
func runKey(w http.ResponseWriter, r *http.Request) (db.RunKey, auth.OIDCRepoIdentity, bool) {
	repo, ok := auth.RunRepoFrom(r.Context())
	if !ok && auth.TokenFrom(r.Context()) == nil {
		jsonError(w, http.StatusUnauthorized, "authentication required: present the job's GitHub Actions OIDC token")
		return db.RunKey{}, repo, false
	}
	if !ok || repo.RunID == "" {
		jsonError(w, http.StatusForbidden, "a run file needs a GitHub Actions OIDC token that names its repository id and run_id")
		return db.RunKey{}, repo, false
	}
	q := r.URL.Query()
	if !strings.EqualFold(q.Get("repository"), repo.RepoPath) || q.Get("run_id") != repo.RunID {
		jsonError(w, http.StatusForbidden, "the request names run "+q.Get("repository")+"#"+q.Get("run_id")+", but the token was minted by run "+repo.RepoPath+"#"+repo.RunID)
		return db.RunKey{}, repo, false
	}
	return db.RunKey{RepoID: repo.RepoID, RunID: repo.RunID}, repo, true
}

func runFileName(w http.ResponseWriter, r *http.Request) (string, bool) {
	name := r.PathValue("name")
	if !validRunFileName.MatchString(name) {
		jsonError(w, http.StatusBadRequest, "invalid run file name: want 1-128 of [A-Za-z0-9._-], starting with a letter or digit")
		return "", false
	}
	return name, true
}

// PutRunFile stores the body under a name for the caller's run. A second put
// of the same name replaces the first, so a re-run of a job sends again.
func (h *Handler) PutRunFile(w http.ResponseWriter, r *http.Request) {
	key, repo, ok := runKey(w, r)
	if !ok {
		return
	}
	name, ok := runFileName(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if err := h.DB.TouchRun(ctx, key, repo.RepoPath); err != nil {
		slog.ErrorContext(ctx, "run file: touch run", "err", err)
		jsonError(w, http.StatusInternalServerError, "failed to record run")
		return
	}

	storageKey, size, ok := h.storeCIBody(w, r, "run file")
	if !ok {
		return
	}

	replaced, err := h.DB.PutRunFile(ctx, key, name, db.RunBlob{StorageKey: storageKey, Size: size})
	if err != nil {
		slog.ErrorContext(ctx, "run file: record", "err", err)
		jsonError(w, http.StatusInternalServerError, "failed to record run file")
		return
	}
	if replaced != "" {
		if _, err := retention.DeleteBlobIfUnreferenced(ctx, h.DB, h.Store, replaced, true); err != nil {
			slog.ErrorContext(ctx, "run file: delete replaced blob", "key", replaced, "err", err)
		}
	}
	slog.InfoContext(ctx, "run file stored", "repo", repo.RepoPath, "run_id", key.RunID, "name", name, "size", size)
	jsonResponse(w, http.StatusCreated, runFileResponse{Name: name, Size: size, SHA256: storageKey})
}

// GetRunFile serves a file the caller's run holds. It honors Range, so a
// download that breaks can resume.
func (h *Handler) GetRunFile(w http.ResponseWriter, r *http.Request) {
	key, repo, ok := runKey(w, r)
	if !ok {
		return
	}
	name, ok := runFileName(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	file, found, err := h.DB.GetRunFile(ctx, key, name)
	if err != nil {
		slog.ErrorContext(ctx, "run file: read", "err", err)
		jsonError(w, http.StatusInternalServerError, "failed to read run file")
		return
	}
	if !found {
		jsonError(w, http.StatusNotFound, h.missingRunFile(r, key, repo, name))
		return
	}
	if err := h.DB.TouchRun(ctx, key, repo.RepoPath); err != nil {
		slog.ErrorContext(ctx, "run file: touch run", "err", err)
		jsonError(w, http.StatusInternalServerError, "failed to record run")
		return
	}
	// A resumed download asks again with Range. Only the first request of a
	// download counts as a read.
	if r.Header.Get("Range") == "" {
		if err := h.DB.MarkRunFileRead(ctx, key, name); err != nil {
			slog.ErrorContext(ctx, "run file: count read", "err", err)
		}
	}

	h.serveCIBlob(w, r, name, file.StorageKey, "run file")
}

// missingRunFile says what the run does hold, so a typo and a send that never
// ran read differently.
func (h *Handler) missingRunFile(r *http.Request, key db.RunKey, repo auth.OIDCRepoIdentity, name string) string {
	msg := "run " + repo.RepoPath + "#" + key.RunID + " holds no file named " + name
	files, err := h.DB.ListRunFiles(r.Context(), key)
	if err != nil || len(files) == 0 {
		return msg + "; it holds no files"
	}
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, f.Name)
	}
	return msg + "; it holds: " + strings.Join(names, ", ")
}

// ListRunFiles lists the files the caller's run holds.
func (h *Handler) ListRunFiles(w http.ResponseWriter, r *http.Request) {
	key, _, ok := runKey(w, r)
	if !ok {
		return
	}
	files, err := h.DB.ListRunFiles(r.Context(), key)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to list run files")
		return
	}
	out := make([]runFileResponse, 0, len(files))
	for _, f := range files {
		out = append(out, runFileResponse{Name: f.Name, Size: f.Size, SHA256: f.StorageKey})
	}
	jsonResponse(w, http.StatusOK, out)
}
