package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminCreateDownloadLink_Success(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	body := bytes.NewBufferString(`{"os":"linux","arch":"amd64","version":"1.0.0"}`)
	w := serve(srv, http.MethodPost, "/api/projects/testproject/download-links", body)
	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	url, _ := resp["url"].(string)
	tok, _ := resp["token"].(string)
	assert.Contains(t, url, "/file?")
	assert.Contains(t, url, "project=testproject")
	assert.Contains(t, url, "token=bhdl_")
	assert.True(t, strings.HasPrefix(tok, "bhdl_"))
	assert.NotNil(t, resp["expires_at"])
}

func TestAdminCreateDownloadLink_ProjectNotFound(t *testing.T) {
	srv, _ := newTestServer(t)
	w := serve(srv, http.MethodPost, "/api/projects/ghost/download-links", bytes.NewBufferString(`{"os":"linux","arch":"amd64","version":"1"}`))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAdminCreateDownloadLink_MissingFields(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)
	w := serve(srv, http.MethodPost, "/api/projects/testproject/download-links", bytes.NewBufferString(`{"os":"linux"}`))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAdminCreateDownloadLink_RejectsAny(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)
	w := serve(srv, http.MethodPost, "/api/projects/testproject/download-links", bytes.NewBufferString(`{"os":"any","arch":"any","version":"1.0.0"}`))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAdminCreateDownloadLink_ArtifactNotFound(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)
	w := serve(srv, http.MethodPost, "/api/projects/testproject/download-links", bytes.NewBufferString(`{"os":"darwin","arch":"arm64","version":"1.0.0"}`))
	assert.Equal(t, http.StatusNotFound, w.Code)
}
