// The GitHub token an operator sets in the admin dashboard.

package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// GitHubCredential returns the stored token and when it was set. found is
// false when no token is stored.
func (d *DB) GitHubCredential(ctx context.Context) (token string, updatedAt time.Time, found bool, err error) {
	row, err := d.q.GetGitHubCredential(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, false, nil
	}
	if err != nil {
		return "", time.Time{}, false, fmt.Errorf("get github credential: %w", err)
	}
	return row.Token, row.UpdatedAt, true, nil
}

func (d *DB) SetGitHubCredential(ctx context.Context, token string) error {
	if err := d.q.SetGitHubCredential(ctx, token); err != nil {
		return fmt.Errorf("set github credential: %w", err)
	}
	return nil
}

func (d *DB) DeleteGitHubCredential(ctx context.Context) error {
	if err := d.q.DeleteGitHubCredential(ctx); err != nil {
		return fmt.Errorf("delete github credential: %w", err)
	}
	return nil
}
