package model

import "time"

type OIDCPolicy struct {
	ID             int64     `json:"id"`
	Issuer         string    `json:"issuer"`
	SubjectPattern string    `json:"subject_pattern"`
	Audience       string    `json:"audience,omitempty"`
	ProjectID      *int64    `json:"project_id,omitempty"`
	Scopes         string    `json:"scopes"`
	CreatedAt      time.Time `json:"created_at"`
}
