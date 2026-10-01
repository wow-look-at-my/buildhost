// Chunked OCI upload sessions: blobs larger than the server's advertised
// direct-upload limit go through POST session + sequential PATCH appends +
// digest-checked PUT, resuming from the server's committed size. Split from
// upload.go, which holds the existence skip, the mount, and the single-request
// finalize.
package ociclient

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"time"
)

// rangePattern matches the inclusive "start-end" byte range the registry
var rangePattern = regexp.MustCompile(`^([0-9]+)-([0-9]+)$`)

// errSessionGone reports that the registry no longer knows the upload session
var errSessionGone = errors.New("upload session no longer exists")

// pushBlobChunked uploads a blob through the given OCI upload session, opening
// a replacement and starting the blob over if the registry forgets it -- a
// publish minutes deep must not die because the far end restarted.
func (p *Pusher) pushBlobChunked(loc, digest string, f *os.File, size int64) error {
	for attempt := range retryAttempts {
		if attempt > 0 {
			time.Sleep(RetryBaseDelay << (attempt - 1))
			var err error
			if _, loc, err = p.startSession(""); err != nil {
				return fmt.Errorf("upload blob %s: %w", digest, err)
			}
			fmt.Fprintf(p.stdout(), "  blob %s: upload session was dropped, restarting it\n", digest)
		}
		err := p.uploadChunks(loc, digest, f, size)
		if errors.Is(err, errSessionGone) {
			continue
		}
		return err
	}
	return fmt.Errorf("upload blob %s: the registry dropped the upload session %d times", digest, retryAttempts)
}

func (p *Pusher) uploadChunks(loc, digest string, f *os.File, size int64) error {
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
	if resp.StatusCode == http.StatusNotFound {
		return errSessionGone
	}
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("finalize blob %s failed: %s: %s", digest, resp.Status, readErrBody(resp))
	}
	return nil
}

func (p *Pusher) startSession(mount string) (bool, string, error) {
	target := p.baseURL() + "/v2/" + p.Project + "/blobs/uploads/"
	if mount != "" {
		target += "?mount=" + url.QueryEscape(mount)
	}
	var lastErr error
	for attempt := range retryAttempts {
		if attempt > 0 {
			time.Sleep(RetryBaseDelay << (attempt - 1))
		}
		resp, err := p.do(http.MethodPost, target, nil, 0, nil)
		if err != nil {
			lastErr = err
			continue
		}
		status, loc := resp.StatusCode, resp.Header.Get("Location")
		body := readErrBody(resp)
		resp.Body.Close()

		switch {
		case status == http.StatusCreated:
			return true, "", nil
		case status == http.StatusAccepted:
			if loc == "" {
				return false, "", fmt.Errorf("start upload session: no Location header")
			}
			abs, err := p.absoluteURL(loc)
			return false, abs, err
		case status >= 500:
			lastErr = fmt.Errorf("start upload session: %s: %s", resp.Status, body)
		default:
			return false, "", fmt.Errorf("start upload session: %s: %s", resp.Status, body)
		}
	}
	return false, "", fmt.Errorf("start upload session: %w", lastErr)
}

func (p *Pusher) patchChunk(loc string, chunk io.Reader, offset, n int64) (int64, error) {
	header := map[string]string{"Content-Range": fmt.Sprintf("%d-%d", offset, offset+n-1)}
	resp, err := p.do(http.MethodPatch, loc, chunk, n, header)
	if err != nil {
		// The chunk may have partially landed before the connection broke; ask
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
	case resp.StatusCode == http.StatusNotFound:
		return 0, errSessionGone
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
		if resp.StatusCode == http.StatusNotFound {
			return 0, errSessionGone
		}
		lastErr = fmt.Errorf("read upload status: %s", resp.Status)
	}
	return 0, fmt.Errorf("read upload status: %w", lastErr)
}

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
