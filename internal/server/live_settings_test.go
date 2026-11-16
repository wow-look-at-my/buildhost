package server_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/admin"
	"github.com/wow-look-at-my/buildhost/internal/config"
)

// A setting saved on the admin port changes what the main port serves, in the
// same process.
func TestSavedSettingAppliesWithNoRestart(t *testing.T) {
	t.Serial()
	const first, second = "live-first.example", "live-second.example"
	t.Setenv("BUILDHOST_PRIMARY_DOMAIN", first)
	config.SetStored(nil)
	t.Cleanup(func() { config.SetStored(nil) })
	env := setupWith(t, func(c *config.Config) { c.PrimaryDomain = first })
	dash := admin.New(env.cfg, env.database, env.store, admin.BuildInfo{}).NewHTTPServer().Handler

	status := func(host string) int {
		resp, _ := siteGet(t, env, host, "/api/v1/projects")
		return resp.StatusCode
	}
	require.Equal(t, http.StatusOK, status(first))
	require.Equal(t, http.StatusNotFound, status(second))

	save := func(method, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		dash.ServeHTTP(w, httptest.NewRequest(method, "/api/settings/BUILDHOST_PRIMARY_DOMAIN", bytes.NewBufferString(body)))
		return w
	}
	w := save(http.MethodPut, `{"value":"`+second+`"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, http.StatusOK, status(second), "the saved apex serves at once")
	assert.Equal(t, http.StatusNotFound, status(first), "the old apex stops at once")

	w = save(http.MethodDelete, "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, http.StatusOK, status(first), "Clear puts the environment value back in use at once")
	assert.Equal(t, http.StatusNotFound, status(second))
}
