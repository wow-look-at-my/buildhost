package goproxy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"golang.org/x/mod/module"
)

// upstreamSource serves modules outside the private prefixes from the public
// module mirror. Its failures are classified with the same taxonomy as the
// GitHub source, so a mirror outage is a 502 rather than a "module not found".
type upstreamSource struct {
	client *http.Client
	base   string
	// privatePrefixes is only for the "not served here" message, so a caller is
	// told what this proxy DOES cover.
	privatePrefixes []string
}

func newUpstreamSource(client *http.Client, base string, privatePrefixes []string) *upstreamSource {
	return &upstreamSource{
		client:          client,
		base:            strings.TrimSuffix(base, "/"),
		privatePrefixes: privatePrefixes,
	}
}

func (u *upstreamSource) enabled() bool { return u.base != "" }

// get fetches one module-proxy endpoint from the mirror. The caller closes the
// returned body.
func (u *upstreamSource) get(ctx context.Context, modPath, version, suffix string) (io.ReadCloser, error) {
	if !u.enabled() {
		return nil, notServedErr(modPath, version, u.privatePrefixes)
	}
	escaped, err := module.EscapePath(modPath)
	if err != nil {
		return nil, invalidErr(modPath, version, "module path cannot be escaped: "+err.Error())
	}
	url := fmt.Sprintf("%s/%s/%s", u.base, escaped, suffix)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, upstreamErr(modPath, version, u.base, 0, "building request", err)
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, upstreamErr(modPath, version, u.base, 0, "request failed", err)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp.Body, nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	detail := strings.TrimSpace(string(body))
	if len(detail) > 300 {
		detail = detail[:300] + "..."
	}

	switch resp.StatusCode {
	case http.StatusNotFound, http.StatusGone:
		// The public mirror serves only public modules and answers 404/410 for
		// anything it cannot see. That is a genuine "not found" for the module
		// space it covers -- but if the module looks like it should have been
		// served privately, say so, because the likeliest cause is a missing
		// private prefix rather than a missing module.
		return nil, notFoundErr(modPath, version, u.base, resp.StatusCode, detail)
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, unauthorizedErr(modPath, version, u.base, resp.StatusCode, detail)
	default:
		return nil, upstreamErr(modPath, version, u.base, resp.StatusCode, detail, nil)
	}
}

func (u *upstreamSource) getBytes(ctx context.Context, modPath, version, suffix string) ([]byte, error) {
	rc, err := u.get(ctx, modPath, version, suffix)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, 16<<20))
	if err != nil {
		return nil, upstreamErr(modPath, version, u.base, 0, "reading response", err)
	}
	return b, nil
}
