package admin

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrackInflight_WriteMethods(t *testing.T) {
	atomic.StoreInt64(&inflightWrites, 0)

	var seen int64
	blocked := make(chan struct{})
	release := make(chan struct{})

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.StoreInt64(&seen, atomic.LoadInt64(&inflightWrites))
		blocked <- struct{}{}
		<-release
		w.WriteHeader(http.StatusOK)
	})
	handler := TrackInflight(inner)

	const n = 5
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPut, "/upload", nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
		}()
	}

	for i := 0; i < n; i++ {
		<-blocked
	}
	assert.Equal(t, int64(n), atomic.LoadInt64(&inflightWrites))

	close(release)
	wg.Wait()
	assert.Equal(t, int64(0), atomic.LoadInt64(&inflightWrites))
}

func TestTrackInflight_GETDoesNotCount(t *testing.T) {
	atomic.StoreInt64(&inflightWrites, 0)

	blocked := make(chan struct{})
	release := make(chan struct{})

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		blocked <- struct{}{}
		<-release
		w.WriteHeader(http.StatusOK)
	})
	handler := TrackInflight(inner)

	const n = 3
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/download", nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
		}()
	}

	for i := 0; i < n; i++ {
		<-blocked
	}
	assert.Equal(t, int64(0), atomic.LoadInt64(&inflightWrites))

	close(release)
	wg.Wait()
	assert.Equal(t, int64(0), atomic.LoadInt64(&inflightWrites))
}

func TestTrackInflight_AllWriteMethods(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			atomic.StoreInt64(&inflightWrites, 0)

			var seen int64
			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen = atomic.LoadInt64(&inflightWrites)
				w.WriteHeader(http.StatusOK)
			})
			handler := TrackInflight(inner)

			req := httptest.NewRequest(method, "/resource", nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			assert.Equal(t, int64(1), seen)
			assert.Equal(t, int64(0), atomic.LoadInt64(&inflightWrites))
		})
	}
}

func TestTrackInflight_HEADAndOPTIONS(t *testing.T) {
	for _, method := range []string{http.MethodHead, http.MethodOptions} {
		t.Run(method, func(t *testing.T) {
			atomic.StoreInt64(&inflightWrites, 0)

			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
			handler := TrackInflight(inner)

			req := httptest.NewRequest(method, "/resource", nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			assert.Equal(t, int64(0), atomic.LoadInt64(&inflightWrites))
		})
	}
}

func TestInflightHandler(t *testing.T) {
	atomic.StoreInt64(&inflightWrites, 0)

	req := httptest.NewRequest(http.MethodGet, "/admin/inflight", nil)
	w := httptest.NewRecorder()
	InflightHandler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assert.Contains(t, w.Body.String(), `"inflight":0`)

	atomic.StoreInt64(&inflightWrites, 42)
	w = httptest.NewRecorder()
	InflightHandler(w, req)
	assert.Contains(t, w.Body.String(), `"inflight":42`)
}

func TestInflightHandler_DuringWrites(t *testing.T) {
	atomic.StoreInt64(&inflightWrites, 0)

	blocked := make(chan struct{})
	release := make(chan struct{})

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		blocked <- struct{}{}
		<-release
	})
	handler := TrackInflight(inner)

	go func() {
		req := httptest.NewRequest(http.MethodPut, "/upload", nil)
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}()
	<-blocked

	req := httptest.NewRequest(http.MethodGet, "/admin/inflight", nil)
	w := httptest.NewRecorder()
	InflightHandler(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"inflight":1`)

	close(release)
}
