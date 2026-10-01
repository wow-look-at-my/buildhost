package strip

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

type Result struct {
	StrippedPath string
	DebugPath    string
}

type ByteResult struct {
	Stripped []byte
	Debug    []byte
}

func StripBytes(data []byte, tmpDir ...string) (*ByteResult, error) {
	dir := ""
	if len(tmpDir) > 0 {
		dir = tmpDir[0]
	}
	tmp, err := os.CreateTemp(dir, "strip-input-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return nil, err
	}
	tmp.Close()

	result, err := Strip(tmp.Name())
	if err != nil {
		return nil, err
	}
	defer os.Remove(result.StrippedPath)
	defer os.Remove(result.DebugPath)

	stripped, err := os.ReadFile(result.StrippedPath)
	if err != nil {
		return nil, err
	}
	debug, err := os.ReadFile(result.DebugPath)
	if err != nil {
		return nil, err
	}
	return &ByteResult{Stripped: stripped, Debug: debug}, nil
}

// StripReader spools r to a temp file under tmpDir, runs the file-based Strip, and
func StripReader(r io.Reader, tmpDir string) (io.ReadCloser, int64, error) {
	return stripStream(r, tmpDir, false)
}

// StripReaderDebug is like StripReader but streams the extracted debug-symbols file
// instead of the stripped binary.
func StripReaderDebug(r io.Reader, tmpDir string) (io.ReadCloser, int64, error) {
	return stripStream(r, tmpDir, true)
}

func stripStream(r io.Reader, tmpDir string, debug bool) (io.ReadCloser, int64, error) {
	in, err := os.CreateTemp(tmpDir, "strip-input-*")
	if err != nil {
		return nil, 0, err
	}
	if _, err := io.Copy(in, r); err != nil {
		in.Close()
		os.Remove(in.Name())
		return nil, 0, err
	}
	if err := in.Close(); err != nil {
		os.Remove(in.Name())
		return nil, 0, err
	}

	res, err := Strip(in.Name())
	os.Remove(in.Name())
	if err != nil {
		return nil, 0, err
	}

	keep, drop := res.StrippedPath, res.DebugPath
	if debug {
		keep, drop = res.DebugPath, res.StrippedPath
	}
	os.Remove(drop)

	f, err := os.Open(keep)
	if err != nil {
		os.Remove(keep)
		return nil, 0, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		os.Remove(keep)
		return nil, 0, err
	}
	return &tempFileReadCloser{f: f, path: keep}, fi.Size(), nil
}

// tempFileReadCloser streams a temp file and removes it on Close.
type tempFileReadCloser struct {
	f    *os.File
	path string
}

func (t *tempFileReadCloser) Read(p []byte) (int, error) { return t.f.Read(p) }

func (t *tempFileReadCloser) Close() error {
	err := t.f.Close()
	os.Remove(t.path)
	return err
}

// ErrNotELF is returned when the input is not an ELF object file. Callers on
var ErrNotELF = errors.New("strip: input is not an ELF binary")

// Strip splits an ELF binary into a stripped binary and its debug symbols.
//
// The work is done in-process (see elf.go), NOT by shelling out to
func Strip(inputPath string) (*Result, error) {
	// Check the input before creating anything, so an unreadable path fails
	if _, err := os.Stat(inputPath); err != nil {
		return nil, fmt.Errorf("read input: %w", err)
	}

	dir := filepath.Dir(inputPath)

	strippedFile, err := os.CreateTemp(dir, "stripped-*")
	if err != nil {
		return nil, fmt.Errorf("create stripped temp: %w", err)
	}
	strippedPath := strippedFile.Name()
	strippedFile.Close()

	debugFile, err := os.CreateTemp(dir, "debug-*")
	if err != nil {
		os.Remove(strippedPath)
		return nil, fmt.Errorf("create debug temp: %w", err)
	}
	debugPath := debugFile.Name()
	debugFile.Close()

	if err := stripELF64(inputPath, strippedPath, debugPath); err != nil {
		os.Remove(strippedPath)
		os.Remove(debugPath)
		return nil, err
	}

	return &Result{StrippedPath: strippedPath, DebugPath: debugPath}, nil
}

// Available reports whether binary stripping can run. It is now always true:
func Available() bool { return true }

// LooksELF reports whether r begins with the ELF magic, consuming only those
// bytes. Callers use it to decide whether stripping is worth attempting BEFORE
func LooksELF(r io.Reader) bool {
	var magic [4]byte
	if _, err := io.ReadFull(r, magic[:]); err != nil {
		return false
	}
	return bytes.Equal(magic[:], elfMagic)
}

// LogSkipped reports that an artifact was served unstripped, and why.
//
// This exists because the opposite -- swallowing the error -- is what let
// stripping be broken in production for weeks without a trace: the shipped
func LogSkipped(ctx context.Context, storageKey string, err error) {
	if errors.Is(err, ErrNotELF) || errors.Is(err, ErrUnsupportedELF) {
		slog.DebugContext(ctx, "serving artifact unstripped",
			"storage_key", storageKey, "reason", err)
		return
	}
	slog.WarnContext(ctx, "binary stripping failed; serving artifact unstripped",
		"storage_key", storageKey, "error", err)
}
