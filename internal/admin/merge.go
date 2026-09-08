package admin

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/go-containers/set"
)

const maxMergeBody = 1 << 16

// DuplicateGroup is a set of projects sharing a repo id across roots, which is
// what a rename leaves behind.
type DuplicateGroup struct {
	RepoID   string           `json:"repo_id"`
	Repo     string           `json:"repo"`
	Projects []DuplicateEntry `json:"projects"`
}

type DuplicateEntry struct {
	Name     string `json:"name"`
	Root     string `json:"root"`
	Repo     string `json:"repo"`
	Releases int    `json:"releases"`
}

// apiDuplicates (GET /api/duplicates) lists the rename leftovers, grouped by
// repo id. A group confined to a shared root is healthy and left out.
func (s *Server) apiDuplicates(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projects, err := s.db.ListProjects(ctx)
	if err != nil {
		s.mergeError(w, r, err)
		return
	}
	byRepo := map[string][]db.Project{}
	for _, p := range projects {
		if p.GithubRepoID != "" {
			byRepo[p.GithubRepoID] = append(byRepo[p.GithubRepoID], p)
		}
	}
	groups := []DuplicateGroup{}
	for repoID, members := range byRepo {
		roots := set.New[string]()
		for _, p := range members {
			roots.Add(projectRoot(p.Name))
		}
		if roots.Len() < 2 {
			continue
		}
		g := DuplicateGroup{RepoID: repoID}
		for _, p := range members {
			releases, err := s.db.ListReleases(ctx, p.ID)
			if err != nil {
				s.mergeError(w, r, err)
				return
			}
			if p.GithubRepo != "" {
				g.Repo = p.GithubRepo
			}
			g.Projects = append(g.Projects, DuplicateEntry{
				Name: p.Name, Root: projectRoot(p.Name), Repo: p.GithubRepo, Releases: len(releases),
			})
		}
		groups = append(groups, g)
	}
	s.writeJSON(w, map[string]any{"groups": groups})
}

func projectRoot(name string) string {
	root, _, _ := strings.Cut(name, "/")
	return root
}

// apiMergePlan (GET /api/projects/{name}/merge-plan?into=X) previews a merge and
// changes nothing.
func (s *Server) apiMergePlan(w http.ResponseWriter, r *http.Request) {
	into := r.URL.Query().Get("into")
	if into == "" {
		http.Error(w, "into is required", http.StatusBadRequest)
		return
	}
	plan, err := s.db.PlanProjectMerge(r.Context(), r.PathValue("name"), into)
	if err != nil {
		s.mergeError(w, r, err)
		return
	}
	s.writeJSON(w, mergePlanJSON(plan))
}

// apiMerge (POST /api/projects/{name}/merge) applies a merge. It snapshots the
// database up front and refuses to proceed when that snapshot fails.
func (s *Server) apiMerge(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body struct {
		Into string `json:"into"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxMergeBody)).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if body.Into == "" {
		http.Error(w, "into is required", http.StatusBadRequest)
		return
	}
	from := r.PathValue("name")

	plan, err := s.db.PlanProjectMerge(ctx, from, body.Into)
	if err != nil {
		s.mergeError(w, r, err)
		return
	}
	if !plan.Applicable() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(mergePlanJSON(plan))
		return
	}

	snap := filepath.Join(filepath.Dir(s.cfg.DBPath), fmt.Sprintf("buildhost-premerge-%s.db", time.Now().UTC().Format("20060102T150405Z")))
	if err := s.db.SnapshotTo(ctx, snap); err != nil {
		slog.ErrorContext(ctx, "merge refused: snapshot failed", "from", from, "into", body.Into, "error", err)
		http.Error(w, "refusing to merge without a snapshot: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.db.ApplyProjectMerge(ctx, plan); err != nil {
		slog.ErrorContext(ctx, "merge failed", "from", from, "into", body.Into, "snapshot", snap, "error", err)
		http.Error(w, err.Error()+" (no changes committed; snapshot at "+snap+")", http.StatusInternalServerError)
		return
	}
	slog.WarnContext(ctx, "projects merged", "from", from, "into", body.Into, "snapshot", snap)

	out := mergePlanJSON(plan)
	out["applied"] = true
	out["snapshot"] = snap
	s.writeJSON(w, out)
}

func mergePlanJSON(p *db.ProjectMergePlan) map[string]any {
	releases := []map[string]any{}
	for _, r := range p.Releases {
		releases = append(releases, map[string]any{
			"id": r.ID, "old_version": r.OldVersion, "new_version": r.NewVersion,
		})
	}
	return map[string]any{
		"from":       p.From.Name,
		"into":       p.Into.Name,
		"repo_id":    p.Into.GithubRepoID,
		"releases":   releases,
		"aliases":    p.Aliases,
		"sites":      p.Sites,
		"oci_tags":   p.OCITags,
		"oci_blobs":  p.OCIBlobs,
		"tokens":     p.Tokens,
		"policies":   p.Policies,
		"conflicts":  p.Conflicts,
		"applicable": p.Applicable(),
		"applied":    false,
	}
}

func (s *Server) mergeError(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "admin merge", "error", err)
	http.Error(w, err.Error(), http.StatusBadRequest)
}
