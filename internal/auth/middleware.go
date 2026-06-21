package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
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
					// Remember why, so an eventual 401 can explain it rather than
					// returning a bare "authentication required".
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
						rctx = WithOIDCRepo(rctx, vr.RepoPath, vr.Issuer)
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
		// passed the org allowlist) marks the request as an authenticated human,
		// which requireProject treats as read authorization for private resources.
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

// projectNotFound writes the canonical 404 for a project that does not exist or
// that the caller may not see. Both cases share this exact response so a hidden
// (HiddenReadAccess) read cannot be used to probe for the existence of private
// projects.
func projectNotFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	w.Write([]byte(`{"error":"project not found"}`))
}

func unauthorizedResponse(w http.ResponseWriter, r *http.Request) {
	msg := "authentication required"
	if err := OIDCErrorFrom(r.Context()); err != nil {
		// A JWT was presented and rejected -- say why (audience, org allowlist,
		// event, expiry, signature, ...) instead of a bare message, so a CI
		// caller can see what to fix.
		msg += ": OIDC token rejected: " + err.Error()
	}

	if strings.HasPrefix(r.URL.Path, "/v2/") {
		// OCI clients (docker pull/push) require the registry error envelope and
		// a Basic challenge on /v2/ so they retry with credentials.
		w.Header().Set("Www-Authenticate", `Basic realm="buildhost"`)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		body, _ := json.Marshal(map[string]any{
			"errors": []map[string]string{{"code": "UNAUTHORIZED", "message": msg}},
		})
		w.Write(body)
		return
	}

	// Browser handling when "Sign in with GitHub" is configured. Programmatic
	// clients (no text/html) -- and deployments without GitHub login configured --
	// fall through to the plain JSON 401 below, unchanged.
	if prefersHTML(r) && githubAuthEnabled() {
		login, signedIn := UserFrom(r.Context())
		switch {
		case !signedIn && TokenFrom(r.Context()) == nil:
			// Anonymous browser: send them to GitHub to sign in, returning to the
			// resource afterward.
			http.Redirect(w, r, loginRedirectURL(r), http.StatusSeeOther)
			return
		case signedIn:
			// Signed in, but not authorized for this resource (their GitHub account
			// can't read the backing repo, or the project has no repo recorded).
			// Re-redirecting to /__signin would loop -- GitHub re-auths the same
			// account and bounces straight back -- so render an actionable page
			// (who you are, what access is needed, sign out to switch accounts)
			// instead of the dead-end JSON 401 a browser cannot act on.
			signedInForbiddenHTML(w, r, login, ProjectFrom(r.Context()))
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	body, _ := json.Marshal(map[string]string{"error": msg})
	w.Write(body)
}

// signedInForbiddenHTML renders an actionable page for a browser that IS signed
// in with GitHub but is not authorized to read this resource. The anonymous case
// redirects to /__signin; this one must NOT -- the user already holds a valid
// session, so a redirect would bounce straight back here (GitHub re-auths the
// same account), an infinite loop. So we explain the situation and offer a
// sign-out, letting them switch to an account that has access. It returns 403
// (authenticated but not permitted), not 401.
func signedInForbiddenHTML(w http.ResponseWriter, r *http.Request, login string, project *db.Project) {
	esc := template.HTMLEscapeString
	var detail string
	if project != nil && project.GithubRepo != "" {
		detail = "Your GitHub account <strong>" + esc(login) + "</strong> doesn't have access to <strong>" +
			esc(project.GithubRepo) + "</strong>, the repository behind this resource. " +
			"Switch to an account that can, or ask the owner for access."
	} else {
		detail = "You're signed in as <strong>" + esc(login) + "</strong>, but this resource isn't shared " +
			"through GitHub sign-in. Ask the owner for a project access token or a temporary download link."
	}

	// Relax the global default-src 'none' just enough for the one inline <style>;
	// no scripts, no external resources (same approach as the web frontend).
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	fmt.Fprintf(w, `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Access denied</title>
<style>
  body { font-family: system-ui, -apple-system, sans-serif; max-width: 34rem; margin: 12vh auto; padding: 0 1.25rem; line-height: 1.55; }
  h1 { font-size: 1.4rem; margin-bottom: .5rem; }
  a.btn { display: inline-block; margin-top: 1rem; padding: .55rem .9rem; border: 1px solid; border-radius: .4rem; text-decoration: none; }
  .hint { margin-top: 1.25rem; font-size: .85rem; opacity: .8; }
</style>
</head>
<body>
<h1>Access denied</h1>
<p>%s</p>
<div><a class="btn" href="%s">Sign out &amp; switch account</a></div>
<p class="hint">To use a different account you may also need to <a href="https://github.com/logout">sign out of GitHub</a> first.</p>
</body>
</html>
`, detail, esc(signoutURL(r)))
}

// prefersHTML reports whether the request came from a browser navigation (its
// Accept header lists text/html). Used to decide whether an auth failure should
// drive the Cloudflare Access sign-in redirect versus the raw JSON that
// programmatic clients expect.
func prefersHTML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// userCanReadProject reports whether the request's signed-in GitHub user (if
// any) may read this private project -- i.e. they can access the project's
// GitHub repo. False if not signed in, the project has no known repo, or GitHub
// login is not configured.
func userCanReadProject(ctx context.Context, project *db.Project) bool {
	if mw == nil || mw.GitHub == nil || project.GithubRepo == "" {
		return false
	}
	login, ok := UserFrom(ctx)
	if !ok {
		return false
	}
	return mw.GitHub.canAccessRepo(ctx, login, GitHubTokenFrom(ctx), project.GithubRepo)
}

// oidcAuthorizesProject reports whether an OIDC identity auto-provisioned for a
// repository may act on the given project. oidcProject is the repo's derived
// single-segment name (see projectFromSubject). A repo owns its own project and
// the entire slash-namespace beneath it: repo "log-streamer" authorizes
// "log-streamer" and "log-streamer/client" (any depth), but never an unrelated
// project like "log-streamer-evil" or "other/thing". The trailing "/" boundary
// is what prevents a sibling-prefix from leaking access.
func oidcAuthorizesProject(oidcProject, requested string) bool {
	if oidcProject == "" {
		return false
	}
	return requested == oidcProject || strings.HasPrefix(requested, oidcProject+"/")
}

// validNamespacedProjectName reports whether name is a well-formed project name,
// allowing one or more "/"-separated segments that each satisfy the
// single-segment rules. It gates OIDC auto-provisioning so a repo cannot create
// a malformed project (bad characters, empty/leading/trailing/double slash)
// under its namespace.
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
				// effect. (A hidden read uses this same 404 for private projects
				// it may not see, so existence never leaks either.)
				if ri.Access() != WriteAccess || t == nil || oidcProject == "" || !oidcAuthorizesProject(oidcProject, ri.ProjectName()) || !validNamespacedProjectName(ri.ProjectName()) {
					// A write request that arrived without a usable credential gets a
					// 401 (carrying the OCI Basic challenge on /v2/), never a bare 404.
					// Two cases reach here with t == nil:
					//   * a JWT was presented and rejected (bad org, event, expiry,
					//     signature, ...): OIDCErrorFrom is set and unauthorizedResponse
					//     surfaces the reason, so a CI caller sees what to fix; and
					//   * no credential was sent at all -- which is exactly the first,
					//     scheme-discovery request a docker/buildkit pusher makes when
					//     creating a new repo. It sends POST /v2/{name}/blobs/uploads/
					//     unauthenticated and only sends the OIDC token after a 401 +
					//     WWW-Authenticate challenge. Returning a "project not found" 404
					//     here made the client give up before authenticating, so a
					//     brand-new project could never be auto-provisioned on first push
					//     (push-to-create was broken; pushing to an already-existing
					//     project worked via the WriteAccess switch below). The
					//     authenticated retry lands back here with a token and provisions.
					// Reads keep the 404 so a private project's existence never leaks --
					// this branch is WriteAccess only.
					if ri.Access() == WriteAccess && t == nil {
						unauthorizedResponse(w, r)
						return
					}
					projectNotFound(w)
					return
				}
				oidcPrivate, _ := OIDCPrivateFrom(r.Context())
				oidcRepoPath, _ := OIDCRepoFrom(r.Context())
				project = &db.Project{Name: ri.ProjectName(), Versioning: db.VersioningAuto, IsPrivate: oidcPrivate, GithubRepo: oidcRepoPath}
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
				// The OIDC publish subject carries the repo identity; use it for two
				// things, both derived from the token (nothing sent in the request):
				// (1) record owner/repo on the project so GitHub-login authz can check
				// the user's access to it; (2) resolve the repo's default branch from
				// GitHub (best-effort, cached, GitHub Actions issuer only) so the apex
				// "latest" tracks it.
				if repoPath, issuer := OIDCRepoFrom(r.Context()); repoPath != "" {
					if project.GithubRepo != repoPath {
						if updateErr := mw.DB.SetProjectGitHubRepo(r.Context(), project.ID, repoPath); updateErr == nil {
							project.GithubRepo = repoPath
						}
					}
					if issuer == GitHubActionsIssuer {
						if branch := GitHubDefaultBranch(r.Context(), repoPath); branch != "" && branch != project.DefaultBranch {
							if updateErr := mw.DB.SetProjectDefaultBranch(r.Context(), project.ID, branch); updateErr == nil {
								slog.WarnContext(r.Context(), "OIDC default-branch sync",
									"project", project.Name,
									"repo", repoPath,
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
			// signed-in-but-forbidden browser gets an actionable page naming the
			// repo it needs (the final next.ServeHTTP re-sets it harmlessly below).
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
					// the project (release artifacts, other branches) stays gated.
					if pra, ok := ri.(PublicReadAuthorizer); ok && pra.AllowsPublicRead(r.Context(), mw.DB, project) {
						parentSpan.SetAttributes(attribute.Bool("project.public_read", true))
						break
					}
					// A human who signed in with GitHub and has access to this
					// project's repo may read it. This is what the browser sign-in
					// redirect leads to.
					if userCanReadProject(r.Context(), project) {
						parentSpan.SetAttributes(attribute.Bool("project.user_read", true))
						break
					}
					if t == nil || !t.HasScope("read") {
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
				// gets a 404 (not 401/403) so a private project never reveals it
				// exists -- indistinguishable from a project that does not exist.
				if project.IsPrivate {
					authorized := userCanReadProject(r.Context(), project) || (t != nil && t.HasScope("read") &&
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
