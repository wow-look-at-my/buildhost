// upstreamError is a failure the upstream registry caused.

package mirror

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// upstreamError is a failure the upstream registry caused.
type upstreamError struct {
	remote string
	status int
	msg    string
}

func (e *upstreamError) Error() string {
	if e.status == 0 {
		return e.remote + ": " + e.msg
	}
	return fmt.Sprintf("%s answered %d: %s", e.remote, e.status, e.msg)
}

// absent is true when the upstream says the content does not exist.
func (e *upstreamError) absent() bool {
	return e.status == http.StatusNotFound || e.status == http.StatusUnauthorized || e.status == http.StatusForbidden
}

// upstream sends one request to the upstream registry. It gets a pull token
// when the registry asks for one, and keeps it until it expires.
func (h *Handler) upstream(ctx context.Context, method, repo, rest, accept string) (*http.Response, error) {
	target := h.Up.Remote + "/v2/" + repo + "/" + rest
	resp, err := h.send(ctx, method, target, accept, h.tokens.get(repo))
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}
	challenge := resp.Header.Get("Www-Authenticate")
	resp.Body.Close()
	token, err := h.tokens.fetch(ctx, h.Client, h.Up.Remote, challenge, repo)
	if err != nil {
		return nil, err
	}
	return h.send(ctx, method, target, accept, token)
}

func (h *Handler) send(ctx context.Context, method, target, accept, token string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return nil, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := h.Client.Do(req)
	if err != nil {
		return nil, &upstreamError{remote: h.Up.Remote, msg: err.Error()}
	}
	return resp, nil
}

// It reads the start of the body, because a registry puts the reason there.
func (h *Handler) statusError(resp *http.Response, repo, rest string) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	msg := repo + "/" + rest
	if b := strings.TrimSpace(string(body)); b != "" {
		msg += ": " + b
	}
	return &upstreamError{remote: h.Up.Remote, status: resp.StatusCode, msg: msg}
}

type token struct {
	value   string
	expires time.Time
}

// tokenCache holds one anonymous pull token per repository.
type tokenCache struct {
	mu sync.Mutex
	m  map[string]token
}

func (c *tokenCache) get(repo string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.m[repo]
	if !ok || time.Now().After(t.expires) {
		return ""
	}
	return t.value
}

// fetch answers a Bearer challenge with an anonymous token request.
func (c *tokenCache) fetch(ctx context.Context, client *http.Client, remote, challenge, repo string) (string, error) {
	scheme, params := parseChallenge(challenge)
	if !strings.EqualFold(scheme, "bearer") || params["realm"] == "" {
		return "", &upstreamError{remote: remote, status: http.StatusUnauthorized, msg: "an auth challenge the mirror cannot answer: " + challenge}
	}
	q := url.Values{}
	if s := params["service"]; s != "" {
		q.Set("service", s)
	}
	scope := params["scope"]
	if scope == "" {
		scope = "repository:" + repo + ":pull"
	}
	q.Set("scope", scope)
	realm, err := url.Parse(params["realm"])
	if err != nil {
		return "", &upstreamError{remote: remote, msg: "bad token realm " + params["realm"]}
	}
	realm.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, realm.String(), nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", &upstreamError{remote: realm.Host, msg: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", &upstreamError{remote: realm.Host, status: resp.StatusCode, msg: "token for " + repo + ": " + strings.TrimSpace(string(body))}
	}
	var out struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", &upstreamError{remote: realm.Host, msg: "token response: " + err.Error()}
	}
	value := out.Token
	if value == "" {
		value = out.AccessToken
	}
	if value == "" {
		return "", &upstreamError{remote: realm.Host, msg: "the token response for " + repo + " holds no token"}
	}
	life := time.Duration(out.ExpiresIn) * time.Second
	if life < time.Minute {
		life = time.Minute
	}
	c.mu.Lock()
	if c.m == nil {
		c.m = map[string]token{}
	}
	c.m[repo] = token{value: value, expires: time.Now().Add(life - 10*time.Second)}
	c.mu.Unlock()
	return value, nil
}

// parseChallenge splits `Bearer realm="...",service="..."` into its scheme and parameters.
func parseChallenge(h string) (string, map[string]string) {
	scheme, rest, _ := strings.Cut(strings.TrimSpace(h), " ")
	params := map[string]string{}
	for rest != "" {
		rest = strings.TrimLeft(rest, " ,")
		key, after, ok := strings.Cut(rest, "=")
		if !ok {
			break
		}
		var value string
		if strings.HasPrefix(after, `"`) {
			end := strings.Index(after[1:], `"`)
			if end < 0 {
				break
			}
			value, rest = after[1:end+1], after[end+2:]
		} else {
			value, rest, _ = strings.Cut(after, ",")
		}
		params[strings.ToLower(strings.TrimSpace(key))] = value
	}
	return scheme, params
}
