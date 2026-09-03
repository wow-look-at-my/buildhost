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
	RepoPath string
	// Issuer is the verified token issuer, so the caller can gate
	Issuer string
	// OwnerID / RepoID are GitHub's numeric account/repository IDs from the
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
