package admin

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

func (s *Server) apiSidebar(w http.ResponseWriter, r *http.Request) {
	s.cpuMu.Lock()
	cpuPct := s.cpuPercent
	s.cpuMu.Unlock()

	resp := map[string]any{
		"build": map[string]any{
			"version":      s.build.Version,
			"commit":       s.build.Commit,
			"commit_url":   s.build.CommitURL(),
			"short_commit": s.build.ShortCommit(),
			"date":         s.build.Date,
		},
		"build_age":   s.buildAge(),
		"cpu_percent": fmt.Sprintf("%.1f%%", cpuPct),
	}

	if du, err := getDiskUsage(s.cfg.DataDir); err == nil && du.Total > 0 {
		resp["disk_used"] = humanSize(int64(du.Used))
		resp["disk_total"] = humanSize(int64(du.Total))
	}

	s.writeJSON(w, resp)
}

func (s *Server) apiDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	stats, err := s.db.GetDashboardStats(ctx)
	if err != nil {
		slog.Error("admin api error", "err", err, "path", r.URL.Path)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	recent, err := s.db.ListRecentReleases(ctx, 10)
	if err != nil {
		slog.Error("admin api error", "err", err, "path", r.URL.Path)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if recent == nil {
		recent = []db.RecentRelease{}
	}

	s.cpuMu.Lock()
	cpuPct := s.cpuPercent
	cpuTotal := s.cpuTotal
	s.cpuMu.Unlock()

	s.writeJSON(w, map[string]any{
		"stats":  stats,
		"recent": recent,
		"config": map[string]any{
			"base_url":          auth.RequestRootURL(r),
			"listen_addr":       s.cfg.ListenAddr,
			"admin_listen_addr": s.cfg.AdminListenAddr,
			"data_dir":          s.cfg.DataDir,
			"oidc_issuers":      s.cfg.OIDCIssuers,
			"oidc_orgs":         s.cfg.OIDCOrgs,
			"oidc_events":       s.cfg.OIDCEvents,
		},
		"services": serviceURLs(r),
		"build": map[string]any{
			"version":      s.build.Version,
			"commit":       s.build.Commit,
			"commit_url":   s.build.CommitURL(),
			"short_commit": s.build.ShortCommit(),
			"date":         s.build.Date,
		},
		"uptime":      formatDuration(time.Since(s.startTime)),
		"cpu_percent": fmt.Sprintf("%.1f%%", cpuPct),
		"cpu_total":   formatDuration(cpuTotal),
		"disk_bytes":  blobsDiskUsage(s.cfg.DataDir + "/blobs"),
	})
}

func (s *Server) apiProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.db.ListProjectSummaries(r.Context())
	if err != nil {
		slog.Error("admin api error", "err", err, "path", r.URL.Path)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if projects == nil {
		projects = []db.ProjectSummary{}
	}
	s.writeJSON(w, projects)
}

func (s *Server) apiProject(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := r.PathValue("name")

	project, err := s.db.GetProject(ctx, name)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("admin api error", "err", err, "path", r.URL.Path)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	releases, err := s.db.ListReleaseSummaries(ctx, project.ID)
	if err != nil {
		slog.Error("admin api error", "err", err, "path", r.URL.Path)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if releases == nil {
		releases = []db.ReleaseSummary{}
	}

	sites, err := s.db.ListSites(ctx, project.ID)
	if err != nil {
		slog.Error("admin api error", "err", err, "path", r.URL.Path)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	s.writeJSON(w, map[string]any{
		"project":  project,
		"releases": releases,
		"sites":    sites,
		"base_url": auth.RequestRootURL(r),
		"services": serviceURLs(r),
	})
}

func (s *Server) apiRelease(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := r.PathValue("name")
	version := r.PathValue("version")

	project, err := s.db.GetProject(ctx, name)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("admin api error", "err", err, "path", r.URL.Path)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	release, err := s.db.GetRelease(ctx, project.ID, version)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("admin api error", "err", err, "path", r.URL.Path)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	rows, pkgs, err := s.db.ListArtifactDetails(ctx, release.ID)
	if err != nil {
		slog.Error("admin api error", "err", err, "path", r.URL.Path)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	totalDownloads, err := s.db.GetTotalDownloads(ctx, release.ID)
	if err != nil {
		slog.Error("admin api error", "err", err, "path", r.URL.Path)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	type artifactView struct {
		db.ListArtifactDetailsWithDownloadsRow
		Packages []db.ListPackagedFormatsRow `json:"packages"`
	}
	artifacts := make([]artifactView, len(rows))
	var totalSize int64
	for i, a := range rows {
		artifacts[i] = artifactView{ListArtifactDetailsWithDownloadsRow: a, Packages: pkgs[i]}
		totalSize += a.Size
	}

	s.writeJSON(w, map[string]any{
		"project":         project,
		"release":         release,
		"artifacts":       artifacts,
		"total_downloads": totalDownloads,
		"total_size":      totalSize,
		"base_url":        auth.RequestRootURL(r),
		"services":        serviceURLs(r),
	})
}

func (s *Server) apiRegistries(w http.ResponseWriter, r *http.Request) {
	projects, err := s.db.ListProjectSummaries(r.Context())
	if err != nil {
		slog.Error("admin api error", "err", err, "path", r.URL.Path)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if projects == nil {
		projects = []db.ProjectSummary{}
	}

	s.writeJSON(w, map[string]any{
		"base_url": auth.RequestRootURL(r),
		"services": serviceURLs(r),
		"projects": projects,
	})
}

func (s *Server) apiSites(w http.ResponseWriter, r *http.Request) {
	sites, err := s.db.ListSiteDetails(r.Context())
	if err != nil {
		slog.Error("admin api error", "err", err, "path", r.URL.Path)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if sites == nil {
		sites = []db.SiteDetail{}
	}
	s.writeJSON(w, map[string]any{
		"sites":    sites,
		"base_url": auth.RequestRootURL(r),
		"services": serviceURLs(r),
	})
}

func (s *Server) apiOIDC(w http.ResponseWriter, r *http.Request) {
	policies, err := s.db.ListOIDCPolicyDetails(r.Context())
	if err != nil {
		slog.Error("admin api error", "err", err, "path", r.URL.Path)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if policies == nil {
		policies = []db.OIDCPolicyDetail{}
	}
	s.writeJSON(w, policies)
}

func (s *Server) apiArtifacts(w http.ResponseWriter, r *http.Request) {
	artifacts, err := s.db.ListAllArtifacts(r.Context())
	if err != nil {
		slog.Error("admin api error", "err", err, "path", r.URL.Path)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if artifacts == nil {
		artifacts = []db.AllArtifact{}
	}
	s.writeJSON(w, artifacts)
}

func (s *Server) apiStorage(w http.ResponseWriter, r *http.Request) {
	breakdown, err := s.db.GetStorageBreakdown(r.Context())
	if err != nil {
		slog.Error("admin api error", "err", err, "path", r.URL.Path)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if breakdown == nil {
		breakdown = []db.StorageBreakdown{}
	}

	stats, err := s.db.GetDashboardStats(r.Context())
	if err != nil {
		slog.Error("admin api error", "err", err, "path", r.URL.Path)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	resp := map[string]any{
		"projects":       breakdown,
		"logical_bytes":  stats.LogicalBytes,
		"physical_bytes": stats.PhysicalBytes,
		"total_bytes":    stats.TotalStorageBytes,
		"stripped_bytes": stats.StrippedBytes,
		"debug_bytes":    stats.DebugBytes,
		"packaged_bytes": stats.PackagedBytes,
		"disk_bytes":     blobsDiskUsage(s.cfg.DataDir + "/blobs"),
	}

	// Upper-bound estimate of what keep-N eviction would free (does not subtract
	// dedup-shared blobs). Omitted on error so the endpoint still returns.
	cutoff := time.Now().Add(-s.cfg.RetentionRecencyGuard)
	if reclaimable, err := s.db.SumReclaimableBytes(r.Context(), int64(s.cfg.RetentionKeepN), cutoff); err == nil {
		resp["reclaimable_bytes"] = reclaimable
	} else {
		slog.Error("admin api error", "err", err, "path", r.URL.Path)
	}

	if du, err := getDiskUsage(s.cfg.DataDir); err == nil && du.Total > 0 {
		resp["disk_used"] = du.Used
		resp["disk_total"] = du.Total
	}

	s.writeJSON(w, resp)
}
