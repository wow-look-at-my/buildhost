package model

import "time"

type APIToken struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	TokenHash   string     `json:"-"`
	TokenPrefix string     `json:"token_prefix"`
	ProjectID   *int64     `json:"project_id,omitempty"`
	Scopes      string     `json:"scopes"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	OIDCProject string     `json:"-"`
}

func (t APIToken) HasScope(scope string) bool {
	for _, s := range splitScopes(t.Scopes) {
		if s == scope {
			return true
		}
	}
	return false
}

func (t APIToken) IsExpired() bool {
	return t.ExpiresAt != nil && t.ExpiresAt.Before(time.Now())
}

func (t APIToken) IsGlobal() bool {
	return t.ProjectID == nil
}

func (t APIToken) AuthorizedForProject(projectID int64) bool {
	return t.ProjectID == nil || *t.ProjectID == projectID
}

func (t APIToken) AuthorizedForProjectName(name string) bool {
	if t.OIDCProject != "" {
		return t.OIDCProject == name
	}
	return true
}

var ValidScopes = map[string]bool{
	"read":  true,
	"write": true,
}

func splitScopes(s string) []string {
	var out []string
	start := 0
	for i := range len(s) {
		if s[i] == ',' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
