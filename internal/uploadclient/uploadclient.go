// Package uploadclient uploads a file to any buildhost upload endpoint,
// transparently switching to a chunked upload session when the file is too
// large for a single request to pass the proxy in front of the server
// (Cloudflare's edge rejects request bodies over ~100 MB with a 413 that
// never reaches the origin).
//
// The direct/chunked decision is made purely from the local file size plus
// the limit the server advertises on GET /api/v1/server-info -- never by
// reacting to a failed oversized attempt. The first attempt is the one that
// succeeds.
package uploadclient

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

const (
	// DefaultChunkThreshold mirrors the server's default
	// max_direct_upload_bytes (95 MiB, just under Cloudflare's 100 MB edge
	// cap): files larger than this go through a chunked upload session. The
	// live value from /api/v1/server-info wins when reachable; this is the
	// fallback.
	DefaultChunkThreshold int64 = 95 << 20 // 95 MiB
	// DefaultChunkSize is how much of the file each chunk request carries.
	DefaultChunkSize int64 = 64 << 20 // 64 MiB
	// retryAttempts bounds per-chunk retries (and no-progress loops).
	retryAttempts = 4
)

// RetryBaseDelay is doubled per retry (1s, 2s, 4s). Var so tests can shorten it.
var RetryBaseDelay = time.Second

// Uploader uploads files to a buildhost server's upload endpoints.
type Uploader struct {
	// Server is the apex server URL, hosting /api/v1/uploads and
	// /api/v1/server-info. Upload targets may be on service subdomains.
	Server string
	// Token authenticates every request (Bearer).
	Token string
	// Client defaults to http.DefaultClient.
	Client *http.Client
	// ChunkSize is the per-request chunk size; 0 uses DefaultChunkSize and a
	// negative value disables chunked uploads entirely (always direct).
	ChunkSize int64
	// Threshold overrides the direct-upload limit; 0 asks the server (falling
	// back to DefaultChunkThreshold on any error).
	Threshold int64
	// Stdout receives chunk progress lines; nil discards them.
	Stdout io.Writer

	// info caches the parsed /api/v1/server-info response (fetched at most
	// once per Uploader; the zero value on any error).
	info        serverInfo
	infoFetched bool
}

// serverInfo is the subset of GET /api/v1/server-info this client consumes.
type serverInfo struct {
	MaxDirectUploadBytes int64 `json:"max_direct_upload_bytes"`
	UploadBySHA256       bool  `json:"upload_by_sha256"`
}

func (u *Uploader) client() *http.Client {
	if u.Client != nil {
		return u.Client
	}
	return http.DefaultClient
}

func (u *Uploader) stdout() io.Writer {
	if u.Stdout != nil {
		return u.Stdout
	}
	return io.Discard
}

func (u *Uploader) chunkSize() int64 {
	if u.ChunkSize == 0 {
		return DefaultChunkSize
	}
	return u.ChunkSize
}

// Upload sends the file at path to target (method + URL of any upload
// endpoint) with the given headers. The returned response is the endpoint's
// own response (direct or finalize); the caller checks its status as usual.
func (u *Uploader) Upload(method, target string, header map[string]string, path string) (*http.Response, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	size := st.Size()

	if u.chunkSize() <= 0 || size <= u.directLimit() {
		return u.direct(method, target, header, f)
	}
	return u.chunked(method, target, header, f, size)
}

// directLimit resolves the largest size to send as one request: the server's
// advertised max_direct_upload_bytes when reachable, else the built-in
// default. Never fails -- any error just means the fallback.
func (u *Uploader) directLimit() int64 {
	if u.Threshold > 0 {
		return u.Threshold
	}
	u.Threshold = DefaultChunkThreshold
	if info := u.serverInfo(); info.MaxDirectUploadBytes > 0 {
		u.Threshold = info.MaxDirectUploadBytes
	}
	return u.Threshold
}

