package auth

import (
	"errors"
	"net/http"

	"github.com/wow-look-at-my/buildhost/internal/db"
)

type Middleware struct {
	DB       *db.DB
	Verifier *OIDCVerifier
}

func (m *Middleware) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := ExtractToken(r)
		if raw != "" {
			if LooksLikeJWT(raw) && m.Verifier != nil {
				policies, err := m.DB.ListOIDCPolicies(r.Context())
				if err == nil && len(policies) > 0 {
					if token, err := m.Verifier.VerifyToken(r.Context(), raw, policies); err == nil {
						r = r.WithContext(WithToken(r.Context(), token))
						next.ServeHTTP(w, r)
						return
					}
				}
			}
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

func requireProjectFunc(parse ParseFunc, next http.HandlerFunc) http.HandlerFunc {
	return requireProject(parse)(http.HandlerFunc(next)).ServeHTTP
}

func requireProject(parse ParseFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ri := parse(r)
			if ri.ProjectName() == "" {
				http.NotFound(w, r)
				return
			}

			project, err := mw.DB.GetProject(r.Context(), ri.ProjectName())
			if errors.Is(err, db.ErrNotFound) {
				http.NotFound(w, r)
				return
			}
			if err != nil {
				http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
				return
			}

			t := TokenFrom(r.Context())
			switch ri.Access() {
			case WriteAccess:
				if t == nil || !t.HasScope("write") {
					http.Error(w, `{"error":"authentication required"}`, http.StatusUnauthorized)
					return
				}
				if !t.AuthorizedForProject(project.ID) {
					http.Error(w, `{"error":"token not authorized for this project"}`, http.StatusForbidden)
					return
				}
			case ReadAccess:
				if project.IsPrivate {
					if t == nil || !t.HasScope("read") {
						http.Error(w, `{"error":"authentication required"}`, http.StatusUnauthorized)
						return
					}
					if !t.AuthorizedForProject(project.ID) {
						http.Error(w, `{"error":"token not authorized for this project"}`, http.StatusForbidden)
						return
					}
				}
			}

			ctx := WithRouteInfo(WithProject(r.Context(), project), ri)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
