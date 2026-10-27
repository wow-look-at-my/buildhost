// Server settings are the admin dashboard's values for BUILDHOST_* variables.

package db

import (
	"context"
	"fmt"
)

// ServerSettings returns every stored value by variable name.
func (d *DB) ServerSettings(ctx context.Context) (map[string]string, error) {
	rows, err := d.q.ListServerSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("list server settings: %w", err)
	}
	m := make(map[string]string, len(rows))
	for _, r := range rows {
		m[r.Name] = r.Value
	}
	return m, nil
}

// SetServerSetting stores value for name.
func (d *DB) SetServerSetting(ctx context.Context, name, value string) error {
	if err := d.q.SetServerSetting(ctx, SetServerSettingParams{Name: name, Value: value}); err != nil {
		return fmt.Errorf("set server setting %s: %w", name, err)
	}
	return nil
}

// DeleteServerSetting removes the stored value for name.
func (d *DB) DeleteServerSetting(ctx context.Context, name string) error {
	if err := d.q.DeleteServerSetting(ctx, name); err != nil {
		return fmt.Errorf("delete server setting %s: %w", name, err)
	}
	return nil
}
