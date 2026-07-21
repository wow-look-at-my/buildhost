package ociclient

// Blob upload paths for Pusher: HEAD-based existence skip and the classic
// monolithic POST for blobs under the server's advertised request-size limit
// (the resumable chunked session for everything larger lives in chunked.go),
// plus the shared HTTP plumbing (request building, redirects, error bodies).

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// pushBlob uploads one blob, skipping blobs the registry already has (HEAD),
// monolithically when it fits in a single request and through a chunked upload
// session otherwise.
func (p *Pusher) pushBlob(l *layout, d descriptor) error {
	if p.pushed[d.Digest] {
		return nil
	}
	path, err := l.blobPath(d.Digest, d.Size)
	if err != nil {
		return err
	}

	if exists, err := p.blobExists(d.Digest); err != nil {
		return err
	} else if exists {
		fmt.Fprintf(p.stdout(), "  blob %s already present, skipped\n", d.Digest)
		p.pushed[d.Digest] = true
		return nil
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}

	if st.Size() <= p.chunk {
		err = p.pushBlobMonolithic(d.Digest, f, st.Size())
	} else {
		err = p.pushBlobChunked(d.Digest, f, st.Size())
	}
	if err != nil {
		return err
	}
	p.pushed[d.Digest] = true
	return nil
}

func (p *Pusher) blobExists(digest string) (bool, error) {
	resp, err := p.do(http.MethodHead, p.baseURL()+"/v2/"+p.Project+"/blobs/"+digest, nil, 0, nil)
	if err != nil {
		return false, fmt.Errorf("check blob %s: %w", digest, err)
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK, nil
}

// pushBlobMonolithic uploads a blob in one request:
// POST /v2/{name}/blobs/uploads/?digest=... with the blob as the body.
func (p *Pusher) pushBlobMonolithic(digest string, f *os.File, size int64) error {
	resp, err := p.do(http.MethodPost, p.baseURL()+"/v2/"+p.Project+"/blobs/uploads/?digest="+url.QueryEscape(digest), f, size, nil)
	if err != nil {
		return fmt.Errorf("upload blob %s: %w", digest, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("upload blob %s failed: %s: %s", digest, resp.Status, readErrBody(resp))
	}
	fmt.Fprintf(p.stdout(), "  blob %s uploaded (%d bytes)\n", digest, size)
	return nil
}

// putManifest uploads a manifest or index under a tag or digest reference.
func (p *Pusher) putManifest(reference, mediaType string, body []byte) error {
	if mediaType == "" {
		return fmt.Errorf("manifest %s has no mediaType", reference)
	}
	header := map[string]string{"Content-Type": mediaType}
	resp, err := p.do(http.MethodPut, p.baseURL()+"/v2/"+p.Project+"/manifests/"+reference, bytes.NewReader(body), int64(len(body)), header)
	if err != nil {
		return fmt.Errorf("put manifest %s: %w", reference, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("put manifest %s failed: %s: %s", reference, resp.Status, readErrBody(resp))
	}
	return nil
}

// do issues one authenticated request. size < 0 leaves Content-Length unset.
func (p *Pusher) do(method, target string, body io.Reader, size int64, header map[string]string) (*http.Response, error) {
	req, err := http.NewRequest(method, target, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.ContentLength = size
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	if p.Token != "" {
		req.Header.Set("Authorization", "Bearer "+p.Token)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	return p.client().Do(req)
}

// absoluteURL resolves a Location header (usually path-absolute) against the
// registry base.
func (p *Pusher) absoluteURL(loc string) (string, error) {
	base, err := url.Parse(p.baseURL())
	if err != nil {
		return "", err
	}
	ref, err := url.Parse(loc)
	if err != nil {
		return "", fmt.Errorf("parse Location %q: %w", loc, err)
	}
	return base.ResolveReference(ref).String(), nil
}

// addQuery returns target with an extra query parameter, preserving any the
// server already put in the Location URL.
func addQuery(target, key, value string) (string, error) {
	u, err := url.Parse(target)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set(key, value)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// readErrBody returns a short prefix of an error response body for messages.
func readErrBody(resp *http.Response) string {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return strings.TrimSpace(string(b))
}
