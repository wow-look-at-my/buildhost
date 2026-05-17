package model

import (
	"strings"
	"time"
)

type Release struct {
	ID          int64      `json:"id"`
	ProjectID   int64      `json:"project_id"`
	Version     string     `json:"version"`
	VersionNum  int64      `json:"version_num"`
	GitBranch   string     `json:"git_branch,omitempty"`
	GitCommit   string     `json:"git_commit,omitempty"`
	Notes       string     `json:"notes,omitempty"`
	Published   bool       `json:"published"`
	CreatedAt   time.Time  `json:"created_at"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
}

func (r Release) IsPrerelease() bool {
	return strings.Contains(r.Version, "-")
}
