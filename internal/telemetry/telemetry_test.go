package telemetry

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestInit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	shutdown, err := Init(context.Background(), srv.URL, "v0.0.1-test")
	require.NoError(t, err)
	require.NotNil(t, shutdown)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, shutdown(ctx))
}

func TestFanoutHandler(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	h1 := slog.NewTextHandler(&buf1, nil)
	h2 := slog.NewTextHandler(&buf2, nil)

	fh := &fanoutHandler{handlers: []slog.Handler{h1, h2}}

	assert.True(t, fh.Enabled(context.Background(), slog.LevelInfo))

	logger := slog.New(fh)
	logger.Info("test message", "key", "value")

	assert.Contains(t, buf1.String(), "test message")
	assert.Contains(t, buf2.String(), "test message")
	assert.Contains(t, buf1.String(), "key=value")
	assert.Contains(t, buf2.String(), "key=value")
}

func TestFanoutHandlerWithAttrs(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, nil)
	fh := &fanoutHandler{handlers: []slog.Handler{h}}

	child := fh.WithAttrs([]slog.Attr{slog.String("service", "test")})
	logger := slog.New(child)
	logger.Info("hello")

	assert.Contains(t, buf.String(), "service=test")
}

func TestFanoutHandlerWithGroup(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, nil)
	fh := &fanoutHandler{handlers: []slog.Handler{h}}

	child := fh.WithGroup("grp")
	logger := slog.New(child)
	logger.Info("hello", "k", "v")

	assert.Contains(t, buf.String(), "grp.k=v")
}
