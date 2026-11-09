package admin

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
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

func TestAPISettingsAppKey(t *testing.T) {
	t.Serial()
	t.Setenv("BUILDHOST_PRIMARY_DOMAIN", "example.com")
	srv, _ := newTestServer(t)
	config.SetStored(nil)
	t.Cleanup(func() { config.SetStored(nil) })

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	good := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	put := func(v string) *httptest.ResponseRecorder {
		b, _ := json.Marshal(map[string]string{"value": v})
		return serve(srv, http.MethodPut, "/api/settings/"+config.GitHubAppKeySetting, bytes.NewBuffer(b))
	}

	w := put(good)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.NotContains(t, w.Body.String(), "PRIVATE KEY", "the key never goes back to the browser")
	row := settingsByName(t, w.Body.Bytes())[config.GitHubAppKeySetting]
	assert.Equal(t, config.KeyFingerprint(key), row.Fingerprint)
	assert.Empty(t, row.Problem)

	w = put("-----BEGIN RSA PRIVATE KEY-----garbage-----END RSA PRIVATE KEY-----")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "not an RSA private key")
	assert.Equal(t, good, config.Getenv(config.GitHubAppKeySetting), "a refused key leaves the stored key as it was")
}

func TestAPISettingsStoresAnEmptyOverride(t *testing.T) {
	t.Serial()
	t.Setenv("BUILDHOST_PRIMARY_DOMAIN", "example.com")
	t.Setenv("BUILDHOST_OTEL_ENDPOINT", "http://collector:4318")
	srv, _ := newTestServer(t)
	config.SetStored(nil)
	t.Cleanup(func() { config.SetStored(nil) })

	w := serve(srv, http.MethodPut, "/api/settings/BUILDHOST_OTEL_ENDPOINT", bytes.NewBufferString(`{"value":""}`))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	row := settingsByName(t, w.Body.Bytes())["BUILDHOST_OTEL_ENDPOINT"]
	assert.Equal(t, "dashboard", row.Source, "an empty save overrides the environment")
	assert.Equal(t, "", row.Value)
	assert.Equal(t, "", config.Getenv("BUILDHOST_OTEL_ENDPOINT"))

	w = serve(srv, http.MethodDelete, "/api/settings/BUILDHOST_OTEL_ENDPOINT", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "http://collector:4318", settingsByName(t, w.Body.Bytes())["BUILDHOST_OTEL_ENDPOINT"].Value, "Clear falls back to the environment")
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
