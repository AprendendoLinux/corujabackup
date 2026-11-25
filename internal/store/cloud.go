package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type CloudAccount struct {
	ID             int64      `json:"id"`
	Name           string     `json:"name"`
	Type           string     `json:"type"`
	Status         string     `json:"status"`
	AccountLabel   string     `json:"account_label"`
	QuotaTotal     int64      `json:"quota_total"`
	QuotaUsed      int64      `json:"quota_used"`
	QuotaCheckedAt *time.Time `json:"quota_checked_at"`
	LastError      string     `json:"last_error"`
	CreatedAt      time.Time  `json:"created_at"`

	// Cifrados (nunca vão para a API):
	Config string `json:"-"`
	Tokens string `json:"-"`
	Device string `json:"-"`
}

const cloudCols = "id, name, type, status, config, COALESCE(tokens,''), COALESCE(device,''), account_label, quota_total, quota_used, quota_checked_at, last_error, created_at"

func scanCloud(r interface{ Scan(...any) error }) (*CloudAccount, error) {
	var a CloudAccount
	var q sql.NullTime
	if err := r.Scan(&a.ID, &a.Name, &a.Type, &a.Status, &a.Config, &a.Tokens, &a.Device, &a.AccountLabel, &a.QuotaTotal, &a.QuotaUsed, &q, &a.LastError, &a.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if q.Valid {
		a.QuotaCheckedAt = &q.Time
	}
	return &a, nil
}

func (s *Store) CreateCloud(ctx context.Context, name, typ, sealedConfig, sealedDevice string) (int64, error) {
	res, err := s.DB.ExecContext(ctx, "INSERT INTO cloud_accounts (name, type, status, config, device) VALUES (?,?, 'pending', ?, ?)", name, typ, sealedConfig, nullStr(sealedDevice))
	if err != nil {
		if isDup(err) {
			return 0, ErrDuplicate
		}
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetCloud(ctx context.Context, id int64) (*CloudAccount, error) {
	return scanCloud(s.DB.QueryRowContext(ctx, "SELECT "+cloudCols+" FROM cloud_accounts WHERE id=?", id))
}

func (s *Store) ListClouds(ctx context.Context) ([]CloudAccount, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+cloudCols+" FROM cloud_accounts ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CloudAccount{}
	for rows.Next() {
		a, err := scanCloud(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

func (s *Store) SetCloudDevice(ctx context.Context, id int64, sealedDevice string) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE cloud_accounts SET device=?, status='pending', last_error='' WHERE id=?", nullStr(sealedDevice), id)
	return err
}

// SetCloudTokens grava os tokens (cifrados) e, se informado, o rótulo da conta.
func (s *Store) SetCloudTokens(ctx context.Context, id int64, sealedTokens, label string) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE cloud_accounts SET tokens=?, device=NULL, status='connected', last_error='', account_label=IF(?='', account_label, ?) WHERE id=?", sealedTokens, label, label, id)
	return err
}

// SetCloudTokensOnly atualiza só os tokens (renovação), sem mexer no estado.
func (s *Store) SetCloudTokensOnly(ctx context.Context, id int64, sealedTokens string) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE cloud_accounts SET tokens=? WHERE id=?", sealedTokens, id)
	return err
}

func (s *Store) SetCloudStatus(ctx context.Context, id int64, status, lastErr string) error {
	if len(lastErr) > 500 {
		lastErr = lastErr[:500]
	}
	_, err := s.DB.ExecContext(ctx, "UPDATE cloud_accounts SET status=?, last_error=? WHERE id=?", status, lastErr, id)
	return err
}

func (s *Store) SetCloudQuota(ctx context.Context, id, total, used int64) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE cloud_accounts SET quota_total=?, quota_used=?, quota_checked_at=UTC_TIMESTAMP() WHERE id=?", total, used, id)
	return err
}

func (s *Store) RenameCloud(ctx context.Context, id int64, name string) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE cloud_accounts SET name=? WHERE id=?", name, id)
	if isDup(err) {
		return ErrDuplicate
	}
	return err
}

func (s *Store) DeleteCloud(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM cloud_accounts WHERE id=?", id)
	return err
}

func (s *Store) SetCloudLabel(ctx context.Context, id int64, label string) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE cloud_accounts SET account_label=?, status='connected', last_error='' WHERE id=?", label, id)
	return err
}
