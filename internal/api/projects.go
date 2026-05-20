package api

//go:generate go run github.com/wow-look-at-my/go-regex-compiler/cmd/regex-gen@latest -regex ^[a-z0-9][a-z0-9._-]{0,127}$ -func validProjectName -package api -output gen_project_name.go -match full

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/model"
)

func init() {
	auth.HandleRaw("POST /api/v1/projects", handler.CreateProject)
	auth.HandleRaw("GET /api/v1/projects", handler.ListProjects)
	auth.Handle("GET /api/v1/projects/{project}", parseRoute, handler.GetProject)
}

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
	if !validProjectName(req.Name) {
		jsonError(w, http.StatusBadRequest, "name must match [a-z0-9][a-z0-9._-]{0,127}")
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
	jsonResponse(w, http.StatusOK, auth.ProjectFrom(r.Context()))
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
