package store

import (
	"context"
	"strings"
)

// GetSettings devolve as configurações pedidas (chaves ausentes ficam de fora).
func (s *Store) GetSettings(ctx context.Context, keys ...string) (map[string]string, error) {
	out := map[string]string{}
	if len(keys) == 0 {
		return out, nil
	}
	q := "SELECT k, COALESCE(v,'') FROM settings WHERE k IN (?" + strings.Repeat(",?", len(keys)-1) + ")"
	args := make([]any, len(keys))
	for i, k := range keys {
		args[i] = k
	}
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func (s *Store) SetSetting(ctx context.Context, k, v string) error {
	_, err := s.DB.ExecContext(ctx, "INSERT INTO settings (k, v) VALUES (?, ?) ON DUPLICATE KEY UPDATE v=VALUES(v)", k, v)
	return err
}

func (s *Store) DeleteSetting(ctx context.Context, k string) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM settings WHERE k=?", k)
	return err
}