// serverInfo fetches and caches /api/v1/server-info. Any fetch or parse
// failure yields the zero value (built-in threshold, no optional
// capabilities) -- never an error, since every capability has a safe fallback.
func (u *Uploader) serverInfo() serverInfo {
	if u.infoFetched {
		return u.info
	}
	u.infoFetched = true
	req, err := http.NewRequest(http.MethodGet, u.Server+"/api/v1/server-info", nil)
	if err != nil {
		return u.info
	}
	c := *u.client()
	c.Timeout = 5 * time.Second
	resp, err := c.Do(req)
	if err != nil {
		return u.info
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return u.info
	}
	var info serverInfo
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&info) == nil {
		u.info = info
	}
	return u.info
}

// SupportsUploadBySHA256 reports whether the server advertises the
// upload_by_sha256 capability: an empty-body artifact PUT carrying
// ?upload_sha256=<hex> registers an already-uploaded blob for another os/arch
// slot without re-sending the bytes. Hash-reference uploads MUST be gated on
// this -- a server without the capability silently ignores the parameter and
// would store the empty request body as the artifact, permanently poisoning
// the slot. Unknown/unreachable servers report false (full uploads always
// work).
func (u *Uploader) SupportsUploadBySHA256() bool {
	return u.serverInfo().UploadBySHA256
}

// UploadByHash performs a hash-reference upload: an empty-body request whose
// upload_sha256 query parameter names a blob the project already uploaded.
// The caller must have confirmed SupportsUploadBySHA256 first, and should
// fall back to a full Upload on any response other than 201 (e.g. a 404 for a
// blob the server has since garbage-collected).
func (u *Uploader) UploadByHash(method, target string, header map[string]string, sha256hex string) (*http.Response, error) {
	parsed, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("parse upload URL: %w", err)
	}
	q := parsed.Query()
	q.Set("upload_sha256", sha256hex)
	parsed.RawQuery = q.Encode()

	req, err := http.NewRequest(method, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+u.Token)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	return u.client().Do(req)
}

// direct is the classic single-request upload, matching what the CLI always
// sent for small files.
func (u *Uploader) direct(method, target string, header map[string]string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, target, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+u.Token)
	req.Header.Set("Content-Type", "application/octet-stream")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	return u.client().Do(req)
}

// chunked uploads via an upload session: create, append chunks (resuming from
// the server's committed size on any hiccup), then finalize the original
// request by reference with the file's sha256 for integrity.
func (u *Uploader) chunked(method, target string, header map[string]string, f *os.File, size int64) (*http.Response, error) {
	sum, err := fileSHA256(f)
	if err != nil {
		return nil, fmt.Errorf("hash %s: %w", f.Name(), err)
	}

	id, err := u.createSession()
	if err != nil {
		return nil, err
	}

	chunkSize := u.chunkSize()
	totalChunks := (size + chunkSize - 1) / chunkSize
	fmt.Fprintf(u.stdout(), "uploading %d MiB in %d chunks of %d MiB\n", size>>20, totalChunks, chunkSize>>20)

	if err := u.sendChunks(id, f, size, totalChunks); err != nil {
		u.abortSession(id)
		return nil, err
	}

	resp, err := u.finalize(method, target, header, id, sum)
	if err != nil {
		u.abortSession(id)
		return nil, err
	}
	return resp, nil
}

// sendChunks appends the file to the session sequentially. Every iteration
// trusts the server's committed size (returned by appends, conflicts, and
// status reads), so a partially delivered chunk resumes instead of restarting.
func (u *Uploader) sendChunks(id string, f *os.File, size, totalChunks int64) error {
	chunkSize := u.chunkSize()
	offset := int64(0)
	failures := 0
	for offset < size {
		n := min(chunkSize, size-offset)
		newOffset, err := u.appendChunk(id, offset, io.NewSectionReader(f, offset, n))
		if err != nil {
			return err
		}
		if newOffset <= offset {
			failures++
			if failures >= retryAttempts {
				return fmt.Errorf("upload session %s made no progress at offset %d", id, offset)
			}
			time.Sleep(RetryBaseDelay << (failures - 1))
		} else {
			failures = 0
			fmt.Fprintf(u.stdout(), "  chunk %d/%d uploaded (%d/%d bytes)\n",
				(newOffset+chunkSize-1)/chunkSize, totalChunks, newOffset, size)
		}
		offset = newOffset
	}
	return nil
}

