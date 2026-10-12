package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Visibility is an override mode.
type Visibility string

const (
	VisibilityAuto    Visibility = "auto"
	VisibilityPublic  Visibility = "public"
	VisibilityPrivate Visibility = "private"
)

// ParseVisibility accepts "auto", "public" or "private". An empty string is
// an error, not "auto": a request that names no mode is a mistake.
func ParseVisibility(s string) (Visibility, error) {
	switch v := Visibility(s); v {
	case VisibilityAuto, VisibilityPublic, VisibilityPrivate:
		return v, nil
	}
	return "", fmt.Errorf(`visibility must be "auto", "public" or "private", got %q`, s)
}

// Apply returns the private state that mode gives over the inherited state.
func (v Visibility) Apply(inheritedPrivate bool) bool {
	switch v {
	case VisibilityPublic:
		return false
	case VisibilityPrivate:
		return true
	}
	return inheritedPrivate
}

// FolderCovers reports whether a folder rule on prefix applies to the project
// name: the project itself, or any project under "<prefix>/".
func FolderCovers(prefix, name string) bool {
	return name == prefix || strings.HasPrefix(name, prefix+"/")
}

// ProjectVisibilityInfo explains how a project's effective visibility follows.
type ProjectVisibilityInfo struct {
	Mode        Visibility `json:"mode"`
	AutoPrivate bool       `json:"auto_private"`
	// FolderPrefix is the longest folder rule that covers the project, or "".
	FolderPrefix string     `json:"folder_prefix"`
	FolderMode   Visibility `json:"folder_mode"`
	Private      bool       `json:"private"`
}

func resolveProject(name string, mode Visibility, autoPrivate bool, folders []VisibilityFolder) ProjectVisibilityInfo {
	info := ProjectVisibilityInfo{Mode: mode, AutoPrivate: autoPrivate, FolderMode: VisibilityAuto}
	for _, f := range folders {
		if FolderCovers(f.Prefix, name) && len(f.Prefix) > len(info.FolderPrefix) {
			info.FolderPrefix = f.Prefix
			info.FolderMode = f.Mode
		}
	}
	info.Private = mode.Apply(info.FolderMode.Apply(autoPrivate))
	return info
}

// projectVisibilityRow returns the project's override row. A project with no
// row (one created before this table) gets one that keeps its current state.
func (d *DB) projectVisibilityRow(ctx context.Context, p *Project) (ProjectVisibility, error) {
	row, err := d.q.GetProjectVisibilityRow(ctx, p.ID)
	if err == nil {
		return row, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return row, fmt.Errorf("get project visibility: %w", err)
	}
	row = ProjectVisibility{ProjectID: p.ID, Mode: VisibilityAuto, AutoPrivate: p.IsPrivate}
	if err := d.q.UpsertProjectVisibilityRow(ctx, UpsertProjectVisibilityRowParams{
		ProjectID: p.ID, Mode: row.Mode, AutoPrivate: row.AutoPrivate,
	}); err != nil {
		return row, fmt.Errorf("insert project visibility: %w", err)
	}
	return row, nil
}

// ProjectVisibility returns the inputs and the result of the project's
// visibility resolution.
func (d *DB) ProjectVisibility(ctx context.Context, p *Project) (ProjectVisibilityInfo, error) {
	row, err := d.projectVisibilityRow(ctx, p)
	if err != nil {
		return ProjectVisibilityInfo{}, err
	}
	folders, err := d.q.ListVisibilityFolders(ctx)
	if err != nil {
		return ProjectVisibilityInfo{}, fmt.Errorf("list visibility folders: %w", err)
	}
	return resolveProject(p.Name, row.Mode, row.AutoPrivate, folders), nil
}

// ListProjectVisibility resolves every project at once, keyed by project ID.
func (d *DB) ListProjectVisibility(ctx context.Context) (map[int64]ProjectVisibilityInfo, error) {
	projects, err := d.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := d.q.ListProjectVisibilityRows(ctx)
	if err != nil {
		return nil, fmt.Errorf("list project visibility: %w", err)
	}
	folders, err := d.q.ListVisibilityFolders(ctx)
	if err != nil {
		return nil, fmt.Errorf("list visibility folders: %w", err)
	}
	byID := make(map[int64]ProjectVisibility, len(rows))
	for _, r := range rows {
		byID[r.ProjectID] = r
	}
	out := make(map[int64]ProjectVisibilityInfo, len(projects))
	for _, p := range projects {
		r, ok := byID[p.ID]
		if !ok {
			r = ProjectVisibility{Mode: VisibilityAuto, AutoPrivate: p.IsPrivate}
		}
		out[p.ID] = resolveProject(p.Name, r.Mode, r.AutoPrivate, folders)
	}
	return out, nil
}

// applyProjectVisibility writes the resolved state to projects.is_private,
// which is the value every read path checks, and updates p to match. The
// UPDATE compares against the stored row, because p can be stale.
func (d *DB) applyProjectVisibility(ctx context.Context, p *Project) error {
	info, err := d.ProjectVisibility(ctx, p)
	if err != nil {
		return err
	}
	if _, err := d.q.ApplyProjectPrivate(ctx, ApplyProjectPrivateParams{
		IsPrivate: info.Private, ID: p.ID, IsPrivate_2: info.Private,
	}); err != nil {
		return fmt.Errorf("set project visibility: %w", err)
	}
	p.IsPrivate = info.Private
	return nil
}

