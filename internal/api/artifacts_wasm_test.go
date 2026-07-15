package api

// Tests for WebAssembly artifact uploads: the platform identifier is os=wasm,
// with arch distinguishing the Go wasm flavor (js for GOOS=js, wasip1 for
// GOOS=wasip1). os=wasm pairs only with those arches and vice versa.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

func TestUploadArtifact_WasmJS(t *testing.T) {
	h, proj, rel := setupUploadTest(t, "wasmjs")

	rec := doUpload(t, h, proj, "wasm", "js", "", "\x00asm-fake-module")
	require.Equal(t, http.StatusCreated, rec.Code)

	body := strings.TrimSpace(rec.Body.String())
	assert.True(t, strings.HasPrefix(body, "{"), "single combination must stay a JSON object")

	var a db.Artifact
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &a))
	assert.Equal(t, db.OSWasm, a.OS)
	assert.Equal(t, db.ArchJS, a.Arch)

	rows, err := h.DB.ListArtifacts(context.Background(), rel.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 1)
}

// Both Go wasm flavors publish under os=wasm in one request via the comma
// list, sharing the blob like any other fan-out.
func TestUploadArtifact_WasmFlavorFanOut(t *testing.T) {
	h, proj, rel := setupUploadTest(t, "wasmfanout")

	rec := doUpload(t, h, proj, "wasm", "js,wasip1", "", "\x00asm-fake-module")
	require.Equal(t, http.StatusCreated, rec.Code)

	var got []db.Artifact
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, []string{"wasm/js", "wasm/wasip1"}, platformsOf(got))
	assert.Equal(t, got[0].StorageKey, got[1].StorageKey)

	rows, err := h.DB.ListArtifacts(context.Background(), rel.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 2)
}

// os=wasm pairs only with the wasm flavor arches, and those arches only with
// os=wasm -- every incompatible combination is a 400 that creates nothing,
// including via the any/all arch alias and the cosmo os alias (neither alias
// includes wasm: "any" means native desktop platforms).
func TestUploadArtifact_WasmIncompatiblePairs(t *testing.T) {
	h, proj, rel := setupUploadTest(t, "wasmbadpair")

	for _, c := range [][2]string{
		{"wasm", "amd64"},    // wasm needs a wasm flavor arch
		{"wasm", "any"},      // arch alias expands to amd64+arm64, never js/wasip1
		{"linux", "js"},      // wasm flavor arch needs os=wasm
		{"cosmo", "wasip1"},  // os alias expands to linux+darwin+windows, never wasm
		{"linux,wasm", "js"}, // list mixing native and wasm cannot satisfy both
	} {
		rec := doUpload(t, h, proj, c[0], c[1], "", "bin")
		assert.Equalf(t, http.StatusBadRequest, rec.Code, "%s/%s must be rejected", c[0], c[1])
		assert.Contains(t, rec.Body.String(), "incompatible os/arch pair")
	}

	rows, err := h.DB.ListArtifacts(context.Background(), rel.ID)
	require.NoError(t, err)
	assert.Empty(t, rows, "rejected pairs must create nothing")
}
