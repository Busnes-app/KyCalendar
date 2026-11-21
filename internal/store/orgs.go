package store

import (
	"context"
	"time"

	"github.com/Busnes-app/kycalendar/internal/store/migrations"
)

// TenancySetting records the install's mode at first start. Every install is single-tenant
// until multi-tenant mode ships (docs/superpowers/specs/2026-11-20-tenancy-design.md, D1).
const TenancySetting = "tenancy_mode"

// ensureOrgs gives a single-tenant install its default org, the org's usage row and the
// recorded mode, idempotently and race-free across replicas. Migration 37 already made them for
// an install that had users; a fresh database gets them here, before its first admin. Copy
// targets (snapshots) clear every table before copying, so the rows never collide.
func (s *SQLStore) ensureOrgs(ctx context.Context) error {
	tx, err := s.beginTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO orgs (id, status) VALUES (?, 'active') ON CONFLICT (id) DO NOTHING`, []any{migrations.DefaultOrgID}},
		{`INSERT INTO org_usage (org_id, bytes) VALUES (?, 0) ON CONFLICT (org_id) DO NOTHING`, []any{migrations.DefaultOrgID}},
		{`INSERT INTO server_settings (key, value, updated_at) VALUES (?, 'single', ?) ON CONFLICT (key) DO NOTHING`, []any{TenancySetting, time.Now().UTC()}},
	} {
		if _, err := tx.ExecContext(ctx, s.rebind(q.sql), q.args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}
