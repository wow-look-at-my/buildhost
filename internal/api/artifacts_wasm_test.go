package api

// Tests for WebAssembly artifact uploads: the platform identifier is os=wasm,
// with arch distinguishing the Go wasm flavor (js for GOOS=js, wasip1 for

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

func TestUploadArtifact_LegacyGoosGoarchPair(t *testing.T) {
	h, proj, rel := setupUploadTest(t, "wasmlegacy")

	rec := doUpload(t, h, proj, "js", "wasm", "", "\x00asm-js-module")
	require.Equal(t, http.StatusCreated, rec.Code)

	var a db.Artifact
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &a))
	assert.Equal(t, db.OSWasm, a.OS)
	assert.Equal(t, db.ArchJS, a.Arch)
	assert.NotContains(t, rec.Body.String(), `"os":"js"`, "js must never surface as an os")

	rec = doUpload(t, h, proj, "wasip1", "wasm", "", "\x00asm-wasip1-module")
	require.Equal(t, http.StatusCreated, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &a))
	assert.Equal(t, db.OSWasm, a.OS)
	assert.Equal(t, db.ArchWasip1, a.Arch)

	// The stored rows are canonical: os=wasm only, never os=js/wasip1.
	rows, err := h.DB.ListArtifacts(context.Background(), rel.ID)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, row := range rows {
		assert.Equal(t, db.OSWasm, row.OS, "legacy pair must be stored under os=wasm")
		assert.Contains(t, []db.Arch{db.ArchJS, db.ArchWasip1}, row.Arch)
	}

	// The alias maps onto the SAME canonical row identity: re-uploading the
	rec = doUpload(t, h, proj, "wasm", "js", "", "\x00asm-js-module")
	assert.Equal(t, http.StatusConflict, rec.Code)

	// The shim is pair-level only. os=js with any other arch stays invalid,
	rec = doUpload(t, h, proj, "js", "amd64", "", "bin")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), `invalid os \"js\"`)

	rec = doUpload(t, h, proj, "linux", "wasm", "", "bin")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), `invalid arch \"wasm\"`)
}

// os=wasm pairs only with the wasm flavor arches, and those arches only with
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
