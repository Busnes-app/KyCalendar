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
	err := row.Scan(&p.ID, &p.UserID, &p.Label, &p.Hash, &p.Scope, &p.CreatedAt, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if last.Valid {
		p.LastUsedAt = &last.Time
	}
	return &p, err
}

const appPasswordCols = `id, user_id, label, hash, scope, created_at, last_used_at`

func (a *appPasswordStore) Create(ctx context.Context, by Grantor, p *AppPassword, max int) error {
	if p.Scope != ScopeDAV && !IsMCPScope(p.Scope) {
		return ErrInvalidScope
	}
	p.CreatedAt = time.Now().UTC()
	insert := func(tx *sql.Tx) error {
		if max > 0 {
			var n int
			if err := tx.QueryRowContext(ctx, a.q(`SELECT COUNT(1) FROM app_passwords WHERE user_id = ?`), p.UserID).Scan(&n); err != nil {
				return err
			}
			if n >= max {
				return ErrQuotaExceeded
			}
		}
		_, err := tx.ExecContext(ctx, a.q(`INSERT INTO app_passwords (id, user_id, label, hash, scope, created_at) VALUES (?, ?, ?, ?, ?, ?)`),
			p.ID, p.UserID, p.Label, p.Hash, p.Scope, p.CreatedAt)
		return err
	}
	if by.seed {
		tx, err := a.store.beginTx(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err := insert(tx); err != nil {
			return err
		}
		return tx.Commit()
	}
	if by.sessionHash == "" {
		return ErrSessionExpired
	}
	// The row lock waits for any purge that updates the row first (SetRole, SetStatus,
	// ReattachSSOUser, BindSignIn, password changes); the session recheck then sees it.
	err := a.store.withPassword(ctx, p.UserID, by.passwordHash, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, a.q(`SELECT COUNT(1) FROM sessions WHERE token_hash = ? AND user_id = ? AND expires_at > ?`),
			by.sessionHash, p.UserID, time.Now().UTC()).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrSessionExpired
		}
		return insert(tx)
	})
	if errors.Is(err, ErrNotFound) { // inactive, or the password changed since authentication
		return ErrSessionExpired
	}
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

const mcpScopes = `scope IN ('mcp:read', 'mcp:write-own', 'mcp:write-any')`

func (a *appPasswordStore) SetScope(ctx context.Context, userID, id, scope string) error {
	if !IsMCPScope(scope) {
		return ErrInvalidScope
	}
	res, err := a.store.db.ExecContext(ctx, a.q(`UPDATE app_passwords SET scope = ? WHERE id = ? AND user_id = ? AND `+mcpScopes), scope, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (a *appPasswordStore) ExpireIdle(ctx context.Context, before time.Time, id string) ([]*AppPassword, error) {
	idle := mcpScopes + ` AND COALESCE(last_used_at, created_at) < ?`
	args := []any{before.UTC()}
	if id != "" {
		idle += ` AND id = ?`
		args = append(args, id)
	}
	rows, err := a.store.db.QueryContext(ctx, a.q(`SELECT `+appPasswordCols+` FROM app_passwords WHERE `+idle), args...)
	if err != nil {
		return nil, err
	}
	var found []*AppPassword
	for rows.Next() {
		p, err := scanAppPassword(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		found = append(found, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Each delete repeats the idle condition: a token used since the read stays.
	var out []*AppPassword
	for _, p := range found {
		res, err := a.store.db.ExecContext(ctx, a.q(`DELETE FROM app_passwords WHERE id = ? AND `+idle), append([]any{p.ID}, args...)...)
		if err != nil {
			return out, err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			out = append(out, p)
		}
	}
	return out, nil
}

func (a *appPasswordStore) RecordMCPEvent(ctx context.Context, tokenID, calendarID, uid string) error {
	_, err := a.store.db.ExecContext(ctx, a.q(`INSERT INTO mcp_events (token_id, calendar_id, uid) VALUES (?, ?, ?)`), tokenID, calendarID, uid)
	return err
}

func (a *appPasswordStore) ForgetMCPEvent(ctx context.Context, tokenID, calendarID, uid string) error {
	_, err := a.store.db.ExecContext(ctx, a.q(`DELETE FROM mcp_events WHERE token_id = ? AND calendar_id = ? AND uid = ?`), tokenID, calendarID, uid)
	return err
}

func (a *appPasswordStore) MCPEvents(ctx context.Context, tokenID string) (map[string]bool, error) {
	rows, err := a.store.db.QueryContext(ctx, a.q(`SELECT calendar_id, uid FROM mcp_events WHERE token_id = ?`), tokenID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var cal, uid string
		if err := rows.Scan(&cal, &uid); err != nil {
			return nil, err
		}
		out[cal+"/"+uid] = true
	}
	return out, rows.Err()
}
