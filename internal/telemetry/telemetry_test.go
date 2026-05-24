package telemetry

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
)

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
