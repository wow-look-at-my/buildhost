package auth

import (
	"net/http"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/model"
)

type Middleware struct {
	DB *db.DB
}

func (m *Middleware) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := ExtractToken(r)
		if raw != "" {
			token, err := m.DB.LookupToken(r.Context(), raw)
			if err == nil {
				r = r.WithContext(WithToken(r.Context(), token))
			}
		}
		next.ServeHTTP(w, r)
	})
}

func RequireWrite(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t := TokenFrom(r.Context())
		if t == nil || !t.HasScope("write") {
			http.Error(w, `{"error":"authentication required"}`, http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func RequireReadForProject(next http.HandlerFunc, getProject func(r *http.Request) *model.Project) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		proj := getProject(r)
		if proj != nil && proj.IsPrivate {
			t := TokenFrom(r.Context())
			if t == nil || !t.HasScope("read") {
				http.Error(w, `{"error":"authentication required"}`, http.StatusUnauthorized)
				return
			}
			if t.ProjectID != nil && *t.ProjectID != proj.ID {
				http.Error(w, `{"error":"token not authorized for this project"}`, http.StatusForbidden)
				return
			}
		}
		next(w, r)
	}
}
