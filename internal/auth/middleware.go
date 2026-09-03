package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	GitHub   *GitHubAuth
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
					r = r.WithContext(WithOIDCError(r.Context(), err))
				} else {
					span.SetAttributes(attribute.String("auth.result", "oidc_ok"))
					span.End()
					parentSpan := trace.SpanFromContext(r.Context())
					parentSpan.SetAttributes(attribute.String("auth.type", "oidc"))
					rctx := WithToken(r.Context(), token)
					if oidcProject != "" {
						rctx = WithOIDCProject(rctx, oidcProject)
						rctx = WithOIDCPrivate(rctx, vr.OIDCPrivate)
						rctx = WithOIDCRepo(rctx, OIDCRepoIdentity{
							RepoPath: vr.RepoPath,
							Issuer:   vr.Issuer,
							OwnerID:  vr.OwnerID,
							RepoID:   vr.RepoID,
						})
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
		// Sign in with GitHub browser session: a verified bh_session cookie
		// (minted at the OAuth callback after the user logged in with GitHub and
		if m.GitHub != nil {
			if login, ghToken, ok := sessionFromRequest(r); ok {
				ctx := WithUser(r.Context(), login)
				ctx = WithGitHubToken(ctx, ghToken)
				r = r.WithContext(ctx)
			}
		}
		next.ServeHTTP(w, r)
	})
}

func RequireWrite(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t := TokenFrom(r.Context())
		if t == nil || !t.HasScope("write") {
			unauthorizedResponse(w, r)
			return
		}
		next(w, r)
	}
}

// userCanReadProject reports whether the request's signed-in GitHub user (if
// any) may read this private project -- i.e. they can access the project's
// GitHub repo. allowed is false if not signed in, the project has no known
// repo, or GitHub login is not configured. sessionTokenDead reports that the
func userCanReadProject(ctx context.Context, project *db.Project) (allowed, sessionTokenDead bool) {
	if mw == nil || mw.GitHub == nil || project.GithubRepo == "" {
		return false, false
	}
	login, ok := UserFrom(ctx)
	if !ok {
		return false, false
	}
	return mw.GitHub.canAccessRepo(ctx, login, GitHubTokenFrom(ctx), project.GithubRepo)
}

// UserCanReadRepo reports whether the request's signed-in GitHub user may read
// owner/repo, asking GitHub itself with the token in their session.
//
// userCanReadProject answers the same question for a buildhost project, via the
func UserCanReadRepo(ctx context.Context, ownerRepo string) bool {
	if mw == nil || mw.GitHub == nil || ownerRepo == "" {
		return false
	}
	login, ok := UserFrom(ctx)
	if !ok {
		return false
	}
	allowed, _ := mw.GitHub.canAccessRepo(ctx, login, GitHubTokenFrom(ctx), ownerRepo)
	return allowed
}

// TokenCanReadProject reports whether the request context carries a credential
// that authorizes READING the given project, applying exactly the token rules
// requireProject's ReadAccess branch applies to a private project: a token with
// the read scope, authorized for the project, and -- for OIDC identities -- inside
func TokenCanReadProject(ctx context.Context, project *db.Project) bool {
	if !project.IsPrivate {
		return true
	}
	t := TokenFrom(ctx)
	if t == nil || !t.HasScope("read") || !t.AuthorizedForProject(project.ID) {
		return false
	}
	if oidcProject := OIDCProjectFrom(ctx); oidcProject != "" && !oidcAuthorizesProject(oidcProject, project.Name) {
		return false
	}
	return true
}

// oidcAuthorizesProject reports whether an OIDC identity auto-provisioned for a
// repository may act on the given project. oidcProject is the repo's derived
func oidcAuthorizesProject(oidcProject, requested string) bool {
	if oidcProject == "" {
		return false
	}
	return requested == oidcProject || strings.HasPrefix(requested, oidcProject+"/")
}

