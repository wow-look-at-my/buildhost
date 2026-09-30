package goproxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sumdbRequest(t *testing.T, name, rest string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/sumdb/"+name+"/"+rest, nil)
	r.SetPathValue("name", name)
	r.SetPathValue("rest", rest)
	w := httptest.NewRecorder()
	serveSumDB(w, r)
	return w
}

func TestSumDBMirrorForwardsLookupsAndTiles(t *testing.T) {
	var seen []string
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "answer for "+r.URL.Path)
	}))
	defer origin.Close()
	prev := sumdbOrigin
	sumdbOrigin = origin.URL
	defer func() { sumdbOrigin = prev }()

	for _, rest := range []string{"lookup/github.com/spf13/cobra@v1.10.2", "tile/8/0/001", "latest"} {
		w := sumdbRequest(t, sumdbName, rest)
		require.Equal(t, http.StatusOK, w.Code, rest)
		assert.Equal(t, "answer for /"+rest, w.Body.String())
		assert.Equal(t, "text/plain; charset=utf-8", w.Header().Get("Content-Type"))
	}
	assert.Len(t, seen, 3)
}

func TestSumDBMirrorAnswersSupportedWithoutAnUpstreamCall(t *testing.T) {
	prev := sumdbOrigin
	sumdbOrigin = "http://127.0.0.1:1"
	defer func() { sumdbOrigin = prev }()

	assert.Equal(t, http.StatusOK, sumdbRequest(t, sumdbName, "supported").Code)
}

func TestSumDBMirrorRefusesOtherDatabasesAndPaths(t *testing.T) {
	w := sumdbRequest(t, "sum.example.com", "supported")
	assert.Equal(t, http.StatusNotFound, w.Code, "a database this proxy does not mirror must 404, so the go command connects to it directly")

	w = sumdbRequest(t, sumdbName, "admin/anything")
	assert.Equal(t, http.StatusNotFound, w.Code, "only the checksum database endpoints are forwarded")
}

func TestSumDBMirrorPassesAnUpstreamErrorThrough(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found: github.com/x/y@v9.9.9", http.StatusNotFound)
	}))
	defer origin.Close()
	prev := sumdbOrigin
	sumdbOrigin = origin.URL
	defer func() { sumdbOrigin = prev }()

	w := sumdbRequest(t, sumdbName, "lookup/github.com/x/y@v9.9.9")
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "not found: github.com/x/y@v9.9.9")
}

func TestSumDBMirrorReportsAnUnreachableOriginAs502(t *testing.T) {
	prev := sumdbOrigin
	sumdbOrigin = "http://127.0.0.1:1"
	defer func() { sumdbOrigin = prev }()

	w := sumdbRequest(t, sumdbName, "latest")
	assert.Equal(t, http.StatusBadGateway, w.Code)
	assert.Contains(t, w.Body.String(), sumdbName+" did not answer")
}
