package strip

import (
	"fmt"
	"io"
	"os"
	"os/exec"
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

func Strip(inputPath string) (*Result, error) {
	data, err := os.ReadFile(inputPath)
	if err != nil {
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

	if err := os.WriteFile(strippedPath, data, 0o600); err != nil {
		os.Remove(strippedPath)
		os.Remove(debugPath)
		return nil, fmt.Errorf("copy for stripping: %w", err)
	}

	if err := exec.Command("objcopy", "--only-keep-debug", strippedPath, debugPath).Run(); err != nil {
		os.Remove(strippedPath)
		os.Remove(debugPath)
		return nil, fmt.Errorf("extract debug info: %w", err)
	}

	if err := exec.Command("strip", "--strip-debug", "--strip-unneeded", strippedPath).Run(); err != nil {
		os.Remove(strippedPath)
		os.Remove(debugPath)
		return nil, fmt.Errorf("strip binary: %w", err)
	}

	return &Result{StrippedPath: strippedPath, DebugPath: debugPath}, nil
}

func Available() bool {
	_, err1 := exec.LookPath("strip")
	_, err2 := exec.LookPath("objcopy")
	return err1 == nil && err2 == nil
}
