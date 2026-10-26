// Package secretserver fetches buildhost's GitHub credential from secret-server
// with a machine token. Fetches it again on a fixed cadence so a rotated secret
// takes effect without a restart.
package secretserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/auth"
)

// URL is the route secret-server vends secrets on.
const URL = "https://secrets.pazer.io/github/v1/secrets"

// MachineTokenPrefix marks a secret-server machine token.
const MachineTokenPrefix = "sst_"

// The secret names. They are the names of the environment variables they stand in for.
const (
	SecretGitHubToken     = "BUILDHOST_GITHUB_TOKEN"
	SecretGitHubAppID     = "BUILDHOST_GITHUB_APP_ID"
	SecretGitHubAppKeyPEM = "BUILDHOST_GITHUB_APP_PRIVATE_KEY"
)

const (
	// RefreshInterval is how often a working fetch repeats.
	RefreshInterval = 15 * time.Minute
	// RetryInterval is how soon a failed fetch repeats.
	RetryInterval = time.Minute
)

var httpClient = &http.Client{Timeout: 20 * time.Second}

// Status is what the last fetch did. It never holds a secret value.
type Status struct {
	Configured bool      `json:"configured"`
	At         time.Time `json:"at,omitzero"`
	OK         bool      `json:"ok"`
	Error      string    `json:"error,omitempty"`
	Applied    string    `json:"applied,omitempty"` // "app", "token" or ""
}

var (
	statusMu sync.Mutex
	status   Status
)

// LastStatus returns the most recent fetch.
func LastStatus() Status {
	statusMu.Lock()
	defer statusMu.Unlock()
	return status
}

func setStatus(s Status) {
	statusMu.Lock()
	status = s
	statusMu.Unlock()
}

// Fetch returns every secret the machine token is entitled to.
func Fetch(ctx context.Context, url, token string) (map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "buildhost")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("secret-server answered %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var secrets map[string]string
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&secrets); err != nil {
		return nil, fmt.Errorf("decode secret-server answer: %w", err)
	}
	return secrets, nil
}

// Source fetches the GitHub credential and puts it in use.
type Source struct {
	URL   string
	Token string

	appID, appKey string
}

// Start fetches once, then keeps fetching until ctx is done.
func (s *Source) Start(ctx context.Context) error {
	if !strings.HasPrefix(s.Token, MachineTokenPrefix) {
		return fmt.Errorf("BUILDHOST_SECRET_SERVER_TOKEN is not a secret-server machine token (it must start with %q)", MachineTokenPrefix)
	}
	next := s.refresh(ctx)
	go func() {
		t := time.NewTimer(next)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				t.Reset(s.refresh(ctx))
			}
		}
	}()
	return nil
}

// refresh fetches once, applies what it got, and returns when to fetch again.
// A failure keeps the credential in use.
func (s *Source) refresh(ctx context.Context) time.Duration {
	st := Status{Configured: true, At: time.Now()}
	applied, err := s.apply(ctx)
	if err != nil {
		st.Error = err.Error()
		setStatus(st)
		slog.Error("secret-server: cannot get the GitHub credential; the one in use stays", "err", err, "retry_in", RetryInterval)
		return RetryInterval
	}
	st.OK, st.Applied = true, applied
	setStatus(st)
	return RefreshInterval
}

func (s *Source) apply(ctx context.Context) (string, error) {
	secrets, err := Fetch(ctx, s.URL, s.Token)
	if err != nil {
		return "", err
	}
	token := secrets[SecretGitHubToken]
	appID, appKey := secrets[SecretGitHubAppID], secrets[SecretGitHubAppKeyPEM]
	if token == "" && (appID == "" || appKey == "") {
		return "", errors.New("the machine token is entitled to neither " + SecretGitHubToken + " nor both of " + SecretGitHubAppID + " and " + SecretGitHubAppKeyPEM)
	}
	auth.SetGitHubToken(token)
	if appID != s.appID || appKey != s.appKey {
		if err := auth.SetGitHubApp(appID, strings.ReplaceAll(appKey, `\n`, "\n")); err != nil {
			return "", err
		}
		s.appID, s.appKey = appID, appKey
	}
	if appID != "" && appKey != "" {
		return "app", nil
	}
	return "token", nil
}
