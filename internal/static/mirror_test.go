package static

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

type fakeMirror struct{ sum, name string }

func (f *fakeMirror) ServeMirrored(w http.ResponseWriter, r *http.Request, sum, name string) {
	f.sum, f.name = sum, name
	w.WriteHeader(http.StatusOK)
}

func TestFileSendsAMirrorRequestToItsSourceWithoutAProject(t *testing.T) {
	src := &fakeMirror{}
	RegisterMirror("test.example", src)
	projectCalls := 0
	h := fileHandler(func(w http.ResponseWriter, r *http.Request) { projectCalls++ })
	sum := strings.Repeat("ab", 32)

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/file?mirror=test.example&name=a.zip&sha256="+sum, nil))
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, sum, src.sum)
	assert.Equal(t, "a.zip", src.name)
	assert.Zero(t, projectCalls, "a mirror request names no project, so the project check would 404 it")

	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/file?project=p&v=1&os=linux&arch=amd64", nil))
	assert.Equal(t, 1, projectCalls, "every other request still goes through the project check")
}

func TestMirrorQueryIsCanonical(t *testing.T) {
	RegisterMirror("test.example", &fakeMirror{})
	sum := strings.Repeat("ab", 32)
	rec := httptest.NewRecorder()
	serveMirror(rec, httptest.NewRequest(http.MethodGet, "/file?sha256="+sum+"&name=a.zip&mirror=test.example&junk=1", nil))
	assert.Equal(t, http.StatusMovedPermanently, rec.Code, "one asset must have one URL, or the CDN keeps several copies")
	assert.Equal(t, "/file?mirror=test.example&name=a.zip&sha256="+sum, rec.Header().Get("Location"))
}

func TestMirrorRequestThatNamesNothingIs404(t *testing.T) {
	RegisterMirror("test.example", &fakeMirror{})
	sum := strings.Repeat("ab", 32)
	for _, q := range []string{
		"mirror=nosuch&name=a.zip&sha256=" + sum,
		"mirror=test.example&name=a.zip&sha256=ABC",
		"mirror=test.example&name=a.zip",
		"mirror=test.example&name=..&sha256=" + sum,
		"mirror=test.example&sha256=" + sum,
	} {
		rec := httptest.NewRecorder()
		serveMirror(rec, httptest.NewRequest(http.MethodGet, "/file?"+q, nil))
		assert.Equal(t, http.StatusNotFound, rec.Code, q)
		assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"), q)
	}
}
