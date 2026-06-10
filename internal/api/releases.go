package api

//go:generate go run github.com/wow-look-at-my/go-regex-compiler/cmd/go-regex-compiler@latest --regex "^[a-zA-Z0-9][a-zA-Z0-9._+-]{0,127}$" --func validVersion --package api --output gen_version.go --match full
//go:generate gofmt -w gen_version.go
//go:generate go run github.com/wow-look-at-my/go-regex-compiler/cmd/go-regex-compiler@latest --regex "^[a-zA-Z0-9._/-]{1,256}$" --func validGitBranch --package api --output gen_git_branch.go --match full
//go:generate gofmt -w gen_git_branch.go
//go:generate go run github.com/wow-look-at-my/go-regex-compiler/cmd/go-regex-compiler@latest --regex "^[a-fA-F0-9]{1,64}$" --func validGitCommit --package api --output gen_git_commit.go --match full
//go:generate gofmt -w gen_git_commit.go

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

func init() {
	auth.OnReady(func() {
		auth.Handle("POST /api/v1/projects/{project}/releases", parseRoute, handler.CreateRelease)
		auth.Handle("GET /api/v1/projects/{project}/releases", parseRoute, handler.ListReleases)
		auth.Handle("GET /api/v1/projects/{project}/releases/{version}", parseRoute, handler.GetRelease)
	})
}

type createReleaseRequest struct {
	Version   string `json:"version"`
	GitBranch string `json:"git_branch"`
	GitCommit string `json:"git_commit"`
	Notes     string `json:"notes"`
	OciUser   string `json:"oci_user"`
	// DefaultBranch records the repo's default branch on the project so the apex
	// "latest" download tracks it instead of whichever branch published most
	// recently. Optional; empty leaves the project's existing value untouched.
	DefaultBranch string `json:"default_branch"`
}

func (h *Handler) CreateRelease(w http.ResponseWriter, r *http.Request) {
	project := auth.ProjectFrom(r.Context())

	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	var req createReleaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var version string
	var versionNum int64

	if project.Versioning == db.VersioningAuto {
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
		if !validVersion(version) {
			jsonError(w, http.StatusBadRequest, "invalid version string")
			return
		}
		versionNum = semverToNum(version)
	}

	if req.GitBranch != "" && !validGitBranch(req.GitBranch) {
		jsonError(w, http.StatusBadRequest, "invalid git_branch")
		return
	}
	if req.DefaultBranch != "" && !validGitBranch(req.DefaultBranch) {
		jsonError(w, http.StatusBadRequest, "invalid default_branch")
		return
	}
	if req.GitCommit != "" && !validGitCommit(req.GitCommit) {
		jsonError(w, http.StatusBadRequest, "invalid git_commit")
		return
	}
	if len(req.Notes) > 65536 {
		jsonError(w, http.StatusBadRequest, "notes too long")
		return
	}
	if req.OciUser != "" && !validOCIUser(req.OciUser) {
		jsonError(w, http.StatusBadRequest, "invalid oci_user")
		return
	}

	// Record the repo's default branch on the project so apex "latest" tracks it.
	// Best-effort project metadata: a failure here must not fail the publish.
	if req.DefaultBranch != "" {
		if err := h.DB.SetProjectDefaultBranch(r.Context(), project.ID, req.DefaultBranch); err != nil {
			slog.WarnContext(r.Context(), "failed to set project default branch",
				"project", project.Name, "default_branch", req.DefaultBranch, "err", err)
		}
	}

	rel := &db.Release{
		ProjectID:  project.ID,
		Version:    version,
		VersionNum: versionNum,
		GitBranch:  req.GitBranch,
		GitCommit:  req.GitCommit,
		Notes:      req.Notes,
		OciUser:    req.OciUser,
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
	project := auth.ProjectFrom(r.Context())
	rt := routeFrom(r.Context())

	rel := h.getRelease(w, r, project.ID, rt.version)
	if rel == nil {
		return
	}

	jsonResponse(w, http.StatusOK, rel)
}

func (h *Handler) ListReleases(w http.ResponseWriter, r *http.Request) {
	project := auth.ProjectFrom(r.Context())

	releases, err := h.DB.ListReleases(r.Context(), project.ID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to list releases")
		return
	}
	if releases == nil {
		releases = []db.Release{}
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
		if n < 0 {
			n = 0
		}
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

// validOCIUser reports whether s is a valid run-as user for a synthesized OCI image:
// "uid", "uid:gid", "user", or "user:group". Each component is either a numeric id
// (1-10 digits) or a name ([a-zA-Z_][a-zA-Z0-9_-]{0,31}), matching the OCI/Docker User
// field. The empty string ("use the image default", i.e. root) is handled by the caller.
// A plain function (not a go-regex-compiler validator) since this is a cold publish-time
// path and adding a //go:generate directive would invalidate the CI generate approval.
func validOCIUser(s string) bool {
	user, group, hasGroup := strings.Cut(s, ":")
	if !validUserComponent(user) {
		return false
	}
	return !hasGroup || validUserComponent(group)
}

func validUserComponent(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	allDigits := true
	for _, r := range s {
		if r < '0' || r > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		return len(s) <= 10
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
			// allowed in any position
		case (r >= '0' && r <= '9') || r == '-':
			if i == 0 {
				return false // a name may not start with a digit or hyphen
			}
		default:
			return false
		}
	}
	return true
}
