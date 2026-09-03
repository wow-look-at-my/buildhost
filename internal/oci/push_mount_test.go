package oci

// Cross-repository blob mount (POST /v2/{name}/blobs/uploads/?mount=): storage
// is content-addressed and global, so a blob another project already links is a
// row away, not an upload away. What these pin is the authorization: the mount

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

// mountBlobRequest issues the mount POST against target, optionally naming a
// source project and carrying a token.
func mountBlobRequest(t *testing.T, h *Handler, target *db.Project, digest, from string, token *db.APIToken) *httptest.ResponseRecorder {
	t.Helper()
	url := "/v2/" + target.Name + "/blobs/uploads/?mount=" + digest
	if from != "" {
		url += "&from=" + from
	}
	req := httptest.NewRequest("POST", url, nil)
	req = withRoute(req, target, route{project: target.Name, action: "uploads"})
	if token != nil {
		req = req.WithContext(auth.WithToken(req.Context(), token))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// storeLinkedBlob puts content in storage and links it to owner, the way a push
// to that project would have.
func storeLinkedBlob(t *testing.T, h *Handler, owner *db.Project, content string) string {
	t.Helper()
	key, size, err := h.Store.Put(t.Context(), strings.NewReader(content))
	require.NoError(t, err)
	require.NoError(t, h.DB.LinkOCIBlob(t.Context(), owner.ID, key, "application/vnd.oci.image.layer.v1.tar+zstd", size, false))
	return "sha256:" + key
}

func TestMountBlob_LinksABlobAnotherReadableProjectAlreadyHas(t *testing.T) {
	t.Serial()
	h, d, _ := setupTest(t)
	base := &db.Project{Name: "agent-host-session", Versioning: db.VersioningAuto}
	child := &db.Project{Name: "agent-host-claude", Versioning: db.VersioningAuto}
	require.NoError(t, d.CreateProject(t.Context(), base))
	require.NoError(t, d.CreateProject(t.Context(), child))
	digest := storeLinkedBlob(t, h, base, "the shared base layer")

	rec := mountBlobRequest(t, h, child, digest, "", nil)

	require.Equal(t, http.StatusCreated, rec.Code, "a readable owner must be enough to mount")
	assert.Equal(t, digest, rec.Header().Get("Docker-Content-Digest"))
	assert.Equal(t, "/v2/"+child.Name+"/blobs/"+digest, rec.Header().Get("Location"))

	link, err := d.GetOCIBlobLink(t.Context(), child.ID, digest[7:])
	require.NoError(t, err, "the mount must leave the child linked to the blob")
	assert.Equal(t, "application/vnd.oci.image.layer.v1.tar+zstd", link.MediaType, "the descriptor is copied from the owner")
	assert.Equal(t, int64(len("the shared base layer")), link.Size)
}

// The digest of a private image's layer is not a secret worth relying on, so
func TestMountBlob_RefusesAPrivateOwnerTheCallerCannotRead(t *testing.T) {
	t.Serial()
	h, d, _ := setupTest(t)
	secret := &db.Project{Name: "someone-elses", Versioning: db.VersioningAuto, IsPrivate: true}
	mine := &db.Project{Name: "mine", Versioning: db.VersioningAuto}
	require.NoError(t, d.CreateProject(t.Context(), secret))
	require.NoError(t, d.CreateProject(t.Context(), mine))
	digest := storeLinkedBlob(t, h, secret, "private bytes")

	rec := mountBlobRequest(t, h, mine, digest, "", nil)

	require.Equal(t, http.StatusAccepted, rec.Code, "an unmountable request falls back to an upload session")
	assert.NotEmpty(t, rec.Header().Get("Docker-Upload-UUID"))
	_, err := d.GetOCIBlobLink(t.Context(), mine.ID, digest[7:])
	assert.ErrorIs(t, err, db.ErrNotFound, "nothing may be linked into the target")
}

// A token scoped to the private owner may read it, so it may mount from it --
// exactly what it could get by pulling.
func TestMountBlob_AllowsAPrivateOwnerTheTokenCanRead(t *testing.T) {
	t.Serial()
	h, d, _ := setupTest(t)
	owner := &db.Project{Name: "owner", Versioning: db.VersioningAuto, IsPrivate: true}
	target := &db.Project{Name: "target", Versioning: db.VersioningAuto}
	require.NoError(t, d.CreateProject(t.Context(), owner))
	require.NoError(t, d.CreateProject(t.Context(), target))
	digest := storeLinkedBlob(t, h, owner, "readable private bytes")

	rec := mountBlobRequest(t, h, target, digest, "", &db.APIToken{Scopes: "read,write"})

	require.Equal(t, http.StatusCreated, rec.Code)
	_, err := d.GetOCIBlobLink(t.Context(), target.ID, digest[7:])
	assert.NoError(t, err)
}

// `from` narrows the search rather than widening it: naming a project that does
func TestMountBlob_HonoursFromAsARestriction(t *testing.T) {
	t.Serial()
	h, d, _ := setupTest(t)
	owner := &db.Project{Name: "real-owner", Versioning: db.VersioningAuto}
	other := &db.Project{Name: "not-the-owner", Versioning: db.VersioningAuto}
	target := &db.Project{Name: "target", Versioning: db.VersioningAuto}
	for _, p := range []*db.Project{owner, other, target} {
		require.NoError(t, d.CreateProject(t.Context(), p))
	}
	digest := storeLinkedBlob(t, h, owner, "layer bytes")

	rec := mountBlobRequest(t, h, target, digest, other.Name, nil)
	require.Equal(t, http.StatusAccepted, rec.Code, "the named project does not have it, so no mount")

	rec = mountBlobRequest(t, h, target, digest, owner.Name, nil)
	require.Equal(t, http.StatusCreated, rec.Code, "the named project does have it")
}

// A link row whose bytes retention has since collected must not be mountable:
// the mount would produce a project pointing at a blob no pull can serve.
func TestMountBlob_RefusesWhenStorageNoLongerHasTheBytes(t *testing.T) {
	t.Serial()
	h, d, store := setupTest(t)
	owner := &db.Project{Name: "owner", Versioning: db.VersioningAuto}
	target := &db.Project{Name: "target", Versioning: db.VersioningAuto}
	require.NoError(t, d.CreateProject(t.Context(), owner))
	require.NoError(t, d.CreateProject(t.Context(), target))
	digest := storeLinkedBlob(t, h, owner, "evicted bytes")
	require.NoError(t, store.Delete(t.Context(), digest[7:]))

	rec := mountBlobRequest(t, h, target, digest, "", nil)

	require.Equal(t, http.StatusAccepted, rec.Code)
	_, err := d.GetOCIBlobLink(t.Context(), target.ID, digest[7:])
	assert.ErrorIs(t, err, db.ErrNotFound)
}

func TestMountBlob_UnknownDigestFallsBackToAnUploadSession(t *testing.T) {
	t.Serial()
	h, d, _ := setupTest(t)
	target := &db.Project{Name: "target", Versioning: db.VersioningAuto}
	require.NoError(t, d.CreateProject(t.Context(), target))

	for _, digest := range []string{
		"sha256:" + strings.Repeat("ab", 32),
		"not-a-digest",
	} {
		rec := mountBlobRequest(t, h, target, digest, "", nil)
		require.Equal(t, http.StatusAccepted, rec.Code, "digest %q", digest)
		assert.NotEmpty(t, rec.Header().Get("Docker-Upload-UUID"), "digest %q", digest)
	}
}
