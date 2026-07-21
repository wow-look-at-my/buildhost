package auth

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

var ErrOIDCNotMatched = errors.New("no matching OIDC policy")

type OIDCVerifier struct {
	mu             sync.RWMutex
	cache          map[string]*cachedJWKS
	trustedIssuers []string
	allowedOrgs    []string
	allowedEvents  []string
}

type cachedJWKS struct {
	keys   []jwkKey
	expiry time.Time
}

type jwkKey struct {
	Kid string
	Pub *rsa.PublicKey
}

type oidcClaims struct {
	jwt.RegisteredClaims
	EventName            string `json:"event_name"`
	RepositoryVisibility string `json:"repository_visibility"`
	// Dedicated GitHub Actions repo-identity claims, preferred over parsing the
	// subject: they carry the plain names and the immutable numeric IDs
	// separately regardless of the subject format (classic or immutable, see
	// splitImmutableID). Empty for issuers that don't mint them; the subject
	// parsers below are the fallback.
	Repository        string `json:"repository"`          // "OWNER/REPO"
	RepositoryID      string `json:"repository_id"`       // numeric repository ID
	RepositoryOwner   string `json:"repository_owner"`    // "OWNER"
	RepositoryOwnerID string `json:"repository_owner_id"` // numeric account ID
}

const oidcLeeway = 60 * time.Second

type OIDCConfig struct {
	TrustedIssuers []string
	AllowedOrgs    []string
	AllowedEvents  []string
}

func NewOIDCVerifier(cfg OIDCConfig) *OIDCVerifier {
	return &OIDCVerifier{
		cache:          make(map[string]*cachedJWKS),
		trustedIssuers: cfg.TrustedIssuers,
		allowedOrgs:    cfg.AllowedOrgs,
		allowedEvents:  cfg.AllowedEvents,
	}
}

func LooksLikeJWT(token string) bool {
	parts := strings.Split(token, ".")
	return len(parts) == 3 && len(token) > 100
}

// VerifyResult holds the result of OIDC verification beyond the token itself.
type VerifyResult struct {
	OIDCPrivate bool
	// RepoPath is the "owner/repo" parsed from a GitHub Actions OIDC subject
	// (`repo:OWNER/REPO:...`). Used to resolve the repo's default branch from
	// GitHub AND recorded on the project so a browser "Sign in with GitHub" can
	// authorize by the user's access to that exact repo. Empty for subjects not
	// in that form.
	RepoPath string
	// Issuer is the verified token issuer, so the caller can gate
	// GitHub-specific behavior (default-branch lookup) on GitHubActionsIssuer.
	Issuer string
	// OwnerID / RepoID are GitHub's numeric account/repository IDs from the
	// verified token (the repository_owner_id / repository_id claims, falling
	// back to an immutable subject's @id suffixes). GitHub NAMES are reusable --
	// delete or rename a repo and a stranger can re-register the name, minting
	// valid tokens that carry the same "owner/repo" -- but the numeric IDs are
	// not. The middleware pins them on the project and rejects OIDC requests
	// whose IDs disagree, so a re-created ("resurrected") repo cannot take over
	// an existing project. Empty when the issuer provides no IDs.
	OwnerID string
	RepoID  string
}

func (v *OIDCVerifier) VerifyToken(ctx context.Context, raw string, policies []db.OIDCPolicy) (*db.APIToken, string, error) {
	return v.verifyTokenFull(ctx, raw, policies, nil)
}

func (v *OIDCVerifier) VerifyTokenFull(ctx context.Context, raw string, policies []db.OIDCPolicy, result *VerifyResult) (*db.APIToken, string, error) {
	return v.verifyTokenFull(ctx, raw, policies, result)
}