// createSession opens an upload session. Returns ("", nil) when the server
// does not implement the endpoint (older buildhost), so the caller can fall
// back to a direct upload.
func (u *Uploader) createSession() (string, error) {
	resp, err := u.sessionRequest(http.MethodPost, "/api/v1/uploads", nil)
	if err != nil {
		return "", fmt.Errorf("create upload session: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("create upload session failed: %s: %s", resp.Status, readErrBody(resp))
	}
	var sess struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&sess); err != nil || sess.ID == "" {
		return "", fmt.Errorf("create upload session: bad response")
	}
	return sess.ID, nil
}

// appendChunk PATCHes one chunk and returns the server's committed size. A
// 409 (offset conflict / busy) and a transport error both resolve to a status
// read, so the caller resumes from wherever the server actually is.
func (u *Uploader) appendChunk(id string, offset int64, chunk io.Reader) (int64, error) {
	resp, err := u.sessionRequest(http.MethodPatch, fmt.Sprintf("/api/v1/uploads/%s?offset=%d", id, offset), chunk)
	if err != nil {
		// The chunk may have partially landed before the connection broke; ask
		// the server how much it has and resume from there.
		return u.sessionSize(id)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusConflict:
		var body struct {
			Size *int64 `json:"size"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.Size == nil {
			return 0, fmt.Errorf("upload chunk at %d: bad response", offset)
		}
		return *body.Size, nil
	case resp.StatusCode >= 500:
		// Transient server error: re-read the committed size and resume.
		return u.sessionSize(id)
	default:
		return 0, fmt.Errorf("upload chunk at %d failed: %s: %s", offset, resp.Status, readErrBody(resp))
	}
}

// sessionSize reads the session's committed size for resuming.
func (u *Uploader) sessionSize(id string) (int64, error) {
	var lastErr error
	for attempt := range retryAttempts {
		if attempt > 0 {
			time.Sleep(RetryBaseDelay << (attempt - 1))
		}
		resp, err := u.sessionRequest(http.MethodGet, "/api/v1/uploads/"+id, nil)
		if err != nil {
			lastErr = err
			continue
		}
		var body struct {
			Size *int64 `json:"size"`
		}
		err = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK && err == nil && body.Size != nil {
			return *body.Size, nil
		}
		lastErr = fmt.Errorf("read upload session size: %s", resp.Status)
	}
	return 0, fmt.Errorf("read upload session size: %w", lastErr)
}

// finalize re-issues the original upload request with an empty body,
// referencing the session and its expected sha256.
func (u *Uploader) finalize(method, target string, header map[string]string, id, sum string) (*http.Response, error) {
	parsed, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("parse upload URL: %w", err)
	}
	q := parsed.Query()
	q.Set("upload_session", id)
	q.Set("upload_sha256", sum)
	parsed.RawQuery = q.Encode()

	req, err := http.NewRequest(method, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+u.Token)
	req.Header.Set("Content-Type", "application/octet-stream")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := u.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("finalize upload: %w", err)
	}
	return resp, nil
}

// abortSession best-effort deletes a session after a hard failure.
func (u *Uploader) abortSession(id string) {
	if resp, err := u.sessionRequest(http.MethodDelete, "/api/v1/uploads/"+id, nil); err == nil {
		resp.Body.Close()
	}
}

func (u *Uploader) sessionRequest(method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, u.Server+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+u.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	return u.client().Do(req)
}

// FileSHA256 returns the hex SHA-256 of the file at path -- the value
// UploadByHash sends, computed the same way the server keys its
// content-addressed storage.
func FileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return fileSHA256(f)
}

// fileSHA256 hashes the whole file and rewinds it.
func fileSHA256(f *os.File) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// readErrBody returns a short prefix of an error response body for messages.
func readErrBody(resp *http.Response) string {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return string(bytes.TrimSpace(b))
}
