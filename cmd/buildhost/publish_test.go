package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestPublishSingleModePublishesRelease guards single-mode `buildhost
// publish` (no --manifest) actually publishing the release it creates. It
// used to stop after create+upload, leaving the release permanently
// unpublished: invisible to latest/branch resolution everywhere (dl, brew,
// apt, npm, the web frontend -- the documented quick-start download 404'd),
// eventually swept by retention as an abandoned upload, and with no CLI
// command to publish it after the fact.
func TestPublishSingleModePublishesRelease(t *testing.T) {
	var published bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/server-info":
			w.Write([]byte(`{"max_direct_upload_bytes":99614720,"upload_sessions":true}`))
		case "POST /api/v1/projects/myapp/releases":
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"version":"v1"}`))
		case "PUT /api/v1/projects/myapp/releases/v1/artifacts/linux/amd64":
			if published {
				t.Error("artifact uploaded after the release was published")
			}
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"sha256":"deadbeef"}`))
		case "POST /api/v1/projects/myapp/releases/v1/publish":
			published = true
			w.Write([]byte(`{"published":true}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	artifact := filepath.Join(t.TempDir(), "artifact")
	if err := os.WriteFile(artifact, []byte("artifact bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	for flag, value := range map[string]string{
		"server":   srv.URL,
		"token":    "test-token",
		"project":  "myapp",
		"os":       "linux",
		"arch":     "amd64",
		"artifact": artifact,
	} {
		if err := publishCmd.Flags().Set(flag, value); err != nil {
			t.Fatalf("set --%s: %v", flag, err)
		}
	}

	if err := runPublish(publishCmd, nil); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if !published {
		t.Fatal("single-mode publish never called POST .../publish; the release would stay unpublished")
	}
}