// validNamespacedProjectName reports whether name is a well-formed project name,
func validNamespacedProjectName(name string) bool {
	if name == "" {
		return false
	}
	for _, seg := range strings.Split(name, "/") {
		if !validOIDCProjectName(seg) {
			return false
		}
	}
	return true
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
				// Auto-provisioning is a write-only action: only a write request
				// (the publish POST/PUT flow, a docker push, a site deploy) may
				// create a missing project. A read never provisions -- it just
				// 404s -- so a GET can never materialize a project as a side
				if ri.Access() != WriteAccess || t == nil || oidcProject == "" || !oidcAuthorizesProject(oidcProject, ri.ProjectName()) || !validNamespacedProjectName(ri.ProjectName()) {
					// A write request that arrived without a usable credential gets a
					if ri.Access() == WriteAccess && t == nil {
						unauthorizedResponse(w, r)
						return
					}
					projectNotFound(w)
					return
				}
				oidcPrivate, _ := OIDCPrivateFrom(r.Context())
				oidcRepo := OIDCRepoFrom(r.Context())
				// The numeric IDs are pinned from birth when the token carries them,
				// so a later re-created repo under the same name is refused below.
				project = &db.Project{
					Name:          ri.ProjectName(),
					Versioning:    db.VersioningAuto,
					IsPrivate:     oidcPrivate,
					GithubRepo:    oidcRepo.RepoPath,
					GithubOwnerID: oidcRepo.OwnerID,
					GithubRepoID:  oidcRepo.RepoID,
				}
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
			if t != nil && oidcProject != "" && oidcAuthorizesProject(oidcProject, project.Name) {
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
				if repo := OIDCRepoFrom(r.Context()); repo.RepoPath != "" {
					if repo.OwnerID != "" && repo.RepoID != "" {
						if project.GithubOwnerID != "" || project.GithubRepoID != "" {
							// Rename/resurrection guard: GitHub NAMES are reusable --
							// delete (or rename) a repo and a stranger can re-register the
							// name and mint valid OIDC tokens for the same "owner/repo" --
							// but the numeric IDs are not. A token whose IDs disagree with
							// the pin may not act on the project, read or write.
							if project.GithubOwnerID != repo.OwnerID || project.GithubRepoID != repo.RepoID {
								slog.WarnContext(r.Context(), "OIDC repo identity mismatch",
									"project", project.Name,
									"repo", repo.RepoPath,
									"pinned_owner_id", project.GithubOwnerID,
									"pinned_repo_id", project.GithubRepoID,
									"token_owner_id", repo.OwnerID,
									"token_repo_id", repo.RepoID,
									"oidc_subject", t.Name,
								)
								if ri.Access() == HiddenReadAccess {
									// Hidden reads answer every unauthorized caller with the
									projectNotFound(w)
									return
								}
								msg := fmt.Sprintf("OIDC repo identity mismatch: token for %s carries GitHub ids owner=%s repo=%s, but project %q is pinned to owner=%s repo=%s; a renamed or re-created (resurrected) repository may not take over an existing project -- if this project should belong to the new repo, an operator must clear or re-pin its recorded GitHub identity",
									repo.RepoPath, repo.OwnerID, repo.RepoID, project.Name, project.GithubOwnerID, project.GithubRepoID)
								w.Header().Set("Content-Type", "application/json")
								w.WriteHeader(http.StatusForbidden)
								body, _ := json.Marshal(map[string]string{"error": msg})
								w.Write(body)
								return
							}
						} else if ri.Access() == WriteAccess {
							if updateErr := mw.DB.SetProjectGitHubIDs(r.Context(), project.ID, repo.OwnerID, repo.RepoID); updateErr == nil {
								slog.WarnContext(r.Context(), "OIDC repo identity pinned",
									"project", project.Name,
									"repo", repo.RepoPath,
									"owner_id", repo.OwnerID,
									"repo_id", repo.RepoID,
								)
								project.GithubOwnerID = repo.OwnerID
								project.GithubRepoID = repo.RepoID
								parentSpan.SetAttributes(attribute.Bool("project.github_ids_pinned", true))
							}
						}
					}
					if project.GithubRepo != repo.RepoPath {
						if updateErr := mw.DB.SetProjectGitHubRepo(r.Context(), project.ID, repo.RepoPath); updateErr == nil {
							project.GithubRepo = repo.RepoPath
						}
					}
					if repo.Issuer == GitHubActionsIssuer {
						if branch := GitHubDefaultBranch(r.Context(), repo.RepoPath); branch != "" && branch != project.DefaultBranch {
							if updateErr := mw.DB.SetProjectDefaultBranch(r.Context(), project.ID, branch); updateErr == nil {
								slog.WarnContext(r.Context(), "OIDC default-branch sync",
									"project", project.Name,
									"repo", repo.RepoPath,
									"was", project.DefaultBranch,
									"now", branch,
								)
								project.DefaultBranch = branch
								parentSpan.SetAttributes(attribute.Bool("project.default_branch_synced", true))
							}
						}
					}
				}
			}
			// Make the resolved project available to unauthorizedResponse, so a
			r = r.WithContext(WithProject(r.Context(), project))

			switch ri.Access() {
			case WriteAccess:
				parentSpan.SetAttributes(attribute.String("project.access", "write"))
				if t == nil || !t.HasScope("write") {
					unauthorizedResponse(w, r)
					return
				}
				if !t.AuthorizedForProject(project.ID) || (oidcProject != "" && !oidcAuthorizesProject(oidcProject, project.Name)) {
					http.Error(w, `{"error":"token not authorized for this project"}`, http.StatusForbidden)
					return
				}
			case ReadAccess:
				parentSpan.SetAttributes(attribute.String("project.access", "read"))
				if project.IsPrivate {
					// A specific resource the route declares public (e.g. a
					// static site published with X-Public-Site: true) is served
					// without auth even under a private project -- the rest of
					if pra, ok := ri.(PublicReadAuthorizer); ok && pra.AllowsPublicRead(r.Context(), mw.DB, project) {
						parentSpan.SetAttributes(attribute.Bool("project.public_read", true))
						break
					}
					// A human who signed in with GitHub and has access to this
					userOK, sessionDead := userCanReadProject(r.Context(), project)
					if userOK {
						parentSpan.SetAttributes(attribute.Bool("project.user_read", true))
						break
					}
					if t == nil || !t.HasScope("read") {
						if sessionDead {
							// The browser IS signed in, but the GitHub token inside
							r = r.WithContext(WithSessionTokenDead(r.Context()))
						}
						unauthorizedResponse(w, r)
						return
					}
					if !t.AuthorizedForProject(project.ID) || (oidcProject != "" && !oidcAuthorizesProject(oidcProject, project.Name)) {
						http.Error(w, `{"error":"token not authorized for this project"}`, http.StatusForbidden)
						return
					}
				}
			case HiddenReadAccess:
				parentSpan.SetAttributes(attribute.String("project.access", "read"))
				// Same authorization as ReadAccess, but an unauthorized caller
				if project.IsPrivate {
					userOK, _ := userCanReadProject(r.Context(), project)
					authorized := userOK || (t != nil && t.HasScope("read") &&
						t.AuthorizedForProject(project.ID) &&
						(oidcProject == "" || oidcAuthorizesProject(oidcProject, project.Name)))
					if !authorized {
						projectNotFound(w)
						return
					}
				}
			}

			ctx := WithRouteInfo(WithProject(r.Context(), project), ri)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
