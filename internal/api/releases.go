package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/model"
)

type createReleaseRequest struct {
	Version   string `json:"version"`
	GitBranch string `json:"git_branch"`
	GitCommit string `json:"git_commit"`
	Notes     string `json:"notes"`
}

func (h *Handler) CreateRelease(w http.ResponseWriter, r *http.Request) {
	t := h.requireWrite(w, r)
	if t == nil {
		return
	}

	project := h.getProject(w, r, r.PathValue("project"))
	if project == nil {
		return
	}

	if !t.AuthorizedForProject(project.ID) {
		jsonError(w, http.StatusForbidden, "token not authorized for this project")
		return
	}

	var req createReleaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var version string
	var versionNum int64

	if project.Versioning == model.VersioningAuto {
		nextNum, err := h.DB.NextVersionNum(r.Context(), project.ID)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to determine next version")
			return
		}
		versionNum = nextNum
		version = fmt.Sprintf("%d", nextNum)
		if req.Version != "" {
			num, err := strconv.ParseInt(req.Version, 10, 64)
			if err == nil {
				versionNum = num
				version = req.Version
			}
		}
	} else {
		if req.Version == "" {
			jsonError(w, http.StatusBadRequest, "version is required for semver projects")
			return
		}
		version = strings.TrimPrefix(req.Version, "v")
		versionNum = semverToNum(version)
	}

	rel := &model.Release{
		ProjectID:  project.ID,
		Version:    version,
		VersionNum: versionNum,
		GitBranch:  req.GitBranch,
		GitCommit:  req.GitCommit,
		Notes:      req.Notes,
	}

	if err := h.DB.CreateRelease(r.Context(), rel); err != nil {
		if errors.Is(err, db.ErrConflict) {
			jsonError(w, http.StatusConflict, "release already exists")
			return
		}
		jsonError(w, http.StatusInternalServerError, "failed to create release")
		return
	}

	jsonResponse(w, http.StatusCreated, rel)
}

func (h *Handler) GetRelease(w http.ResponseWriter, r *http.Request) {
	project := h.getProject(w, r, r.PathValue("project"))
	if project == nil {
		return
	}
	if !h.checkReadAccess(w, r, project) {
		return
	}

	rel := h.getRelease(w, r, project.ID, r.PathValue("version"))
	if rel == nil {
		return
	}

	jsonResponse(w, http.StatusOK, rel)
}

func (h *Handler) ListReleases(w http.ResponseWriter, r *http.Request) {
	project := h.getProject(w, r, r.PathValue("project"))
	if project == nil {
		return
	}
	if !h.checkReadAccess(w, r, project) {
		return
	}

	releases, err := h.DB.ListReleases(r.Context(), project.ID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to list releases")
		return
	}
	if releases == nil {
		releases = []model.Release{}
	}

	jsonResponse(w, http.StatusOK, releases)
}

func semverToNum(v string) int64 {
	parts := strings.SplitN(v, "-", 2)
	v = parts[0]

	segments := strings.Split(v, ".")
	var num int64
	for i, s := range segments {
		if i >= 3 {
			break
		}
		n, _ := strconv.ParseInt(s, 10, 64)
		switch i {
		case 0:
			num += n * 1_000_000
		case 1:
			num += n * 1_000
		case 2:
			num += n
		}
	}
	return num
}