func (v *OIDCVerifier) verifyTokenFull(ctx context.Context, raw string, policies []db.OIDCPolicy, result *VerifyResult) (*db.APIToken, string, error) {
	unverified := &oidcClaims{}
	_, _, err := jwt.NewParser().ParseUnverified(raw, unverified)
	if err != nil {
		return nil, "", fmt.Errorf("parse token: %w", err)
	}

	if unverified.ExpiresAt == nil {
		return nil, "", errors.New("token missing exp claim")
	}
	now := time.Now()
	if now.After(unverified.ExpiresAt.Time.Add(oidcLeeway)) {
		return nil, "", errors.New("token expired")
	}
	if unverified.NotBefore != nil && now.Before(unverified.NotBefore.Time.Add(-oidcLeeway)) {
		return nil, "", errors.New("token not yet valid")
	}

	var matchedPolicy *db.OIDCPolicy
	for i := range policies {
		p := &policies[i]
		if p.Issuer != unverified.Issuer {
			continue
		}
		if matchSubject(p.SubjectPattern, unverified.Subject) {
			matchedPolicy = p
			break
		}
	}
	if matchedPolicy != nil && matchedPolicy.Audience != "" {
		aud, _ := unverified.GetAudience()
		if !slices.Contains(aud, matchedPolicy.Audience) {
			return nil, "", errors.New("token audience does not match policy")
		}
	}

	if matchedPolicy == nil && !slices.Contains(v.trustedIssuers, unverified.Issuer) {
		return nil, "", ErrOIDCNotMatched
	}

	keys, err := v.getKeys(ctx, unverified.Issuer)
	if err != nil {
		return nil, "", fmt.Errorf("fetch JWKS: %w", err)
	}

	token, err := jwt.ParseWithClaims(raw, &oidcClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unsupported algorithm: %v", t.Header["alg"])
		}
		kid, _ := t.Header["kid"].(string)
		for _, key := range keys {
			if kid == "" || key.Kid == kid {
				return key.Pub, nil
			}
		}
		return nil, errors.New("no matching key found")
	}, jwt.WithLeeway(oidcLeeway), jwt.WithExpirationRequired())
	if err != nil {
		return nil, "", fmt.Errorf("verify token: %w", err)
	}

	verified := token.Claims.(*oidcClaims)
	ownerID, repoID := verified.repoIDs()

	// Surface the repo identity and issuer for both verification paths, so the
	// caller can resolve the repo's default branch from GitHub, record the repo
	// on the project, and pin/verify the numeric repo IDs, without anything
	// being sent in the publish request.
	if result != nil {
		result.Issuer = verified.Issuer
		result.RepoPath = verified.repoPath()
		result.OwnerID, result.RepoID = ownerID, repoID
	}

	if matchedPolicy != nil {
		return &db.APIToken{
			ID:        -1,
			Name:      "oidc:" + verified.Subject,
			ProjectID: matchedPolicy.ProjectID,
			Scopes:    matchedPolicy.Scopes,
		}, "", nil
	}

	org := verified.ownerName()
	if !orgAllowed(v.allowedOrgs, org, ownerID) {
		if ownerID != "" {
			return nil, "", fmt.Errorf("org %q (owner id %s) not in allowed list", org, ownerID)
		}
		return nil, "", fmt.Errorf("org %q not in allowed list", org)
	}

	if !slices.Contains(v.allowedEvents, "*") && !slices.Contains(v.allowedEvents, verified.EventName) {
		return nil, "", fmt.Errorf("event %q not in allowed list", verified.EventName)
	}

	// No audience gate here: auto-provisioning trusts the issuer signature, the
	// org allowlist, the event allowlist and the subject. Binding to a specific
	// audience would require the server to know its own URL, which is a config
	// footgun -- a wrong or missing value silently rejects every publish -- for
	// little gain on a single-tenant build host. Policy-scoped tokens can still
	// opt into an explicit audience via OIDCPolicy.Audience above.

	project := verified.projectName()
	if project == "" {
		return nil, "", errors.New("cannot derive project name from OIDC subject")
	}
	if result != nil {
		result.OIDCPrivate = verified.RepositoryVisibility != "public"
	}
	return &db.APIToken{
		ID:     -1,
		Name:   "oidc:" + verified.Subject,
		Scopes: "read,write",
	}, project, nil
}

