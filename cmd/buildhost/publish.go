package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"

	"github.com/wow-look-at-my/buildhost/internal/uploadclient"
)

func init() {
	rootCmd.AddCommand(publishCmd)
}

var publishCmd = &cobra.Command{
	Use:   "publish",
	Short: "Publish artifacts to the registry",
	RunE:  runPublish,
}

func init() {
	publishCmd.Flags().String("server", "", "Registry server URL")
	publishCmd.Flags().String("token", "", "API token")
	publishCmd.Flags().String("project", "", "Project name")
	publishCmd.Flags().String("version", "", "Version (auto-assigned if omitted for auto-versioned projects)")
	publishCmd.Flags().String("os", "", "Target OS: one name, a comma-separated list (linux,darwin,windows), or cosmo/any for all of linux+darwin+windows")
	publishCmd.Flags().String("arch", "", "Target architecture: one name, a comma-separated list, or any for amd64+arm64")
	publishCmd.Flags().String("kind", "binary", "Artifact kind (binary, library, assets, archive)")
	publishCmd.Flags().String("artifact", "", "Path to artifact file")
	publishCmd.Flags().String("git-branch", "", "Git branch")
	publishCmd.Flags().String("git-commit", "", "Git commit")
	publishCmd.Flags().String("oci-user", "", "Run-as user for synthesized OCI images (uid[:gid] or name[:group]); default is root")
	publishCmd.Flags().String("manifest", "", "Path to release manifest (TOML)")
	publishCmd.Flags().Bool("draft", false, "Upload without publishing: the release stays out of latest/branch resolution and every package manager, downloadable only by its exact version")
	addChunkSizeFlag(publishCmd)
}

type manifest struct {
	Server    string             `toml:"server"`
	Token     string             `toml:"token"`
	Project   string             `toml:"project"`
	Version   string             `toml:"version"`
	GitBranch string             `toml:"git_branch"`
	GitCommit string             `toml:"git_commit"`
	Notes     string             `toml:"notes"`
	OciUser   string             `toml:"oci_user"`
	Artifacts []manifestArtifact `toml:"artifact"`
}

type manifestArtifact struct {
	OS       string `toml:"os"`
	Arch     string `toml:"arch"`
	Kind     string `toml:"kind"`
	Path     string `toml:"path"`
	Filename string `toml:"filename"`
}

func runPublish(cmd *cobra.Command, _ []string) error {
	manifestPath, _ := cmd.Flags().GetString("manifest")
	if manifestPath != "" {
		return publishFromManifest(cmd, manifestPath)
	}
	return publishSingle(cmd)
}

