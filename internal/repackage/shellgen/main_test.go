package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func answers(t *testing.T, body string, statuses ...int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := int(n.Add(1)) - 1
		if i < len(statuses) {
			w.WriteHeader(statuses[i])
			return
		}
		io.WriteString(w, body)
	}))
	t.Cleanup(s.Close)
	return s, &n
}

func fastRetries(t *testing.T) {
	t.Helper()
	oldEvery, oldDeadline := retryEvery, fetchDeadline
	retryEvery, fetchDeadline = 5*time.Millisecond, 2*time.Second
	t.Cleanup(func() { retryEvery, fetchDeadline = oldEvery, oldDeadline })
}

func TestGetAsksAgainAfterARateLimitOrServerError(t *testing.T) {
	fastRetries(t)
	s, asked := answers(t, "layer", http.StatusTooManyRequests, http.StatusBadGateway)
	oldURL := registryURL
	registryURL = s.URL
	t.Cleanup(func() { registryURL = oldURL })

	ctx, cancel := context.WithTimeout(context.Background(), fetchDeadline)
	defer cancel()
	body, err := get(ctx, "t", "/v2/x", "")
	require.NoError(t, err)
	defer body.Close()
	got, _ := io.ReadAll(body)
	assert.Equal(t, "layer", string(got))
	assert.EqualValues(t, 3, asked.Load(), "two refusals, then the answer")
}

func TestGetDoesNotRetryAnAnswerThatWillNotChange(t *testing.T) {
	fastRetries(t)
	s, asked := answers(t, "", http.StatusNotFound)
	oldURL := registryURL
	registryURL = s.URL
	t.Cleanup(func() { registryURL = oldURL })

	_, err := get(context.Background(), "t", "/v2/x", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 404")
	assert.EqualValues(t, 1, asked.Load())
}

func TestGetGivesUpAtTheDeadlineAndSaysSo(t *testing.T) {
	fastRetries(t)
	fetchDeadline = 100 * time.Millisecond
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(s.Close)
	oldURL := registryURL
	registryURL = s.URL
	t.Cleanup(func() { registryURL = oldURL })

	ctx, cancel := context.WithTimeout(context.Background(), fetchDeadline)
	defer cancel()
	_, err := get(ctx, "t", "/v2/x", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 429")
	assert.Contains(t, err.Error(), "deadline")
}

func TestFetchTokenAsksAgainAfterARateLimit(t *testing.T) {
	fastRetries(t)
	s, asked := answers(t, `{"token":"abc"}`, http.StatusTooManyRequests)
	oldURL := tokenURL
	tokenURL = s.URL
	t.Cleanup(func() { tokenURL = oldURL })

	ctx, cancel := context.WithTimeout(context.Background(), fetchDeadline)
	defer cancel()
	token, err := fetchToken(ctx)
	require.NoError(t, err)
	assert.Equal(t, "abc", token)
	assert.EqualValues(t, 2, asked.Load())
}
