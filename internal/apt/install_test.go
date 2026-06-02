package apt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestParseRoute_InstallScript(t *testing.T) {
	tests := []struct {
		name string
		path string
		want route
	}{
		{
			name: "install.sh, single-segment name",
			path: "/myapp/install.sh",
			want: route{project: "myapp", subPath: "install.sh"},
		},
		{
			name: "install.sh, multi-segment name",
			path: "/team/tool/install.sh",
			want: route{project: "team/tool", subPath: "install.sh"},
		},
		{
			name: "key.asc still parses",
			path: "/myapp/key.asc",
			want: route{project: "myapp", subPath: "key.asc"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.path, nil)
			got := parseRoute(req).(route)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestServeInstallScript(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningAuto}
	require.NoError(t, d.CreateProject(ctx, proj))

	req := httptest.NewRequest("GET", "/myapp/install.sh", nil)
	req.Host = "apt.pazer.build"
	req = withRoute(req, proj, route{project: "myapp", subPath: "install.sh"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "shellscript")

	body := rec.Body.String()
	// Self-referential, subdomain-correct URL derived from the request Host.
	assert.Contains(t, body, "APT_URL='https://apt.pazer.build/myapp'")
	assert.Contains(t, body, "PROJECT='myapp'")
	// Signed-by setup using the armored key served at key.asc (no gpg needed).
	assert.Contains(t, body, "$APT_URL/key.asc")
	assert.Contains(t, body, "deb [signed-by=$KEYRING] $APT_URL stable main")
	assert.Contains(t, body, "/etc/apt/keyrings/buildhost-myapp.asc")
	assert.Contains(t, body, "/etc/apt/sources.list.d/buildhost-myapp.list")
	assert.Contains(t, body, "sudo apt-get install $PROJECT")
	// Private-repo token support is wired in.
	assert.Contains(t, body, "BUILDHOST_TOKEN")
}

func TestServeInstallScript_NamespacedSlug(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "team/tool", Versioning: db.VersioningAuto}
	require.NoError(t, d.CreateProject(ctx, proj))

	req := httptest.NewRequest("GET", "/team/tool/install.sh", nil)
	req.Host = "apt.pazer.build"
	req = withRoute(req, proj, route{project: "team/tool", subPath: "install.sh"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	// Slashes are preserved in the URL but sanitized in local file names.
	assert.Contains(t, body, "APT_URL='https://apt.pazer.build/team/tool'")
	assert.Contains(t, body, "/etc/apt/keyrings/buildhost-team-tool.asc")
	assert.Contains(t, body, "/etc/apt/sources.list.d/buildhost-team-tool.list")
	assert.Contains(t, body, "/etc/apt/auth.conf.d/buildhost-team-tool.conf")
}
