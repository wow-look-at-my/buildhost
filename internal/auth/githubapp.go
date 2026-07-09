package auth

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// GitHub App authentication for buildhost's own REST lookups (resolving a repo's
// default branch). Preferred over a static PAT: no token to rotate, least-
// privilege (metadata:read), and installation tokens carry a far higher rate
// limit. The flow is the standard one:
//
//	app JWT (RS256, signed with the app private key)
//	  -> GET /repos/{owner}/{repo}/installation        (installation id)
//	  -> POST /app/installations/{id}/access_tokens     (1h installation token)
//	  -> GET /repos/{owner}/{repo}                       (default_branch)
//
// Installation ids and tokens are cached, so steady state is a single repos call.

type githubApp struct {
	appID      string
	privateKey *rsa.PrivateKey

	mu        sync.Mutex
	jwtVal    string
	jwtExpiry time.Time
	instCache map[string]instEntry // owner -> installation id
	tokCache  map[int64]appTok     // installation id -> access token
}

type instEntry struct {
	id     int64
	expiry time.Time
}

type appTok struct {
	token  string
	expiry time.Time
}

var (
	ghApp   *githubApp
	ghAppMu sync.RWMutex
)

// SetGitHubApp configures GitHub App auth for default-branch lookups. appID is
// the app's numeric ID (or client ID); privateKeyPEM is its PEM private key.
// Either being empty disables App auth (the lookup falls back to
// BUILDHOST_GITHUB_TOKEN, then anonymous). Returns an error only for a malformed
// key, so a bad config surfaces at startup rather than silently degrading.
func SetGitHubApp(appID, privateKeyPEM string) error {
	if appID == "" || privateKeyPEM == "" {
		ghAppMu.Lock()
		ghApp = nil
		ghAppMu.Unlock()
		return nil
	}
	key, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(privateKeyPEM))
	if err != nil {
		return fmt.Errorf("parse GitHub App private key: %w", err)
	}
	ghAppMu.Lock()
	ghApp = &githubApp{
		appID:      appID,
		privateKey: key,
		instCache:  map[string]instEntry{},
		tokCache:   map[int64]appTok{},
	}
	ghAppMu.Unlock()
	return nil
}

func currentGitHubApp() *githubApp {
	ghAppMu.RLock()
	defer ghAppMu.RUnlock()
	return ghApp
}

// bearerForRepo returns the bearer token to authenticate a github.com REST call
// for owner/repo: a GitHub App installation token when an App is configured,
// otherwise the static PAT, otherwise "" (anonymous). Best-effort -- a failure to
// mint an App token falls through to the PAT/anonymous path.
func bearerForRepo(ctx context.Context, owner, repo string) string {
	if app := currentGitHubApp(); app != nil {
		if tok := app.installationToken(ctx, owner, repo); tok != "" {
			return tok
		}
	}
	return currentGitHubToken()
}

const appInstallTokenTTLBuffer = time.Minute

// installationToken returns a cached or freshly-minted installation access token
// for the installation covering owner/repo, or "" on any failure.
func (a *githubApp) installationToken(ctx context.Context, owner, repo string) string {
	instID := a.installationID(ctx, owner, repo)
	if instID == 0 {
		return ""
	}

	a.mu.Lock()
	if e, ok := a.tokCache[instID]; ok && time.Now().Before(e.expiry.Add(-appInstallTokenTTLBuffer)) {
		tok := e.token
		a.mu.Unlock()
		return tok
	}
	a.mu.Unlock()

	jwtStr := a.signedJWT()
	if jwtStr == "" {
		return ""
	}
	token, exp := a.createInstallationToken(ctx, jwtStr, instID)
	if token == "" {
		return ""
	}
	a.mu.Lock()
	a.tokCache[instID] = appTok{token: token, expiry: exp}
	a.mu.Unlock()
	return token
}

const appInstallationIDTTL = 24 * time.Hour

// installationID resolves (and caches by owner) the installation id covering
// owner/repo. Returns 0 if the app is not installed there or on any error.
func (a *githubApp) installationID(ctx context.Context, owner, repo string) int64 {
	now := time.Now()
	a.mu.Lock()
	if e, ok := a.instCache[owner]; ok && now.Before(e.expiry) {
		id := e.id
		a.mu.Unlock()
		return id
	}
	a.mu.Unlock()

	jwtStr := a.signedJWT()
	if jwtStr == "" {
		return 0
	}

	var body struct {
		ID int64 `json:"id"`
	}
	if !a.appGet(ctx, jwtStr, "/repos/"+owner+"/"+repo+"/installation", &body) || body.ID == 0 {
		return 0
	}
	a.mu.Lock()
	a.instCache[owner] = instEntry{id: body.ID, expiry: now.Add(appInstallationIDTTL)}
	a.mu.Unlock()
	return body.ID
}

func (a *githubApp) createInstallationToken(ctx context.Context, jwtStr string, instID int64) (string, time.Time) {
	ctx, cancel := context.WithTimeout(ctx, branchLookupBudget)
	defer cancel()

	url := gitHubAPIBase + "/app/installations/" + strconv.FormatInt(instID, 10) + "/access_tokens"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", time.Time{}
	}
	req.Header.Set("User-Agent", "buildhost")
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+jwtStr)

	resp, err := githubBranchHTTPClient.Do(req)
	if err != nil {
		return "", time.Time{}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return "", time.Time{}
	}
	var body struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil || body.Token == "" {
		return "", time.Time{}
	}
	if body.ExpiresAt.IsZero() {
		body.ExpiresAt = time.Now().Add(time.Hour)
	}
	return body.Token, body.ExpiresAt
}

// appGet performs an app-JWT-authenticated GET and decodes JSON into out.
// Returns false on any non-200 / transport / decode error.
func (a *githubApp) appGet(ctx context.Context, jwtStr, path string, out any) bool {
	ctx, cancel := context.WithTimeout(ctx, branchLookupBudget)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, gitHubAPIBase+path, nil)
	if err != nil {
		return false
	}
	req.Header.Set("User-Agent", "buildhost")
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+jwtStr)

	resp, err := githubBranchHTTPClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out) == nil
}

// signedJWT returns a cached or freshly-signed app JWT (RS256). GitHub caps app
// JWT lifetime at 10 minutes; we use 9 and backdate iat 30s for clock skew.
func (a *githubApp) signedJWT() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.jwtVal != "" && time.Now().Before(a.jwtExpiry.Add(-time.Minute)) {
		return a.jwtVal
	}
	now := time.Now()
	exp := now.Add(9 * time.Minute)
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.RegisteredClaims{
		Issuer:    a.appID,
		IssuedAt:  jwt.NewNumericDate(now.Add(-30 * time.Second)),
		ExpiresAt: jwt.NewNumericDate(exp),
	})
	signed, err := tok.SignedString(a.privateKey)
	if err != nil {
		return ""
	}
	a.jwtVal = signed
	a.jwtExpiry = exp
	return signed
}
