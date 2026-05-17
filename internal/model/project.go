package model

import "time"

type Versioning string

const (
	VersioningAuto   Versioning = "auto"
	VersioningSemver Versioning = "semver"
)

type Project struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Homepage    string     `json:"homepage,omitempty"`
	License     string     `json:"license,omitempty"`
	IsPrivate   bool       `json:"is_private"`
	Versioning  Versioning `json:"versioning"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}
