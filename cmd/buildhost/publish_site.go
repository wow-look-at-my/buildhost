package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/wow-look-at-my/buildhost/internal/serviceurl"
)

func init() {
	rootCmd.AddCommand(publishSiteCmd)
	publishSiteCmd.Flags().String("server", "", "Registry server URL")
	publishSiteCmd.Flags().String("token", "", "API token")
	publishSiteCmd.Flags().String("project", "", "Project name")
	publishSiteCmd.Flags().String("branch", "", "Branch name")
	publishSiteCmd.Flags().String("dir", "", "Directory containing site files")
	publishSiteCmd.Flags().String("git-commit", "", "Git commit SHA")
	addChunkSizeFlag(publishSiteCmd)
}

var publishSiteCmd = &cobra.Command{
	Use:   "publish-site",
	Short: "Publish a static site to the registry",
	RunE:  runPublishSite,
}

func runPublishSite(cmd *cobra.Command, _ []string) error {
	serverURL, _ := cmd.Flags().GetString("server")
	token, _ := cmd.Flags().GetString("token")
	project, _ := cmd.Flags().GetString("project")
	branch, _ := cmd.Flags().GetString("branch")
	dir, _ := cmd.Flags().GetString("dir")
	gitCommit, _ := cmd.Flags().GetString("git-commit")

	if serverURL == "" || token == "" || project == "" || branch == "" || dir == "" {
		return fmt.Errorf("--server, --token, --project, --branch, and --dir are required")
	}

	sitesBase, err := serviceurl.Base(serverURL, "sites")
	if err != nil {
		return err
	}

	// Archive to a temp file (not a streaming pipe) so the upload knows the
	// size up front -- that is what lets it choose a chunked upload session
	// for archives too large for a single request to pass the proxy in front
	// of the server.
	tmp, err := os.CreateTemp("", "buildhost-site-*.tar.gz")
	if err != nil {
		return fmt.Errorf("create archive temp file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := createTarGz(tmp, dir); err != nil {
		tmp.Close()
		return fmt.Errorf("create archive: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("create archive: %w", err)
	}

	up, err := newUploader(cmd, serverURL, token)
	if err != nil {
		return err
	}
	header := map[string]string{"Content-Type": "application/gzip"}
	if gitCommit != "" {
		header["X-Git-Commit"] = gitCommit
	}

	endpoint := fmt.Sprintf("%s/%s/branch/%s", sitesBase, project, branch)
	resp, err := up.Upload("PUT", endpoint, header, tmp.Name())
	if err != nil {
		return fmt.Errorf("upload site: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("upload failed: %s: %s", resp.Status, body)
	}

	fmt.Printf("published site %s branch %s\n", project, branch)
	fmt.Printf("  %s\n", publishedSiteURL(body, sitesBase, project, branch))
	return nil
}

// publishedSiteURL is where the site is now served. Never derive it: the server
// owns the URL grammar and reports it. The fallback is for servers predating
// that field -- keep it on the canonical spelling, not the /branch/ one the
// upload endpoint uses. see docs/sites.md
func publishedSiteURL(body []byte, sitesBase, project, branch string) string {
	var resp struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &resp); err == nil && resp.URL != "" {
		return resp.URL
	}
	return fmt.Sprintf("%s/%s/@%s/", sitesBase, project, branch)
}

func createTarGz(w io.Writer, dir string) error {
	gw := gzip.NewWriter(w)
	tw := tar.NewWriter(gw)

	dir = filepath.Clean(dir)
	err := filepath.Walk(dir, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(dir, filePath)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}

		if !info.Mode().IsRegular() && !info.IsDir() {
			return nil
		}

		if shouldSkip(rel) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		hdr := &tar.Header{
			Name:    rel,
			Size:    info.Size(),
			Mode:    int64(info.Mode().Perm()),
			ModTime: info.ModTime(),
		}

		if info.IsDir() {
			hdr.Typeflag = tar.TypeDir
			if !strings.HasSuffix(hdr.Name, "/") {
				hdr.Name += "/"
			}
			hdr.Size = 0
			return tw.WriteHeader(hdr)
		}

		hdr.Typeflag = tar.TypeReg
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}

		f, err := os.Open(filePath)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		return err
	}

	if err := tw.Close(); err != nil {
		return err
	}
	return gw.Close()
}

func shouldSkip(name string) bool {
	base := filepath.Base(name)
	switch base {
	case ".git", ".svn", ".hg", ".DS_Store", "Thumbs.db":
		return true
	}
	return false
}
