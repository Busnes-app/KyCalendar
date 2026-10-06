package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type appPasswordStore struct{ store *SQLStore }

func (a *appPasswordStore) q(s string) string { return a.store.rebind(s) }

func scanAppPassword(row interface{ Scan(...any) error }) (*AppPassword, error) {
	var p AppPassword
	var last sql.NullTime
	err := row.Scan(&p.ID, &p.UserID, &p.Label, &p.Hash, &p.CreatedAt, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if last.Valid {
		p.LastUsedAt = &last.Time
	}
	return &p, err
}

const appPasswordCols = `id, user_id, label, hash, created_at, last_used_at`

func (a *appPasswordStore) Create(ctx context.Context, p *AppPassword) error {
	p.CreatedAt = time.Now().UTC()
	_, err := a.store.db.ExecContext(ctx, a.q(`INSERT INTO app_passwords (id, user_id, label, hash, created_at) VALUES (?, ?, ?, ?, ?)`),
		p.ID, p.UserID, p.Label, p.Hash, p.CreatedAt)
	return err
}

func (a *appPasswordStore) Get(ctx context.Context, id string) (*AppPassword, error) {
	return scanAppPassword(a.store.db.QueryRowContext(ctx, a.q(`SELECT `+appPasswordCols+` FROM app_passwords WHERE id = ?`), id))
}

func (a *appPasswordStore) ListByUser(ctx context.Context, userID string) ([]*AppPassword, error) {
	rows, err := a.store.db.QueryContext(ctx, a.q(`SELECT `+appPasswordCols+` FROM app_passwords WHERE user_id = ? ORDER BY created_at`), userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AppPassword
	for rows.Next() {
		p, err := scanAppPassword(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (a *appPasswordStore) Delete(ctx context.Context, userID, id string) error {
	res, err := a.store.db.ExecContext(ctx, a.q(`DELETE FROM app_passwords WHERE id = ? AND user_id = ?`), id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (a *appPasswordStore) DeleteByUser(ctx context.Context, userID string) error {
	_, err := a.store.db.ExecContext(ctx, a.q(`DELETE FROM app_passwords WHERE user_id = ?`), userID)
	return err
}

func (a *appPasswordStore) TouchLastUsed(ctx context.Context, id string, at time.Time) error {
	_, err := a.store.db.ExecContext(ctx, a.q(`UPDATE app_passwords SET last_used_at = ? WHERE id = ?`), at.UTC(), id)
	return err
}