// splitImmutableID splits one segment of an OIDC subject repo path into its
// name and optional "@<numeric-id>" suffix. GitHub repos created after
// 2026-07-15 mint "immutable" subject claims that pin each segment to its
// account/repo ID -- `repo:OWNER@OWNERID/REPO@REPOID:ref:...` -- while classic
// repos keep the bare `repo:OWNER/REPO:ref:...` form (see
// https://github.blog/changelog/2026-04-23-immutable-subject-claims-for-github-actions-oidc-tokens/).
// Org allowlisting, project derivation, and GitHub REST lookups want the NAME;
// the ID is what makes the subject rename/resurrection-proof, so it is
// returned separately for pinning. Only a suffix after the LAST "@" that is
// non-empty and all digits is split off (GitHub names cannot contain "@", but
// be conservative); anything else -- including every classic segment -- passes
// through byte-for-byte with an empty id. The raw subject (IDs included) still
// ends up in the token name ("oidc:<sub>"), so the IDs stay visible in logs
// and token listings. The same "name@id" grammar is accepted in
// BUILDHOST_OIDC_ORGS entries (see orgAllowed).
func splitImmutableID(segment string) (name, id string) {
	at := strings.LastIndexByte(segment, '@')
	if at <= 0 || at == len(segment)-1 {
		return segment, ""
	}
	for _, c := range segment[at+1:] {
		if c < '0' || c > '9' {
			return segment, ""
		}
	}
	return segment[:at], segment[at+1:]
}

// trimImmutableID returns just the name half of splitImmutableID.
func trimImmutableID(segment string) string {
	name, _ := splitImmutableID(segment)
	return name
}

// validNumericID reports whether s is a plausible GitHub numeric ID: all
// digits, bounded. Gates the ID claims before they are pinned to a project or
// echoed into errors, mirroring how validRepoPath gates the repo path.
func validNumericID(s string) bool {
	if s == "" || len(s) > 20 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// ownerName returns the org/user owning the token's repo, preferring the
// dedicated repository_owner claim over parsing the subject.
func (c *oidcClaims) ownerName() string {
	if c.RepositoryOwner != "" {
		return trimImmutableID(c.RepositoryOwner)
	}
	return orgFromSubject(c.Subject)
}

// projectName derives the auto-provisioned project name (the repo name,
// lowercased and validated), preferring the dedicated repository claim over
// parsing the subject.
func (c *oidcClaims) projectName() string {
	if c.Repository != "" {
		if _, repo, ok := strings.Cut(c.Repository, "/"); ok {
			name := strings.ToLower(trimImmutableID(repo))
			if validOIDCProjectName(name) {
				return name
			}
		}
	}
	return projectFromSubject(c.Subject)
}

// repoPath returns the token's "owner/repo" (plain names, original casing --
// it feeds GitHub REST lookups), preferring the dedicated repository claim
// over parsing the subject.
func (c *oidcClaims) repoPath() string {
	if c.Repository != "" {
		if path := trimRepoPathIDs(c.Repository); validRepoPath(path) {
			return path
		}
	}
	return repoPathFromSubject(c.Subject)
}

// repoIDs returns the numeric owner/repo IDs, preferring the dedicated
// repository_owner_id / repository_id claims and falling back to an immutable
// subject's @id suffixes. Either may be empty (classic-era issuers mint the
// claims too, but non-GitHub issuers may mint neither).
func (c *oidcClaims) repoIDs() (ownerID, repoID string) {
	if validNumericID(c.RepositoryOwnerID) {
		ownerID = c.RepositoryOwnerID
	}
	if validNumericID(c.RepositoryID) {
		repoID = c.RepositoryID
	}
	if ownerID != "" && repoID != "" {
		return ownerID, repoID
	}
	subOwnerID, subRepoID := idsFromSubject(c.Subject)
	if ownerID == "" {
		ownerID = subOwnerID
	}
	if repoID == "" {
		repoID = subRepoID
	}
	return ownerID, repoID
}

// idsFromSubject extracts the numeric IDs from an immutable GitHub Actions
// OIDC subject (`repo:OWNER@OWNERID/REPO@REPOID:...`). Both are empty for
// classic subjects and non-repo subjects.
func idsFromSubject(subject string) (ownerID, repoID string) {
	if !strings.HasPrefix(subject, "repo:") {
		return "", ""
	}
	rest := subject[len("repo:"):]
	colon := strings.Index(rest, ":")
	if colon < 0 {
		return "", ""
	}
	repoPath := rest[:colon]
	slash := strings.Index(repoPath, "/")
	if slash < 0 {
		return "", ""
	}
	_, ownerID = splitImmutableID(repoPath[:slash])
	_, repoID = splitImmutableID(repoPath[strings.LastIndex(repoPath, "/")+1:])
	return ownerID, repoID
}

// trimRepoPathIDs strips the immutable @id suffix from every "/"-separated
// segment of an "owner/repo" path.
func trimRepoPathIDs(path string) string {
	segments := strings.Split(path, "/")
	for i, s := range segments {
		segments[i] = trimImmutableID(s)
	}
	return strings.Join(segments, "/")
}

// orgAllowed reports whether the token's org may auto-provision. "*" allows
// all. GitHub org/user logins are case-insensitive (github.com treats
// "PazerOP" and "pazerop" as the same account) and the token preserves the
// canonical casing the org was created with, so names compare
// case-insensitively -- otherwise auto-provisioning silently fails on a pure
// casing mismatch. An entry may additionally pin the org's numeric account ID
// as "name@id" (the immutable-subject grammar): it then also requires the
// token to carry exactly that owner ID -- refusing ID-less tokens -- so the
// allowlist entry survives its org being deleted and the name re-registered
// by a stranger. A plain name entry matches any ID; the per-project pin
// (projects.github_owner_id) is what guards existing projects.
func orgAllowed(allowed []string, org, ownerID string) bool {
	if slices.Contains(allowed, "*") {
		return true
	}
	for _, entry := range allowed {
		name, id := splitImmutableID(entry)
		if !strings.EqualFold(name, org) {
			continue
		}
		if id == "" || id == ownerID {
			return true
		}
	}
	return false
}

func projectFromSubject(subject string) string {
	if !strings.HasPrefix(subject, "repo:") {
		return ""
	}
	rest := subject[len("repo:"):]
	colon := strings.Index(rest, ":")
	if colon < 0 {
		return ""
	}
	repoPath := rest[:colon]
	slash := strings.LastIndex(repoPath, "/")
	if slash < 0 {
		return ""
	}
	name := strings.ToLower(trimImmutableID(repoPath[slash+1:]))
	if !validOIDCProjectName(name) {
		return ""
	}
	return name
}

func validOIDCProjectName(name string) bool {
	if len(name) == 0 || len(name) > 128 {
		return false
	}
	for i, c := range name {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			continue
		}
		if i > 0 && (c == '.' || c == '_' || c == '-') {
			continue
		}
		return false
	}
	return true
}

