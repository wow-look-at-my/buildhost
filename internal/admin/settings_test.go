package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/config"
)

func settingsByName(t *testing.T, body []byte) map[string]settingRow {
	t.Helper()
	var resp struct {
		Settings []settingRow `json:"settings"`
	}
	require.NoError(t, json.Unmarshal(body, &resp))
	m := map[string]settingRow{}
	for _, s := range resp.Settings {
		m[s.Name] = s
	}
	return m
}

func TestAPISettings(t *testing.T) {
	t.Serial()
	t.Setenv("BUILDHOST_PRIMARY_DOMAIN", "example.com")
	t.Setenv("BUILDHOST_RUN_FILES_KEEP", "48h")
	srv, _ := newTestServer(t)
	config.SetStored(nil)
	t.Cleanup(func() { config.SetStored(nil) })

	w := serve(srv, http.MethodGet, "/api/settings", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	rows := settingsByName(t, w.Body.Bytes())
	assert.Len(t, rows, len(config.Settings), "the page lists every setting")
	assert.Equal(t, "environment", rows["BUILDHOST_RUN_FILES_KEEP"].Source)
	assert.Equal(t, "48h", rows["BUILDHOST_RUN_FILES_KEEP"].Value)

	w = serve(srv, http.MethodPut, "/api/settings/BUILDHOST_RUN_FILES_KEEP", bytes.NewBufferString(`{"value":"6h"}`))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	rows = settingsByName(t, w.Body.Bytes())
	assert.Equal(t, "dashboard", rows["BUILDHOST_RUN_FILES_KEEP"].Source, "a stored value takes precedence over the environment")
	assert.Equal(t, "6h", rows["BUILDHOST_RUN_FILES_KEEP"].Value)
	assert.Equal(t, "6h", config.Getenv("BUILDHOST_RUN_FILES_KEEP"))

	w = serve(srv, http.MethodPut, "/api/settings/BUILDHOST_SECRET_SERVER_TOKEN", bytes.NewBufferString(`{"value":"sst_abc"}`))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.NotContains(t, w.Body.String(), "sst_abc", "a secret never goes back to the browser")
	assert.True(t, settingsByName(t, w.Body.Bytes())["BUILDHOST_SECRET_SERVER_TOKEN"].Set)

	for _, tc := range []struct{ name, body, want string }{
		{"BUILDHOST_RUN_FILES_KEEP", `{"value":"soon"}`, "duration"},
		{"BUILDHOST_SECRET_SERVER_TOKEN", `{"value":"ghp_x"}`, "sst_"},
		{"BUILDHOST_DATA_DIR", `{"value":"/tmp"}`, "environment only"},
	} {
		w = serve(srv, http.MethodPut, "/api/settings/"+tc.name, bytes.NewBufferString(tc.body))
		assert.Equal(t, http.StatusBadRequest, w.Code, tc.name)
		assert.Contains(t, w.Body.String(), tc.want, tc.name)
	}
	w = serve(srv, http.MethodPut, "/api/settings/NOPE", bytes.NewBufferString(`{"value":"x"}`))
	assert.Equal(t, http.StatusNotFound, w.Code)

	w = serve(srv, http.MethodDelete, "/api/settings/BUILDHOST_RUN_FILES_KEEP", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "48h", settingsByName(t, w.Body.Bytes())["BUILDHOST_RUN_FILES_KEEP"].Value, "clearing falls back to the environment")
}

func TestAPISettingsRefusesAConfigThatCannotStart(t *testing.T) {
	t.Serial()
	t.Setenv("BUILDHOST_PRIMARY_DOMAIN", "")
	srv, _ := newTestServer(t)
	config.SetStored(nil)
	t.Cleanup(func() { config.SetStored(nil) })

	w := serve(srv, http.MethodPut, "/api/settings/BUILDHOST_PRIMARY_DOMAIN", bytes.NewBufferString(`{"value":"example.com"}`))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	w = serve(srv, http.MethodDelete, "/api/settings/BUILDHOST_PRIMARY_DOMAIN", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code, "without the stored domain the server would not start")
	assert.Contains(t, w.Body.String(), "BUILDHOST_PRIMARY_DOMAIN is required")
	assert.Equal(t, "example.com", config.Getenv("BUILDHOST_PRIMARY_DOMAIN"), "a refused change leaves the stored values as they were")
}

func TestAPIRestart(t *testing.T) {
	t.Serial()
	srv, _ := newTestServer(t)
	w := serve(srv, http.MethodPost, "/api/restart", nil)
	assert.Equal(t, http.StatusConflict, w.Code, "no restart hook, no restart")

	called := make(chan struct{})
	srv.SetRestart(func() { close(called) })
	w = serve(srv, http.MethodPost, "/api/restart", nil)
	assert.Equal(t, http.StatusAccepted, w.Code)
	<-called
}
