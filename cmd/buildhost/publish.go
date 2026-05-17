package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"
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
	publishCmd.Flags().String("os", "", "Target OS")
	publishCmd.Flags().String("arch", "", "Target architecture")
	publishCmd.Flags().String("kind", "binary", "Artifact kind (binary, library, assets, archive)")
	publishCmd.Flags().String("artifact", "", "Path to artifact file")
	publishCmd.Flags().String("git-branch", "", "Git branch")
	publishCmd.Flags().String("git-commit", "", "Git commit")
	publishCmd.Flags().String("manifest", "", "Path to release manifest (TOML)")
}

type manifest struct {
	Server    string             `toml:"server"`
	Token     string             `toml:"token"`
	Project   string             `toml:"project"`
	Version   string             `toml:"version"`
	GitBranch string             `toml:"git_branch"`
	GitCommit string             `toml:"git_commit"`
	Notes     string             `toml:"notes"`
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
		return publishFromManifest(manifestPath)
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

	if serverURL == "" || token == "" || project == "" || artifactPath == "" || osStr == "" || archStr == "" {
		return fmt.Errorf("--server, --token, --project, --artifact, --os, and --arch are required")
	}

	releaseBody, _ := json.Marshal(map[string]string{
		"version":    version,
		"git_branch": gitBranch,
		"git_commit": gitCommit,
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

	f, err := os.Open(artifactPath)
	if err != nil {
		return fmt.Errorf("open artifact: %w", err)
	}
	defer f.Close()

	url := fmt.Sprintf("%s/api/v1/projects/%s/releases/%s/artifacts/%s/%s?kind=%s",
		serverURL, project, rel.Version, osStr, archStr, kind)
	resp, err = doRequest("PUT", url, token, f)
	if err != nil {
		return fmt.Errorf("upload artifact: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("upload failed: %s", resp.Status)
	}

	fmt.Printf("uploaded %s/%s %s/%s\n", project, rel.Version, osStr, archStr)
	return nil
}

func publishFromManifest(path string) error {
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

	baseDir := filepath.Dir(path)
	for _, a := range m.Artifacts {
		artifactPath := a.Path
		if !filepath.IsAbs(artifactPath) {
			artifactPath = filepath.Join(baseDir, artifactPath)
		}

		f, err := os.Open(artifactPath)
		if err != nil {
			return fmt.Errorf("open %s: %w", a.Path, err)
		}

		kind := a.Kind
		if kind == "" {
			kind = "binary"
		}

		url := fmt.Sprintf("%s/api/v1/projects/%s/releases/%s/artifacts/%s/%s?kind=%s",
			m.Server, m.Project, rel.Version, a.OS, a.Arch, kind)

		req, err := http.NewRequest("PUT", url, f)
		if err != nil {
			f.Close()
			return err
		}
		req.Header.Set("Authorization", "Bearer "+m.Token)
		if a.Filename != "" {
			req.Header.Set("X-Artifact-Filename", a.Filename)
		}

		resp, err := http.DefaultClient.Do(req)
		f.Close()
		if err != nil {
			return fmt.Errorf("upload %s/%s: %w", a.OS, a.Arch, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			return fmt.Errorf("upload %s/%s failed: %s", a.OS, a.Arch, resp.Status)
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
