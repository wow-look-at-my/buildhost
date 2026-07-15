package sites

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/retention"
)

const (
	maxSiteUploadSize       = 256 << 20 // 256 MiB
	maxSiteDecompressedSize = 1 << 30   // 1 GiB
	maxFileCount            = 10000
)

func (h *Handler) Upload(w http.ResponseWriter, r *http.Request) {
	ctx, span := sitesTracer.Start(r.Context(), "sites.upload")
	defer span.End()

	project := auth.ProjectFrom(ctx)
	rt := routeFrom(ctx)

	span.SetAttributes(
		attribute.String("sites.project", project.Name),
		attribute.String("sites.branch", rt.branch),
	)

	r.Body = http.MaxBytesReader(w, r.Body, maxSiteUploadSize)

	// Resolve body and content type — either direct upload or URL fetch.
	bodyReader := io.Reader(r.Body)
	bodyContentType := r.Header.Get("Content-Type")
	if bodyContentType == "application/json" {
		var fetchReq struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		}
		if err := json.NewDecoder(r.Body).Decode(&fetchReq); err != nil {
			http.Error(w, `{"error":"invalid json body"}`, http.StatusBadRequest)
			return
		}
		fetched, fetchedCT, err := fetchFromURL(ctx, fetchReq.URL, fetchReq.Headers, h.FetchDomains)
		if err != nil {
			span.RecordError(err)
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
			return
		}
		defer fetched.Close()
		bodyReader = io.LimitReader(fetched, maxSiteUploadSize+1)
		bodyContentType = fetchedCT
	}

	var (
		storageKey         string
		size               int64
		sha256hex          string
		fileCount          int
		validErr, storeErr error
	)

	if bodyContentType == "application/zip" {
		// ZIP needs random access (its central directory is at the end), so it can't be
		// read from a forward-only stream. Spool the upload to a temp file under the data
		// volume (/tmp is read-only in the hardened image) and read it via ReaderAt --
		// never the whole zip in memory.
		tmp, terr := os.CreateTemp(h.TmpDir, "site-zip-*")
		if terr != nil {
			http.Error(w, `{"error":"failed to buffer upload"}`, http.StatusInternalServerError)
			return
		}
		defer func() {
			tmp.Close()
			os.Remove(tmp.Name())
		}()
		zipSize, rerr := io.Copy(tmp, bodyReader)
		if rerr != nil {
			http.Error(w, `{"error":"failed to read request body"}`, http.StatusBadRequest)
			return
		}
		zr, zerr := zip.NewReader(tmp, zipSize)
		if zerr != nil {
			span.RecordError(zerr)
			http.Error(w, `{"error":"invalid archive"}`, http.StatusBadRequest)
			return
		}
		storageKey, size, sha256hex, fileCount, validErr, storeErr = h.storeTar(ctx, func(out io.Writer) (int, error) {
			return zipToTar(zr, out)
		})
	} else {
		gz, gerr := gzip.NewReader(bodyReader)
		if gerr != nil {
			http.Error(w, `{"error":"invalid gzip data"}`, http.StatusBadRequest)
			return
		}
		defer gz.Close()
		storageKey, size, sha256hex, fileCount, validErr, storeErr = h.storeTar(ctx, func(out io.Writer) (int, error) {
			return validateTar(io.TeeReader(gz, out))
		})
	}

	if validErr != nil {
		span.RecordError(validErr)
		if errors.Is(validErr, errSiteTooLarge) {
			http.Error(w, `{"error":"decompressed archive too large"}`, http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, `{"error":"invalid archive"}`, http.StatusBadRequest)
		}
		return
	}
	if storeErr != nil {
		span.RecordError(storeErr)
		span.SetStatus(codes.Error, "store failed")
		http.Error(w, `{"error":"failed to store site"}`, http.StatusInternalServerError)
		return
	}

	span.SetAttributes(
		attribute.Int("sites.file_count", fileCount),
		attribute.Int64("sites.size", size),
	)

	gitCommit := r.Header.Get("X-Git-Commit")
	// Opt-in: a site published with X-Public-Site: true is served without a
	// token even when its project is private (e.g. a PR preview of a private
	// repo). The project's own visibility -- and thus its release artifacts --
	// is unaffected; only this site's read path is opened.
	isPublic := r.Header.Get("X-Public-Site") == "true"

	site := &db.Site{
		ProjectID:  project.ID,
		Branch:     rt.branch,
		StorageKey: storageKey,
		Size:       size,
		SHA256:     sha256hex,
		FileCount:  int64(fileCount),
		GitCommit:  gitCommit,
		IsPublic:   isPublic,
	}

	span.SetAttributes(attribute.Bool("sites.public", isPublic))

	oldKey, err := h.DB.UpsertSite(ctx, site)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "upsert failed")
		http.Error(w, `{"error":"failed to save site"}`, http.StatusInternalServerError)
		return
	}

	// Delete the replaced site's blob only if no other row (another branch, an
	// artifact, an OCI image) still references that content-addressed key.
	if oldKey != "" && oldKey != storageKey {
		_, _ = retention.DeleteBlobIfUnreferenced(ctx, h.DB, h.Store, oldKey, true)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(site)
}

var errSiteTooLarge = errors.New("decompressed archive too large")

