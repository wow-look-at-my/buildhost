// Chunked OCI upload sessions: blobs larger than the server's advertised
// direct-upload limit go through POST session + sequential PATCH appends +
// digest-checked PUT, resuming from the server's committed size. Split from
// ociclient.go, which holds the pusher, refs, and monolithic uploads.
package ociclient

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"time"
)

// rangePattern matches the inclusive "start-end" byte range the registry
// reports in Range headers.
var rangePattern = regexp.MustCompile(`^([0-9]+)-([0-9]+)$`)

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
