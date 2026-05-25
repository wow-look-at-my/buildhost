package auth

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/wow-look-at-my/buildhost/internal/db"
)

var authTracer = otel.Tracer("buildhost.auth")

type Middleware struct {
	DB       *db.DB
	Verifier *OIDCVerifier
}

func (m *Middleware) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := ExtractToken(r)
		if raw != "" {
			if LooksLikeJWT(raw) && m.Verifier != nil {
				ctx, span := authTracer.Start(r.Context(), "auth.verify_oidc")
				policies, _ := m.DB.ListOIDCPolicies(ctx)
				var vr VerifyResult
				token, oidcProject, err := m.Verifier.VerifyTokenFull(ctx, raw, policies, &vr)
				if err != nil {
					span.SetAttributes(attribute.String("auth.result", "oidc_failed"))
					span.End()
					slog.Debug("OIDC verification failed", "err", err)
				} else {
					span.SetAttributes(attribute.String("auth.result", "oidc_ok"))
					span.End()
					parentSpan := trace.SpanFromContext(r.Context())
					parentSpan.SetAttributes(attribute.String("auth.type", "oidc"))
					rctx := WithToken(r.Context(), token)
					if oidcProject != "" {
						rctx = WithOIDCProject(rctx, oidcProject)
						rctx = WithOIDCPrivate(rctx, vr.OIDCPrivate)
					}
					r = r.WithContext(rctx)
					next.ServeHTTP(w, r)
					return
				}
			}
			token, err := m.DB.LookupToken(r.Context(), raw)
			if err == nil {
				parentSpan := trace.SpanFromContext(r.Context())
				parentSpan.SetAttributes(
					attribute.String("auth.type", "token"),
					attribute.String("auth.token_prefix", token.TokenPrefix),
				)
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

func unauthorizedResponse(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/v2/") {
		w.Header().Set("Www-Authenticate", `Basic realm="buildhost"`)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"errors":[{"code":"UNAUTHORIZED","message":"authentication required"}]}`))
		return
	}
	http.Error(w, `{"error":"authentication required"}`, http.StatusUnauthorized)
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

			parentSpan := trace.SpanFromContext(r.Context())
			parentSpan.SetAttributes(attribute.String("project.name", ri.ProjectName()))

			project, err := mw.DB.GetProject(r.Context(), ri.ProjectName())
			if errors.Is(err, db.ErrNotFound) {
				t := TokenFrom(r.Context())
				oidcProject := OIDCProjectFrom(r.Context())
				if t == nil || oidcProject == "" || oidcProject != ri.ProjectName() {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusNotFound)
					w.Write([]byte(`{"error":"project not found"}`))
					return
				}
				oidcPrivate, _ := OIDCPrivateFrom(r.Context())
				project = &db.Project{Name: ri.ProjectName(), Versioning: db.VersioningAuto, IsPrivate: oidcPrivate}
				createErr := mw.DB.CreateProject(r.Context(), project)
				if createErr != nil && !errors.Is(createErr, db.ErrConflict) {
					http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
					return
				}
				if errors.Is(createErr, db.ErrConflict) {
					project, err = mw.DB.GetProject(r.Context(), ri.ProjectName())
					if err != nil {
						http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
						return
					}
				}
				parentSpan.SetAttributes(attribute.Bool("project.auto_created", true))
				err = nil
			}
			if err != nil {
				parentSpan.RecordError(err)
				parentSpan.SetStatus(codes.Error, "project lookup failed")
				http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
				return
			}

			t := TokenFrom(r.Context())
			oidcProject := OIDCProjectFrom(r.Context())
			if t != nil && oidcProject != "" && oidcProject == project.Name {
				oidcPrivate, hasPrivate := OIDCPrivateFrom(r.Context())
				if hasPrivate && project.IsPrivate != oidcPrivate {
					if updateErr := mw.DB.SetProjectVisibility(r.Context(), project.ID, oidcPrivate); updateErr == nil {
						slog.WarnContext(r.Context(), "OIDC visibility sync",
							"project", project.Name,
							"was_private", project.IsPrivate,
							"now_private", oidcPrivate,
							"oidc_subject", t.Name,
						)
						project.IsPrivate = oidcPrivate
						parentSpan.SetAttributes(attribute.Bool("project.visibility_synced", true))
					}
				}
			}
			switch ri.Access() {
			case WriteAccess:
				parentSpan.SetAttributes(attribute.String("project.access", "write"))
				if t == nil || !t.HasScope("write") {
					unauthorizedResponse(w, r)
					return
				}
				if !t.AuthorizedForProject(project.ID) || (oidcProject != "" && oidcProject != project.Name) {
					http.Error(w, `{"error":"token not authorized for this project"}`, http.StatusForbidden)
					return
				}
			case ReadAccess:
				parentSpan.SetAttributes(attribute.String("project.access", "read"))
				if project.IsPrivate {
					if t == nil || !t.HasScope("read") {
						unauthorizedResponse(w, r)
						return
					}
					if !t.AuthorizedForProject(project.ID) || (oidcProject != "" && oidcProject != project.Name) {
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
