package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// CacheScope names the entries one ref of one repository holds. RepoID and Ref
// come from the verified OIDC token of the job that asks.
type CacheScope struct {
	RepoID string
	Ref    string
}

// CacheMatch is the entry a restore found, and the scope that holds it.
type CacheMatch struct {
	Scope      CacheScope
	Key        string
	StorageKey string
	Size       int64
}

// FindCache answers the entry a restore reads, in the order GitHub's cache
// uses. The exact key comes first, then each restore key as a prefix, newest
// first. Each is tried in every scope, in the order scopes names them.
func (d *DB) FindCache(ctx context.Context, scopes []CacheScope, key string, restoreKeys []string) (CacheMatch, bool, error) {
	for _, s := range scopes {
		row, err := d.q.GetCacheEntry(ctx, GetCacheEntryParams{RepoID: s.RepoID, Ref: s.Ref, Key: key})
		if err == nil {
			return CacheMatch{Scope: s, Key: row.Key, StorageKey: row.StorageKey, Size: row.Size}, true, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return CacheMatch{}, false, fmt.Errorf("get cache entry: %w", err)
		}
	}
	for _, prefix := range restoreKeys {
		for _, s := range scopes {
			row, err := d.q.FindCacheEntryByPrefix(ctx, FindCacheEntryByPrefixParams{RepoID: s.RepoID, Ref: s.Ref, Prefix: prefix})
			if err == nil {
				return CacheMatch{Scope: s, Key: row.Key, StorageKey: row.StorageKey, Size: row.Size}, true, nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return CacheMatch{}, false, fmt.Errorf("find cache entry: %w", err)
			}
		}
	}
	return CacheMatch{}, false, nil
}

// GetCache returns the entry the scope holds under key, and whether it holds one.
func (d *DB) GetCache(ctx context.Context, s CacheScope, key string) (CacheMatch, bool, error) {
	row, err := d.q.GetCacheEntry(ctx, GetCacheEntryParams{RepoID: s.RepoID, Ref: s.Ref, Key: key})
	if errors.Is(err, sql.ErrNoRows) {
		return CacheMatch{}, false, nil
	}
	if err != nil {
		return CacheMatch{}, false, fmt.Errorf("get cache entry: %w", err)
	}
	return CacheMatch{Scope: s, Key: row.Key, StorageKey: row.StorageKey, Size: row.Size}, true, nil
}

// PutCache records blob under key in the scope. A key is written once, so it
// reports false and changes nothing when the scope already holds the key.
func (d *DB) PutCache(ctx context.Context, s CacheScope, key, repoPath string, blob RunBlob) (bool, error) {
	n, err := d.q.InsertCacheEntry(ctx, InsertCacheEntryParams{
		RepoID: s.RepoID, Ref: s.Ref, Key: key, RepoPath: repoPath, StorageKey: blob.StorageKey, Size: blob.Size,
	})
	if err != nil {
		return false, fmt.Errorf("put cache entry: %w", err)
	}
	return n == 1, nil
}

// MarkCacheRead counts one restore of the entry and moves its last use to now.
func (d *DB) MarkCacheRead(ctx context.Context, s CacheScope, key string) error {
	if err := d.q.MarkCacheEntryRead(ctx, MarkCacheEntryReadParams{RepoID: s.RepoID, Ref: s.Ref, Key: key}); err != nil {
		return fmt.Errorf("mark cache entry read: %w", err)
	}
	return nil
}

// ListCacheEntries returns every entry, least recently used first.
func (d *DB) ListCacheEntries(ctx context.Context) ([]CacheEntry, error) {
	rows, err := d.q.ListCacheEntries(ctx)
	if err != nil {
		return nil, fmt.Errorf("list cache entries: %w", err)
	}
	return rows, nil
}

// DeleteCache forgets the entry. The caller deletes its blob once nothing else
// references it.
func (d *DB) DeleteCache(ctx context.Context, s CacheScope, key string) error {
	if err := d.q.DeleteCacheEntry(ctx, DeleteCacheEntryParams{RepoID: s.RepoID, Ref: s.Ref, Key: key}); err != nil {
		return fmt.Errorf("delete cache entry: %w", err)
	}
	return nil
}