// repoPathFromSubject extracts "owner/repo" from a GitHub Actions OIDC subject
// of the form "repo:OWNER/REPO:...". Returns "" if the subject is not in that
// form. Unlike projectFromSubject it preserves the owner and original casing,
// since it feeds a GitHub REST lookup (github.com/OWNER/REPO). Immutable
// numeric IDs ("OWNER@ID/REPO@ID", see trimImmutableID) are stripped so the
// result stays a valid REST path -- validRepoPath rejects "@", and github.com
// wants the names.
func repoPathFromSubject(subject string) string {
	if !strings.HasPrefix(subject, "repo:") {
		return ""
	}
	rest := subject[len("repo:"):]
	colon := strings.Index(rest, ":")
	if colon < 0 {
		return ""
	}
	return trimRepoPathIDs(rest[:colon])
}

func orgFromSubject(subject string) string {
	if !strings.HasPrefix(subject, "repo:") {
		return ""
	}
	rest := subject[len("repo:"):]
	colon := strings.Index(rest, ":")
	if colon < 0 {
		return ""
	}
	repoPath := rest[:colon]
	slash := strings.Index(repoPath, "/")
	if slash < 0 {
		return ""
	}
	return trimImmutableID(repoPath[:slash])
}

func matchSubject(pattern, subject string) bool {
	if pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, ":*") {
		prefix := pattern[:len(pattern)-1]
		return strings.HasPrefix(subject, prefix)
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := pattern[:len(pattern)-1]
		return strings.HasPrefix(subject, prefix)
	}
	return pattern == subject
}
