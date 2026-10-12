package admin

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/db"
)

// Visibility overrides.

type visibilityRequest struct {
	Mode   string `json:"mode"`
	Prefix string `json:"prefix"`
	Branch string `json:"branch"`
}

func decodeVisibility(w http.ResponseWriter, r *http.Request) (visibilityRequest, db.Visibility, bool) {
	var req visibilityRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return req, "", false
	}
	mode, err := db.ParseVisibility(req.Mode)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return req, "", false
	}
	return req, mode, true
}

// projectOr404 loads the {name} project, or writes the error response.
func (s *Server) projectOr404(w http.ResponseWriter, r *http.Request) (*db.Project, bool) {
	project, err := s.db.GetProject(r.Context(), r.PathValue("name"))
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return nil, false
	}
	if err != nil {
		slog.Error("admin api error", "err", err, "path", r.URL.Path)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return nil, false
	}
	return project, true
}

func (s *Server) apiVisibilityFolders(w http.ResponseWriter, r *http.Request) {
	folders, err := s.db.ListVisibilityFolders(r.Context())
	if err != nil {
		slog.Error("admin api error", "err", err, "path", r.URL.Path)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, folders)
}

// apiSetVisibilityFolder sets the rule on a folder. "auto" removes it.
func (s *Server) apiSetVisibilityFolder(w http.ResponseWriter, r *http.Request) {
	req, mode, ok := decodeVisibility(w, r)
	if !ok {
		return
	}
	prefix := strings.Trim(req.Prefix, "/")
	if prefix == "" {
		http.Error(w, "prefix is required", http.StatusBadRequest)
		return
	}
	if err := s.db.SetVisibilityFolder(r.Context(), prefix, mode); err != nil {
		slog.Error("admin set folder visibility", "err", err, "prefix", prefix)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	slog.Warn("folder visibility changed", "prefix", prefix, "mode", mode)
	s.writeJSON(w, map[string]any{"prefix": prefix, "mode": mode})
}

func (s *Server) apiSetProjectVisibility(w http.ResponseWriter, r *http.Request) {
	_, mode, ok := decodeVisibility(w, r)
	if !ok {
		return
	}
	project, ok := s.projectOr404(w, r)
	if !ok {
		return
	}
	if err := s.db.SetProjectVisibilityMode(r.Context(), project, mode); err != nil {
		slog.Error("admin set project visibility", "err", err, "project", project.Name)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	info, err := s.db.ProjectVisibility(r.Context(), project)
	if err != nil {
		slog.Error("admin set project visibility", "err", err, "project", project.Name)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	slog.Warn("project visibility changed", "project", project.Name, "mode", mode, "private", info.Private)
	s.writeJSON(w, map[string]any{"name": project.Name, "visibility": info})
}

func (s *Server) apiSetReleaseVisibility(w http.ResponseWriter, r *http.Request) {
	_, mode, ok := decodeVisibility(w, r)
	if !ok {
		return
	}
	project, ok := s.projectOr404(w, r)
	if !ok {
		return
	}
	release, err := s.db.GetRelease(r.Context(), project.ID, r.PathValue("version"))
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		slog.Error("admin api error", "err", err, "path", r.URL.Path)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := s.db.SetReleaseVisibility(r.Context(), release.ID, mode); err != nil {
		slog.Error("admin set release visibility", "err", err, "project", project.Name, "version", release.Version)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	slog.Warn("release visibility changed", "project", project.Name, "version", release.Version, "mode", mode)
	s.writeJSON(w, map[string]any{
		"name": project.Name, "version": release.Version, "mode": mode,
		"private": mode.Apply(project.IsPrivate),
	})
}

// apiSetSiteVisibility takes the branch in the body: a branch name can hold "/".
func (s *Server) apiSetSiteVisibility(w http.ResponseWriter, r *http.Request) {
	req, mode, ok := decodeVisibility(w, r)
	if !ok {
		return
	}
	if req.Branch == "" {
		http.Error(w, "branch is required", http.StatusBadRequest)
		return
	}
	project, ok := s.projectOr404(w, r)
	if !ok {
		return
	}
	if err := s.db.SetSiteVisibility(r.Context(), project.ID, req.Branch, mode); err != nil {
		slog.Error("admin set site visibility", "err", err, "project", project.Name, "branch", req.Branch)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	slog.Warn("site visibility changed", "project", project.Name, "branch", req.Branch, "mode", mode)
	s.writeJSON(w, map[string]any{"name": project.Name, "branch": req.Branch, "mode": mode})
}