// storeTar streams the tar produced by writeTar into the store while validating,
// hashing and enforcing the decompressed-size cap -- without ever buffering the whole
// archive. writeTar writes the tar to the provided writer and returns the file count.
// validErr is a client error (invalid archive or over the cap); storeErr is a server
// error from the storage backend.
func (h *Handler) storeTar(ctx context.Context, writeTar func(io.Writer) (int, error)) (key string, size int64, sha string, fileCount int, validErr, storeErr error) {
	hasher := sha256.New()
	pr, pw := io.Pipe()
	type result struct {
		n   int
		err error
	}
	rc := make(chan result, 1)
	go func() {
		capped := &cappedWriter{w: io.MultiWriter(pw, hasher), max: maxSiteDecompressedSize}
		n, werr := writeTar(capped)
		pw.CloseWithError(werr)
		rc <- result{n, werr}
	}()

	key, size, perr := h.Store.Put(ctx, pr)
	res := <-rc
	if res.err != nil {
		return "", 0, "", 0, res.err, nil
	}
	if perr != nil {
		return "", 0, "", 0, nil, perr
	}
	return key, size, hex.EncodeToString(hasher.Sum(nil)), res.n, nil, nil
}

// cappedWriter forwards writes to w until more than max bytes have been written, then
// fails with errSiteTooLarge so a gzip/zip bomb can't be expanded without bound.
type cappedWriter struct {
	w   io.Writer
	n   int64
	max int64
}

func (c *cappedWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	if err == nil && c.n > c.max {
		return n, errSiteTooLarge
	}
	return n, err
}

func validateTar(r io.Reader) (int, error) {
	tr := tar.NewReader(r)
	count := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("invalid tar archive: %w", err)
		}

		name := path.Clean(hdr.Name)
		if path.IsAbs(name) {
			return 0, fmt.Errorf("absolute path not allowed: %s", hdr.Name)
		}
		if strings.HasPrefix(name, "..") || strings.Contains(name, "/..") {
			return 0, fmt.Errorf("path traversal not allowed: %s", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeReg, tar.TypeDir:
		default:
			return 0, fmt.Errorf("unsupported entry type %d for %s (only regular files and directories allowed)", hdr.Typeflag, hdr.Name)
		}

		if hdr.Typeflag == tar.TypeReg {
			count++
			if count > maxFileCount {
				return 0, fmt.Errorf("too many files (max %d)", maxFileCount)
			}
		}

		if _, err := io.Copy(io.Discard, tr); err != nil {
			return 0, fmt.Errorf("read entry %s: %w", hdr.Name, err)
		}
	}
	if count == 0 {
		return 0, fmt.Errorf("archive contains no files")
	}
	return count, nil
}

var siteFetchClient = &http.Client{
	Timeout: 5 * time.Minute,
	// Strip auth headers when a redirect goes to a different host (e.g., CDN redirect).
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 0 && req.URL.Host != via[0].URL.Host {
			req.Header.Del("Authorization")
		}
		if len(via) >= 10 {
			return fmt.Errorf("too many redirects")
		}
		return nil
	},
}

// fetchFromURL fetches a URL on behalf of the upload, enforcing an allowlist of
// trusted hostnames to prevent SSRF. Returns the response body and its Content-Type.
func fetchFromURL(ctx context.Context, rawURL string, headers map[string]string, allowedDomains []string) (io.ReadCloser, string, error) {
	if len(allowedDomains) == 0 {
		return nil, "", fmt.Errorf("fetch mode not enabled on this server")
	}
	if rawURL == "" {
		return nil, "", fmt.Errorf("url is required")
	}

	parsed, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("invalid url")
	}
	if parsed.URL.Scheme != "https" {
		return nil, "", fmt.Errorf("only https URLs are allowed")
	}
	if !slices.Contains(allowedDomains, parsed.URL.Hostname()) {
		return nil, "", fmt.Errorf("fetch domain %q not in allowed list", parsed.URL.Hostname())
	}
	for k, v := range headers {
		parsed.Header.Set(k, v)
	}
	parsed = parsed.WithContext(ctx)

	resp, err := siteFetchClient.Do(parsed)
	if err != nil {
		return nil, "", fmt.Errorf("fetch failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, "", fmt.Errorf("fetch returned %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/gzip"
	}
	return resp.Body, ct, nil
}

// zipToTar streams a ZIP archive out as a tar, applying the same safety checks as
// validateTar (path traversal, file count, entry type restrictions).
func zipToTar(zr *zip.Reader, out io.Writer) (int, error) {
	tw := tar.NewWriter(out)
	count := 0

	for _, f := range zr.File {
		name := path.Clean(f.Name)

		if path.IsAbs(name) {
			return 0, fmt.Errorf("absolute path not allowed: %s", f.Name)
		}
		if strings.HasPrefix(name, "..") || strings.Contains(name, "/..") {
			return 0, fmt.Errorf("path traversal not allowed: %s", f.Name)
		}

		info := f.FileInfo()
		if info.IsDir() {
			if err := tw.WriteHeader(&tar.Header{
				Typeflag: tar.TypeDir,
				Name:     name + "/",
				Mode:     0755,
			}); err != nil {
				return 0, fmt.Errorf("write dir header %s: %w", name, err)
			}
			continue
		}

		if !info.Mode().IsRegular() {
			return 0, fmt.Errorf("unsupported entry type for %s (only regular files and directories allowed)", f.Name)
		}

		rc, err := f.Open()
		if err != nil {
			return 0, fmt.Errorf("open entry %s: %w", f.Name, err)
		}

		if err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg,
			Name:     name,
			Size:     int64(f.UncompressedSize64),
			Mode:     0644,
		}); err != nil {
			rc.Close()
			return 0, fmt.Errorf("write header %s: %w", name, err)
		}

		if _, err := io.Copy(tw, rc); err != nil {
			rc.Close()
			return 0, fmt.Errorf("copy entry %s: %w", name, err)
		}
		rc.Close()

		count++
		if count > maxFileCount {
			return 0, fmt.Errorf("too many files (max %d)", maxFileCount)
		}
	}

	if count == 0 {
		return 0, fmt.Errorf("archive contains no files")
	}

	return count, tw.Close()
}
