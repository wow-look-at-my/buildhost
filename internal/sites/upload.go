package sites

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
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

	var buf bytes.Buffer
	var fileCount int

	if r.Header.Get("Content-Type") == "application/zip" {
		zipData, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, `{"error":"failed to read request body"}`, http.StatusBadRequest)
			return
		}
		fileCount, err = zipToTar(zipData, &buf)
		if err != nil {
			span.RecordError(err)
			http.Error(w, `{"error":"invalid archive"}`, http.StatusBadRequest)
			return
		}
	} else {
		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			http.Error(w, `{"error":"invalid gzip data"}`, http.StatusBadRequest)
			return
		}
		defer gz.Close()

		limited := io.LimitReader(gz, maxSiteDecompressedSize+1)
		fileCount, err = validateTar(io.TeeReader(limited, &buf))
		if err != nil {
			span.RecordError(err)
			http.Error(w, `{"error":"invalid archive"}`, http.StatusBadRequest)
			return
		}
	}

	if int64(buf.Len()) > maxSiteDecompressedSize {
		http.Error(w, `{"error":"decompressed archive too large"}`, http.StatusRequestEntityTooLarge)
		return
	}

	hasher := sha256.New()
	hasher.Write(buf.Bytes())
	sha256hex := hex.EncodeToString(hasher.Sum(nil))

	span.SetAttributes(
		attribute.Int("sites.file_count", fileCount),
		attribute.Int("sites.size", buf.Len()),
	)

	storageKey, size, err := h.Store.Put(ctx, bytes.NewReader(buf.Bytes()))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "store failed")
		http.Error(w, `{"error":"failed to store site"}`, http.StatusInternalServerError)
		return
	}

	gitCommit := r.Header.Get("X-Git-Commit")

	site := &db.Site{
		ProjectID:  project.ID,
		Branch:     rt.branch,
		StorageKey: storageKey,
		Size:       size,
		SHA256:     sha256hex,
		FileCount:  int64(fileCount),
		GitCommit:  gitCommit,
	}

	oldKey, err := h.DB.UpsertSite(ctx, site)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "upsert failed")
		http.Error(w, `{"error":"failed to save site"}`, http.StatusInternalServerError)
		return
	}

	if oldKey != "" && oldKey != storageKey {
		_ = h.Store.Delete(ctx, oldKey)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(site)
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

// zipToTar converts a ZIP archive to tar format, applying the same safety
// checks as validateTar (path traversal, file count, entry type restrictions).
func zipToTar(data []byte, out *bytes.Buffer) (int, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return 0, fmt.Errorf("invalid zip archive: %w", err)
	}

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
