package uploads

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBodyEmptyReadsAnUnknownLength: a finalize that reaches the server with
// no stated length, as a proxy can forward it, is empty when nothing can be
// read. Only bytes that arrive make it a body.
func TestBodyEmptyReadsAnUnknownLength(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		length int64
		want   bool
	}{
		{"stated zero", "", 0, true},
		{"unknown and empty", "", -1, true},
		{"unknown with bytes", "x", -1, false},
		{"stated bytes", "abc", 3, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, "/x", strings.NewReader(tc.body))
			req.ContentLength = tc.length
			assert.Equal(t, tc.want, bodyEmpty(req))
		})
	}
	req := httptest.NewRequest(http.MethodPut, "/x", nil)
	req.Body = http.NoBody
	assert.True(t, bodyEmpty(req), "http.NoBody is empty")
}
