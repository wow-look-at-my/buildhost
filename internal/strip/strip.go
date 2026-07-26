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
// returns a reader over the stripped binary plus its exact size. The returned ReadCloser
// owns the stripped temp file and removes it (and the discarded debug file) on Close, so
// the caller MUST Close it. Bounded memory: the artifact is streamed to disk, never held
// in a []byte.
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
// the download path treat any Strip error as "serve the artifact unstripped",
// which is exactly the right behavior here.
var ErrNotELF = errors.New("strip: input is not an ELF binary")

// Strip splits an ELF binary into a stripped binary and its debug symbols.
//
// The work is done in-process (see elf.go), NOT by shelling out to
// strip/objcopy. Two reasons, both of which cost production dearly:
//
//   - The hardened image is distroless and ships no binutils, so the shell-out
//     implementation could never run there. Stripping silently no-opped in
//     production for weeks -- every download carried `X-Debug-Symbols:
//     unavailable` and nobody was told -- while the design says stripping
//     happens at download time.
//   - strip/objcopy go through BFD, which accepts PE/COFF and Mach-O as well as
//     ELF and rewrites those inputs rather than failing. A Cosmopolitan APE
//     artifact is a PE32+ to BFD, so it was served corrupt and irreproducible,
//     breaking `brew install` on any host that did have binutils.
//
// Non-ELF input is refused (ErrNotELF) before anything is written, so the
// caller falls back to serving the uploaded bytes untouched.
func Strip(inputPath string) (*Result, error) {
	// Check the input before creating anything, so an unreadable path fails
	// cleanly instead of leaving temp files next to a directory that may not
	// even exist.
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
// stripping is implemented in-process, so it no longer depends on strip(1) and
// objcopy(1) being installed. It used to be false in the distroless production
// image, which is how the feature came to silently do nothing there.
func Available() bool { return true }

// LooksELF reports whether r begins with the ELF magic, consuming only those
// bytes. Callers use it to decide whether stripping is worth attempting BEFORE
// spooling an artifact to disk: a non-ELF artifact (a Cosmopolitan APE, a
// Mach-O, a script, an archive) can never be stripped, and spooling gigabytes
// to discover that on every download would be pure waste.
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
// image had no strip(1), every attempt failed, both call sites discarded the
// error, and the only visible symptom was a header nobody was watching. A
// non-ELF artifact is an ordinary, expected skip and logs at debug level;
// anything else is a real failure and logs at warn, because it means an ELF
// this server was supposed to strip came out the other side untouched.
func LogSkipped(ctx context.Context, storageKey string, err error) {
	if errors.Is(err, ErrNotELF) || errors.Is(err, ErrUnsupportedELF) {
		slog.DebugContext(ctx, "serving artifact unstripped",
			"storage_key", storageKey, "reason", err)
		return
	}
	slog.WarnContext(ctx, "binary stripping failed; serving artifact unstripped",
		"storage_key", storageKey, "error", err)
}
