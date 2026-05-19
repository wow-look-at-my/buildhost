package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/model"
)

type createProjectRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Homepage    string `json:"homepage"`
	License     string `json:"license"`
	IsPrivate   bool   `json:"is_private"`
	Versioning  string `json:"versioning"`
}

func (h *Handler) CreateProject(w http.ResponseWriter, r *http.Request) {
	if h.requireWrite(w, r) == nil {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	var req createProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name == "" {
		jsonError(w, http.StatusBadRequest, "name is required")
		return
	}

	versioning := model.Versioning(req.Versioning)
	if versioning == "" {
		versioning = model.VersioningAuto
	}
	if versioning != model.VersioningAuto && versioning != model.VersioningSemver {
		jsonError(w, http.StatusBadRequest, "versioning must be 'auto' or 'semver'")
		return
	}

	p := &model.Project{
		Name:        req.Name,
		Description: req.Description,
		Homepage:    req.Homepage,
		License:     req.License,
		IsPrivate:   req.IsPrivate,
		Versioning:  versioning,
	}

	if err := h.DB.CreateProject(r.Context(), p); err != nil {
		if errors.Is(err, db.ErrConflict) {
			jsonError(w, http.StatusConflict, "project already exists")
			return
		}
		jsonError(w, http.StatusInternalServerError, "failed to create project")
		return
	}

	jsonResponse(w, http.StatusCreated, p)
}

func (h *Handler) GetProject(w http.ResponseWriter, r *http.Request) {
	project := h.getProject(w, r, r.PathValue("project"))
	if project == nil {
		return
	}
	if !h.checkReadAccess(w, r, project) {
		return
	}
	jsonResponse(w, http.StatusOK, project)
}

func (h *Handler) ListProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := h.DB.ListProjects(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to list projects")
		return
	}

	t := auth.TokenFrom(r.Context())
	var visible []model.Project
	for _, p := range projects {
		if !p.IsPrivate {
			visible = append(visible, p)
		} else if t != nil && t.HasScope("read") && t.AuthorizedForProject(p.ID) {
			visible = append(visible, p)
		}
	}
	if visible == nil {
		visible = []model.Project{}
	}

	jsonResponse(w, http.StatusOK, visible)
}
