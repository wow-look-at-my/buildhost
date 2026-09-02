package server_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/server"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

func signJWT(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": kid})
	payload, _ := json.Marshal(claims)
	content := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	hash := sha256.Sum256([]byte(content))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
	require.NoError(t, err)
	return content + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// jsonDoc renders a request body from its fields. Marshalling is what keeps a
// quote or a backslash in a value from breaking the document.
func jsonDoc(t *testing.T, fields map[string]any) string {
	t.Helper()
	doc, err := json.Marshal(fields)
	require.NoError(t, err)
	return string(doc)
}

func jwksServer(t *testing.T, pub *rsa.PublicKey, kid string) *httptest.Server {
	t.Helper()
	n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString([]byte{1, 0, 1})
	// Marshalled, not formatted: a quote or a backslash in a value would break a formatted document.
	jwksBody, err := json.Marshal(map[string]any{
		"keys": []map[string]string{{"kty": "RSA", "kid": kid, "n": n, "e": e}},
	})
	require.NoError(t, err)

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/.well-known/openid-configuration" {
			discovery, err := json.Marshal(map[string]string{"jwks_uri": srv.URL + "/.well-known/jwks"})
			require.NoError(t, err)
			w.Write(discovery)
			return
		}
		w.Write(jwksBody)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestOIDC_AutoCreateProject(t *testing.T) {
	dbDir := t.TempDir()
	storeDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "test.db")
	database, err := db.Open(dbPath)
	require.Nil(t, err)
	t.Cleanup(func() { database.Close() })

	store, err := storage.NewFilesystem(storeDir, true)
	require.Nil(t, err)

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	jwksSrv := jwksServer(t, &key.PublicKey, "kid-auto")

	cfg := config.Config{
		ListenAddr:  ":0",
		DataDir:     dbDir,
		DBPath:      dbPath,
		OIDCIssuers: []string{jwksSrv.URL},
		OIDCOrgs:    []string{"*"},
		OIDCEvents:  []string{"push"},
	}

	srv := server.New(cfg, database, store)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	token := signJWT(t, key, "kid-auto", map[string]any{
		"iss":        jwksSrv.URL,
		"sub":        "repo:myorg/autoproject:ref:refs/heads/main",
		"event_name": "push",
		"aud":        "https://buildhost.example.com",
		"exp":        time.Now().Add(10 * time.Minute).Unix(),
		"iat":        time.Now().Unix(),
	})

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/projects/autoproject/releases", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusCreated, resp.StatusCode)

	proj, err := database.GetProject(context.Background(), "autoproject")
	require.NoError(t, err)
	require.Equal(t, "autoproject", proj.Name)
}
