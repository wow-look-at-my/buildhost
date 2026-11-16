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
	"strings"
	"testing"
	"time"

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
	assert.Equal(t, strings.TrimSpace(good), config.Getenv(config.GitHubAppKeySetting), "a refused key leaves the stored key as it was")
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

func TestAPISettingsApplyWithNoRestart(t *testing.T) {
	t.Serial()
	t.Setenv("BUILDHOST_PRIMARY_DOMAIN", "example.com")
	srv, _ := newTestServer(t)
	config.SetStored(nil)
	t.Cleanup(func() { config.SetStored(nil) })
	var seen []config.Config
	config.OnChange(func(c config.Config) { seen = append(seen, c) })

	w := serve(srv, http.MethodPut, "/api/settings/BUILDHOST_MIRROR_KEEP", bytes.NewBufferString(`{"value":"3h"}`))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Len(t, seen, 1, "a save gives the new configuration to the running server")
	assert.Equal(t, 3*time.Hour, seen[0].MirrorKeep)
	assert.Equal(t, 3*time.Hour, config.Current().MirrorKeep)
	assert.Equal(t, 3*time.Hour, srv.conf().MirrorKeep, "the dashboard pages read the saved value")
	assert.NotContains(t, w.Body.String(), "restart_pending")

	w = serve(srv, http.MethodPut, "/api/settings/BUILDHOST_MIRROR_KEEP", bytes.NewBufferString(`{"value":"never"}`))
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Len(t, seen, 1, "a refused save applies nothing")

	w = serve(srv, http.MethodDelete, "/api/settings/BUILDHOST_MIRROR_KEEP", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Len(t, seen, 2)
	assert.Equal(t, 7*24*time.Hour, config.Current().MirrorKeep, "Clear puts the default back in use")
}

func TestAPIValidateSetting(t *testing.T) {
	t.Serial()
	t.Setenv("BUILDHOST_PRIMARY_DOMAIN", "example.com")
	t.Setenv("BUILDHOST_GITHUB_APP_ID", "")
	srv, database := newTestServer(t)
	config.SetStored(nil)
	t.Cleanup(func() { config.SetStored(nil) })
	type result struct {
		OK      bool   `json:"ok"`
		Error   string `json:"error"`
		Warning string `json:"warning"`
	}
	check := func(name, body string) result {
		t.Helper()
		w := serve(srv, http.MethodPost, "/api/settings/"+name+"/validate", bytes.NewBufferString(body))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var r result
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &r))
		return r
	}

	assert.Equal(t, result{OK: true}, check("BUILDHOST_RUN_FILES_KEEP", `{"value":" 6h "}`))
	r := check("BUILDHOST_RUN_FILES_KEEP", `{"value":"soon"}`)
	assert.False(t, r.OK)
	assert.Contains(t, r.Error, "duration")
	r = check("BUILDHOST_PRIMARY_DOMAIN", `{"value":"https://example.com"}`)
	assert.Contains(t, r.Error, "not a domain name")
	r = check("BUILDHOST_PRIMARY_DOMAIN", `{"value":""}`)
	assert.Contains(t, r.Error, "BUILDHOST_PRIMARY_DOMAIN is required", "the check covers the whole configuration after the change")
	r = check("BUILDHOST_SECRET_SERVER_TOKEN", `{"value":"ghp_x"}`)
	assert.Contains(t, r.Error, "sst_")
	r = check("BUILDHOST_DATA_DIR", `{"value":"/tmp"}`)
	assert.Contains(t, r.Error, "environment only")
	r = check(config.GitHubAppKeySetting, `{"value":"-----BEGIN RSA PRIVATE KEY-----garbage-----END RSA PRIVATE KEY-----"}`)
	assert.Contains(t, r.Error, "not an RSA private key")
	r = check("BUILDHOST_GITHUB_APP_ID", `{"value":"12345"}`)
	assert.True(t, r.OK)
	assert.Contains(t, r.Warning, "BUILDHOST_GITHUB_APP_PRIVATE_KEY is empty")
	assert.Equal(t, result{OK: true}, check("BUILDHOST_RUN_FILES_KEEP", `{"value":null}`), "a null value checks a Clear")

	w := serve(srv, http.MethodPost, "/api/settings/NOPE/validate", bytes.NewBufferString(`{"value":"x"}`))
	assert.Equal(t, http.StatusNotFound, w.Code)
	w = serve(srv, http.MethodPost, "/api/settings/BUILDHOST_RUN_FILES_KEEP/validate", bytes.NewBufferString(`not json`))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	stored, err := database.ServerSettings(t.Context())
	require.NoError(t, err)
	assert.Empty(t, stored, "a check stores nothing")
	assert.Empty(t, config.Getenv("BUILDHOST_RUN_FILES_KEEP"), "a check applies nothing")
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
