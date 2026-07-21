package ociclient

// Blob upload paths for Pusher: HEAD-based existence skip, the classic
// monolithic POST for blobs under the server's advertised request-size limit,
// and the resumable chunked upload session for everything larger -- plus the
// shared HTTP plumbing (request building, redirects, error bodies).

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
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

// pushBlobChunked uploads a blob through an OCI upload session: POST opens the
// session, sequential PATCH appends carry chunks no bigger than the resolved
// limit, and a final empty PUT with ?digest= verifies and stores. Every
// iteration trusts the server's committed size (from the append's Range
// header, a 416's Range header, or the status endpoint after a transport
// error), so a partially delivered chunk resumes instead of restarting.
func (p *Pusher) pushBlobChunked(digest string, f *os.File, size int64) error {
	loc, err := p.startSession()
	if err != nil {
		return fmt.Errorf("upload blob %s: %w", digest, err)
	}

	totalChunks := (size + p.chunk - 1) / p.chunk
	fmt.Fprintf(p.stdout(), "  blob %s: %d MiB in %d chunks of <=%d MiB\n", digest, size>>20, totalChunks, p.chunk>>20)

	offset := int64(0)
	failures := 0
	for offset < size {
		n := min(p.chunk, size-offset)
		committed, err := p.patchChunk(loc, io.NewSectionReader(f, offset, n), offset, n)
		if err != nil {
			return fmt.Errorf("upload blob %s at offset %d: %w", digest, offset, err)
		}
		if committed <= offset {
			failures++
			if failures >= retryAttempts {
				return fmt.Errorf("upload blob %s made no progress at offset %d", digest, offset)
			}
			time.Sleep(RetryBaseDelay << (failures - 1))
		} else {
			failures = 0
			fmt.Fprintf(p.stdout(), "    chunk %d/%d uploaded (%d/%d bytes)\n", (committed+p.chunk-1)/p.chunk, totalChunks, committed, size)
		}
		offset = committed
	}

	putURL, err := addQuery(loc, "digest", digest)
	if err != nil {
		return err
	}
	resp, err := p.do(http.MethodPut, putURL, nil, 0, nil)
	if err != nil {
		return fmt.Errorf("finalize blob %s: %w", digest, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("finalize blob %s failed: %s: %s", digest, resp.Status, readErrBody(resp))
	}
	return nil
}

// startSession opens a blob upload session and returns its absolute URL.
func (p *Pusher) startSession() (string, error) {
	resp, err := p.do(http.MethodPost, p.baseURL()+"/v2/"+p.Project+"/blobs/uploads/", nil, 0, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf("start upload session: %s: %s", resp.Status, readErrBody(resp))
	}
	loc := resp.Header.Get("Location")
	if loc == "" {
		return "", fmt.Errorf("start upload session: no Location header")
	}
	return p.absoluteURL(loc)
}

// patchChunk sends one chunk and returns the server's committed size. A 416
// (offset mismatch after a lost response) resolves from its Range header; a
// transport error or 5xx resolves through the status endpoint. Anything else
// unexpected is a hard error.
func (p *Pusher) patchChunk(loc string, chunk io.Reader, offset, n int64) (int64, error) {
	header := map[string]string{"Content-Range": fmt.Sprintf("%d-%d", offset, offset+n-1)}
	resp, err := p.do(http.MethodPatch, loc, chunk, n, header)
	if err != nil {
		// The chunk may have partially landed before the connection broke; ask
		// the server how much it has and resume from there.
		return p.sessionStatus(loc)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusAccepted, resp.StatusCode == http.StatusRequestedRangeNotSatisfiable:
		committed, ok := committedFromRange(resp.Header.Get("Range"))
		if !ok {
			return 0, fmt.Errorf("append chunk: response carried no usable Range header")
		}
		return committed, nil
	case resp.StatusCode >= 500:
		return p.sessionStatus(loc)
	default:
		return 0, fmt.Errorf("append chunk failed: %s: %s", resp.Status, readErrBody(resp))
	}
}

// sessionStatus reads the session's committed size for resuming.
func (p *Pusher) sessionStatus(loc string) (int64, error) {
	var lastErr error
	for attempt := range retryAttempts {
		if attempt > 0 {
			time.Sleep(RetryBaseDelay << (attempt - 1))
		}
		resp, err := p.do(http.MethodGet, loc, nil, 0, nil)
		if err != nil {
			lastErr = err
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
			if committed, ok := committedFromRange(resp.Header.Get("Range")); ok {
				return committed, nil
			}
		}
		lastErr = fmt.Errorf("read upload status: %s", resp.Status)
	}
	return 0, fmt.Errorf("read upload status: %w", lastErr)
}

// committedFromRange converts an inclusive "0-<end>" Range header to the
// committed byte count. "0-0" is ambiguous between zero and one byte; zero is
// assumed (re-sending one byte is harmless, and real chunks are megabytes).
func committedFromRange(r string) (int64, bool) {
	m := rangePattern.FindStringSubmatch(r)
	if m == nil || m[1] != "0" {
		return 0, false
	}
	end, err := strconv.ParseInt(m[2], 10, 64)
	if err != nil {
		return 0, false
	}
	if end == 0 {
		return 0, true
	}
	return end + 1, true
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