// SyncAutoPrivate records what the source (the GitHub repo) says about the
// project, then re-resolves. An override keeps winning over the new value.
func (d *DB) SyncAutoPrivate(ctx context.Context, p *Project, autoPrivate bool) error {
	row, err := d.projectVisibilityRow(ctx, p)
	if err != nil {
		return err
	}
	if row.AutoPrivate != autoPrivate {
		if err := d.q.UpsertProjectVisibilityRow(ctx, UpsertProjectVisibilityRowParams{
			ProjectID: p.ID, Mode: row.Mode, AutoPrivate: autoPrivate,
		}); err != nil {
			return fmt.Errorf("update project visibility: %w", err)
		}
	}
	return d.applyProjectVisibility(ctx, p)
}

// SetProjectVisibilityMode sets the project's own override.
func (d *DB) SetProjectVisibilityMode(ctx context.Context, p *Project, mode Visibility) error {
	row, err := d.projectVisibilityRow(ctx, p)
	if err != nil {
		return err
	}
	if err := d.q.UpsertProjectVisibilityRow(ctx, UpsertProjectVisibilityRowParams{
		ProjectID: p.ID, Mode: mode, AutoPrivate: row.AutoPrivate,
	}); err != nil {
		return fmt.Errorf("set project visibility mode: %w", err)
	}
	return d.applyProjectVisibility(ctx, p)
}

// ListVisibilityFolders returns every folder rule. A folder with no rule is auto.
func (d *DB) ListVisibilityFolders(ctx context.Context) ([]VisibilityFolder, error) {
	return d.q.ListVisibilityFolders(ctx)
}

// SetVisibilityFolder sets or clears (VisibilityAuto) the rule on a folder,
// then re-resolves every project the folder covers.
func (d *DB) SetVisibilityFolder(ctx context.Context, prefix string, mode Visibility) error {
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		return errors.New("folder prefix is empty")
	}
	var err error
	if mode == VisibilityAuto {
		err = d.q.DeleteVisibilityFolder(ctx, prefix)
	} else {
		err = d.q.UpsertVisibilityFolder(ctx, UpsertVisibilityFolderParams{Prefix: prefix, Mode: mode})
	}
	if err != nil {
		return fmt.Errorf("set visibility folder: %w", err)
	}
	projects, err := d.ListProjects(ctx)
	if err != nil {
		return err
	}
	for i := range projects {
		if !FolderCovers(prefix, projects[i].Name) {
			continue
		}
		if err := d.applyProjectVisibility(ctx, &projects[i]); err != nil {
			return fmt.Errorf("project %q: %w", projects[i].Name, err)
		}
	}
	return nil
}

// ReleaseVisibility returns the release's override, VisibilityAuto if none.
func (d *DB) ReleaseVisibility(ctx context.Context, releaseID int64) (Visibility, error) {
	v, err := d.q.GetReleaseVisibility(ctx, releaseID)
	if errors.Is(err, sql.ErrNoRows) {
		return VisibilityAuto, nil
	}
	if err != nil {
		return "", fmt.Errorf("get release visibility: %w", err)
	}
	return v, nil
}

// ReleaseVisibilities returns every release override of a project.
func (d *DB) ReleaseVisibilities(ctx context.Context, projectID int64) (map[int64]Visibility, error) {
	rows, err := d.q.ListReleaseVisibilityByProject(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("list release visibility: %w", err)
	}
	out := make(map[int64]Visibility, len(rows))
	for _, r := range rows {
		out[r.ReleaseID] = r.Mode
	}
	return out, nil
}

func (d *DB) SetReleaseVisibility(ctx context.Context, releaseID int64, mode Visibility) error {
	if mode == VisibilityAuto {
		return d.q.DeleteReleaseVisibility(ctx, releaseID)
	}
	return d.q.UpsertReleaseVisibility(ctx, UpsertReleaseVisibilityParams{ReleaseID: releaseID, Mode: mode})
}

// SiteVisibility returns the override of a project's site branch,
// VisibilityAuto if none.
func (d *DB) SiteVisibility(ctx context.Context, projectID int64, branch string) (Visibility, error) {
	v, err := d.q.GetSiteVisibility(ctx, GetSiteVisibilityParams{ProjectID: projectID, Branch: branch})
	if errors.Is(err, sql.ErrNoRows) {
		return VisibilityAuto, nil
	}
	if err != nil {
		return "", fmt.Errorf("get site visibility: %w", err)
	}
	return v, nil
}

// SiteVisibilities returns every site override of a project, keyed by branch.
func (d *DB) SiteVisibilities(ctx context.Context, projectID int64) (map[string]Visibility, error) {
	rows, err := d.q.ListSiteVisibilityByProject(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("list site visibility: %w", err)
	}
	out := make(map[string]Visibility, len(rows))
	for _, r := range rows {
		out[r.Branch] = r.Mode
	}
	return out, nil
}

func (d *DB) SetSiteVisibility(ctx context.Context, projectID int64, branch string, mode Visibility) error {
	if mode == VisibilityAuto {
		return d.q.DeleteSiteVisibility(ctx, DeleteSiteVisibilityParams{ProjectID: projectID, Branch: branch})
	}
	return d.q.UpsertSiteVisibility(ctx, UpsertSiteVisibilityParams{ProjectID: projectID, Branch: branch, Mode: mode})
}
