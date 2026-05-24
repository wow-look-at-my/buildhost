package admin

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
)

var inflightWrites int64

func TrackInflight(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut, http.MethodPost, http.MethodPatch, http.MethodDelete:
			atomic.AddInt64(&inflightWrites, 1)
			defer atomic.AddInt64(&inflightWrites, -1)
		}
		next.ServeHTTP(w, r)
	})
}

func InflightHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int64{
		"inflight": atomic.LoadInt64(&inflightWrites),
	})
}
