package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/retention"
)

const maxWebhookBody = 1 << 20 // 1 MiB

func init() {
	auth.OnReady(func() {
		auth.HandleRaw("POST /api/v1/webhooks/github", handler.GitHubWebhook)
	})
}

type githubDeleteEvent struct {
	Ref        string `json:"ref"`
	RefType    string `json:"ref_type"`
	Repository struct {
		Name string `json:"name"`
	} `json:"repository"`
}

func (h *Handler) GitHubWebhook(w http.ResponseWriter, r *http.Request) {
	if h.GitHubWebhookSecret == "" {
		jsonError(w, http.StatusNotFound, "webhook not configured")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !validGitHubSignature(r.Header.Get("X-Hub-Signature-256"), body, h.GitHubWebhookSecret) {
		jsonError(w, http.StatusUnauthorized, "invalid webhook signature")
		return
	}

	event := r.Header.Get("X-GitHub-Event")
	switch event {
	case "ping":
		jsonResponse(w, http.StatusOK, map[string]any{"ok": true})
	case "delete":
		h.handleGitHubDelete(w, r, body)
	default:
		jsonResponse(w, http.StatusOK, map[string]any{"ok": true, "ignored": true})
	}
}

func (h *Handler) handleGitHubDelete(w http.ResponseWriter, r *http.Request, body []byte) {
	var event githubDeleteEvent
	if err := json.Unmarshal(body, &event); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid delete payload")
		return
	}

	if event.RefType != "branch" {
		jsonResponse(w, http.StatusOK, map[string]any{"ok": true, "ignored": true})
		return
	}

	repoName := strings.ToLower(strings.TrimSpace(event.Repository.Name))
	branch := strings.TrimSpace(event.Ref)
	if repoName == "" || branch == "" {
		jsonError(w, http.StatusBadRequest, "repository.name and ref are required")
		return
	}
	if !validProjectName(repoName) || !validGitBranch(branch) {
		jsonError(w, http.StatusBadRequest, "invalid repository or branch name")
		return
	}

	deleted, err := h.DB.DeleteSitesByRepositoryBranch(r.Context(), repoName, branch)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to delete branch sites")
		return
	}

	blobsDeleted := 0
	seenKeys := map[string]struct{}{}
	for _, site := range deleted {
		if site.StorageKey == "" {
			continue
		}
		if _, seen := seenKeys[site.StorageKey]; seen {
			continue
		}
		seenKeys[site.StorageKey] = struct{}{}
		ok, err := retention.DeleteBlobIfUnreferenced(r.Context(), h.DB, h.Store, site.StorageKey, true)
		if err != nil {
			slog.WarnContext(r.Context(), "github webhook: failed to delete unreferenced site blob",
				"repository", repoName, "branch", branch, "project", site.ProjectName, "key", site.StorageKey, "err", err)
			continue
		}
		if ok {
			blobsDeleted++
		}
	}

	jsonResponse(w, http.StatusOK, map[string]any{
		"ok":            true,
		"repository":    repoName,
		"branch":        branch,
		"sites_deleted": len(deleted),
		"blobs_deleted": blobsDeleted,
	})
}

func validGitHubSignature(header string, body []byte, secret string) bool {
	if !strings.HasPrefix(header, "sha256=") {
		return false
	}
	got, err := hex.DecodeString(strings.TrimPrefix(header, "sha256="))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	want := mac.Sum(nil)
	return hmac.Equal(got, want)
}
