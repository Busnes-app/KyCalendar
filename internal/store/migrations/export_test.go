package migrations

import (
	"context"
	"database/sql"
)

// RunUntil migrates db up to and including version, so an upgrade test can start from an
// older schema.
func RunUntil(ctx context.Context, db *sql.DB, driver string, version int) error {
	if _, err := db.ExecContext(ctx, initTable(driver)); err != nil {
		return err
	}
	for _, m := range registry {
		if m.Version > version {
			break
		}
		if err := apply(ctx, db, driver, m); err != nil {
			return err
		}
	}
	return nil
}
