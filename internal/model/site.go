package model

import "time"

type Site struct {
	ID         int64     `json:"id"`
	ProjectID  int64     `json:"project_id"`
	Branch     string    `json:"branch"`
	StorageKey string    `json:"storage_key"`
	Size       int64     `json:"size"`
	SHA256     string    `json:"sha256"`
	FileCount  int       `json:"file_count"`
	GitCommit  string    `json:"git_commit,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}
