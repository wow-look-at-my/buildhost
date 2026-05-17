package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"testing"
)

func TestPutStoresContentAndReturnsCorrectKey(t *testing.T) {
	fs, err := NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatalf("NewFilesystem: %v", err)
	}

	content := []byte("hello world")
	wantHash := sha256.Sum256(content)
	wantKey := hex.EncodeToString(wantHash[:])

	key, size, err := fs.Put(context.Background(), bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if key != wantKey {
		t.Errorf("key = %q, want %q", key, wantKey)
	}
	if size != int64(len(content)) {
		t.Errorf("size = %d, want %d", size, len(content))
	}
}

func TestPutDeduplicates(t *testing.T) {
	fs, err := NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatalf("NewFilesystem: %v", err)
	}

	content := []byte("duplicate me")

	key1, size1, err := fs.Put(context.Background(), bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Put #1: %v", err)
	}

	key2, size2, err := fs.Put(context.Background(), bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Put #2: %v", err)
	}

	if key1 != key2 {
		t.Errorf("keys differ: %q vs %q", key1, key2)
	}
	if size1 != size2 {
		t.Errorf("sizes differ: %d vs %d", size1, size2)
	}
}

func TestGetReturnsStoredContent(t *testing.T) {
	fs, err := NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatalf("NewFilesystem: %v", err)
	}

	content := []byte("retrieve me")
	key, _, err := fs.Put(context.Background(), bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	rc, size, err := fs.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("content = %q, want %q", got, content)
	}
	if size != int64(len(content)) {
		t.Errorf("size = %d, want %d", size, len(content))
	}
}

func TestGetMissingKeyReturnsErrNotExist(t *testing.T) {
	fs, err := NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatalf("NewFilesystem: %v", err)
	}

	// Use a valid-looking hex key so the shard path is well-formed.
	fakeKey := "aa" + hex.EncodeToString(make([]byte, 31))

	_, _, err = fs.Get(context.Background(), fakeKey)
	if err != os.ErrNotExist {
		t.Errorf("Get(missing) error = %v, want os.ErrNotExist", err)
	}
}

func TestDeleteRemovesContent(t *testing.T) {
	fs, err := NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatalf("NewFilesystem: %v", err)
	}

	content := []byte("delete me")
	key, _, err := fs.Put(context.Background(), bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	if err := fs.Delete(context.Background(), key); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, _, err = fs.Get(context.Background(), key)
	if err != os.ErrNotExist {
		t.Errorf("Get after Delete error = %v, want os.ErrNotExist", err)
	}
}

func TestDeleteIdempotent(t *testing.T) {
	fs, err := NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatalf("NewFilesystem: %v", err)
	}

	fakeKey := "bb" + hex.EncodeToString(make([]byte, 31))

	if err := fs.Delete(context.Background(), fakeKey); err != nil {
		t.Errorf("Delete(missing) = %v, want nil", err)
	}
}

func TestExistsReturnsCorrectBoolean(t *testing.T) {
	fs, err := NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatalf("NewFilesystem: %v", err)
	}

	fakeKey := "cc" + hex.EncodeToString(make([]byte, 31))

	exists, err := fs.Exists(context.Background(), fakeKey)
	if err != nil {
		t.Fatalf("Exists(missing): %v", err)
	}
	if exists {
		t.Error("Exists(missing) = true, want false")
	}

	content := []byte("exist check")
	key, _, err := fs.Put(context.Background(), bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	exists, err = fs.Exists(context.Background(), key)
	if err != nil {
		t.Fatalf("Exists(present): %v", err)
	}
	if !exists {
		t.Error("Exists(present) = false, want true")
	}

	// Verify Exists returns false after Delete.
	if err := fs.Delete(context.Background(), key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	exists, err = fs.Exists(context.Background(), key)
	if err != nil {
		t.Fatalf("Exists(after delete): %v", err)
	}
	if exists {
		t.Error("Exists(after delete) = true, want false")
	}
}