func publishSingle(cmd *cobra.Command) error {
	serverURL, _ := cmd.Flags().GetString("server")
	token, _ := cmd.Flags().GetString("token")
	project, _ := cmd.Flags().GetString("project")
	version, _ := cmd.Flags().GetString("version")
	osStr, _ := cmd.Flags().GetString("os")
	archStr, _ := cmd.Flags().GetString("arch")
	kind, _ := cmd.Flags().GetString("kind")
	artifactPath, _ := cmd.Flags().GetString("artifact")
	gitBranch, _ := cmd.Flags().GetString("git-branch")
	gitCommit, _ := cmd.Flags().GetString("git-commit")
	ociUser, _ := cmd.Flags().GetString("oci-user")
	draft, _ := cmd.Flags().GetBool("draft")

	if serverURL == "" || token == "" || project == "" || artifactPath == "" || osStr == "" || archStr == "" {
		return fmt.Errorf("--server, --token, --project, --artifact, --os, and --arch are required")
	}

	releaseBody, _ := json.Marshal(map[string]any{
		"version":    version,
		"git_branch": gitBranch,
		"git_commit": gitCommit,
		"oci_user":   ociUser,
		"draft":      draft,
	})
	resp, err := doRequest("POST", serverURL+"/api/v1/projects/"+project+"/releases", token, bytes.NewReader(releaseBody))
	if err != nil {
		return fmt.Errorf("create release: %w", err)
	}

	var rel struct{ Version string }
	json.NewDecoder(resp.Body).Decode(&rel)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusConflict {
		return fmt.Errorf("create release failed: %s", resp.Status)
	}
	if rel.Version == "" {
		rel.Version = version
	}

	up, err := newUploader(cmd, serverURL, token)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/api/v1/projects/%s/releases/%s/artifacts/%s/%s?kind=%s",
		serverURL, project, rel.Version, osStr, archStr, kind)
	resp, err = up.Upload("PUT", url, nil, artifactPath)
	if err != nil {
		return fmt.Errorf("upload artifact: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("upload failed: %s", resp.Status)
	}

	fmt.Printf("uploaded %s/%s %s/%s\n", project, rel.Version, osStr, archStr)

	// A draft stops here by design: it stays unpublished, so latest/branch
	// resolution and every package manager ignore it, and retention keeps it
	// rather than sweeping it as an abandoned upload. Print the exact-version
	// URL, which is the only way to reach it.
	if draft {
		fmt.Printf("draft %s/%s (not published)\n", project, rel.Version)
		fmt.Printf("download: %s?project=%s&v=%s&os=%s&arch=%s\n",
			strings.Replace(serverURL, "://", "://static.", 1)+"/file", project, rel.Version, osStr, archStr)
		return nil
	}

	// Publish the release, exactly like manifest mode: an unpublished release
	// is invisible to latest/branch resolution (dl, brew, apt, npm, the web
	// frontend) and is eventually swept by retention as an abandoned upload --
	// and the CLI has no other way to publish it later.
	resp, err = doRequest("POST", fmt.Sprintf("%s/api/v1/projects/%s/releases/%s/publish", serverURL, project, rel.Version), token, nil)
	if err != nil {
		return fmt.Errorf("publish release: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("publish failed: %s", resp.Status)
	}

	fmt.Printf("published %s/%s\n", project, rel.Version)
	return nil
}

func publishFromManifest(cmd *cobra.Command, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}

	var m manifest
	if err := toml.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("parse manifest: %w", err)
	}

	releaseBody, _ := json.Marshal(map[string]string{
		"version":    m.Version,
		"git_branch": m.GitBranch,
		"git_commit": m.GitCommit,
		"notes":      m.Notes,
		"oci_user":   m.OciUser,
	})
	resp, err := doRequest("POST", m.Server+"/api/v1/projects/"+m.Project+"/releases", m.Token, bytes.NewReader(releaseBody))
	if err != nil {
		return fmt.Errorf("create release: %w", err)
	}
	var rel struct{ Version string }
	json.NewDecoder(resp.Body).Decode(&rel)
	resp.Body.Close()
	if rel.Version == "" {
		rel.Version = m.Version
	}

	up, err := newUploader(cmd, m.Server, m.Token)
	if err != nil {
		return err
	}

	// When the server supports hash-reference uploads, byte-identical manifest
	// entries transfer once: the first entry of each (sha256, kind) group
	// sends the bytes, the rest register the stored blob by reference -- each
	// keeping its own per-entry filename header. Without the capability the
	// loop is exactly the classic one-full-upload-per-entry (and never probes:
	// an old server ignores upload_sha256 and would store the empty body).
	canHashRef := up.SupportsUploadBySHA256()
	type blobGroup struct{ sum, kind string }
	uploaded := make(map[blobGroup]bool)

	baseDir := filepath.Dir(path)
	for _, a := range m.Artifacts {
		artifactPath := a.Path
		if !filepath.IsAbs(artifactPath) {
			artifactPath = filepath.Join(baseDir, artifactPath)
		}

		kind := a.Kind
		if kind == "" {
			kind = "binary"
		}

		url := fmt.Sprintf("%s/api/v1/projects/%s/releases/%s/artifacts/%s/%s?kind=%s",
			m.Server, m.Project, rel.Version, a.OS, a.Arch, kind)

		var header map[string]string
		if a.Filename != "" {
			header = map[string]string{"X-Artifact-Filename": a.Filename}
		}

		var group blobGroup
		if canHashRef {
			sum, err := uploadclient.FileSHA256(artifactPath)
			if err != nil {
				return fmt.Errorf("hash %s: %w", artifactPath, err)
			}
			group = blobGroup{sum, kind}
			if uploaded[group] {
				resp, err := up.UploadByHash("PUT", url, header, sum)
				if err != nil {
					return fmt.Errorf("upload %s/%s: %w", a.OS, a.Arch, err)
				}
				resp.Body.Close()
				switch resp.StatusCode {
				case http.StatusCreated:
					fmt.Printf("registered %s/%s %s/%s (existing blob, no bytes sent)\n", m.Project, rel.Version, a.OS, a.Arch)
					continue
				case http.StatusConflict:
					// A real slot conflict -- the full upload would 409 too.
					return fmt.Errorf("upload %s/%s failed: %s", a.OS, a.Arch, resp.Status)
				default:
					// E.g. 404: the blob vanished between uploads. The bytes
					// are right here -- fall back to sending them.
					fmt.Printf("hash-reference upload for %s/%s returned %s; sending full upload\n", a.OS, a.Arch, resp.Status)
				}
			}
		}

		resp, err := up.Upload("PUT", url, header, artifactPath)
		if err != nil {
			return fmt.Errorf("upload %s/%s: %w", a.OS, a.Arch, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			return fmt.Errorf("upload %s/%s failed: %s", a.OS, a.Arch, resp.Status)
		}
		if canHashRef {
			uploaded[group] = true
		}

		fmt.Printf("uploaded %s/%s %s/%s\n", m.Project, rel.Version, a.OS, a.Arch)
	}

	resp, err = doRequest("POST", fmt.Sprintf("%s/api/v1/projects/%s/releases/%s/publish", m.Server, m.Project, rel.Version), m.Token, nil)
	if err != nil {
		return fmt.Errorf("publish release: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("publish failed: %s", resp.Status)
	}

	fmt.Printf("published %s/%s\n", m.Project, rel.Version)
	return nil
}

func doRequest(method, url, token string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	return http.DefaultClient.Do(req)
}
