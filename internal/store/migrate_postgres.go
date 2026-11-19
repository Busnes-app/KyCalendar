package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/Busnes-app/kycalendar/internal/store/migrations"
)

// ErrTargetPopulated is a migration target that already holds data.
var ErrTargetPopulated = errors.New("store: the PostgreSQL target already holds data; migrate into an empty database")

// seeded are the rows migrations write themselves; a target holding only these is empty.
var seeded = map[string]int64{"calendar_meta": 1, "calendar_usage": 1}

// MigrateFromSQLite copies the SQLite database at src, which must be at this build's schema
// (the caller migrates a private copy), into the PostgreSQL database at dsn. Everything runs
// in one transaction under the bootstrap lock: the emptiness check before any DDL, the
// migrations, the copy, the sequence advance and the row-for-row verification. check rolls
// it all back. It returns the rows copied per table.
func MigrateFromSQLite(ctx context.Context, src, dsn string, check bool) (map[string]int64, error) {
	lite, err := sql.Open("sqlite", src+"?mode=ro&_pragma=foreign_keys(ON)")
	if err != nil {
		return nil, err
	}
	defer lite.Close()
	ltx, err := lite.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer ltx.Rollback()
	if err := checkSchemaVersion(ctx, ltx); err != nil {
		return nil, fmt.Errorf("source: %w", err)
	}
	if err := checkCatalog(ctx, ltx, false); err != nil {
		return nil, fmt.Errorf("source: %w", err)
	}

	pg, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	defer pg.Close()
	tx, err := pg.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("target: %w", err)
	}
	defer tx.Rollback()
	// The transaction form of the lock migrations and the first-admin bootstrap hold, so no
	// server can start on the target while it is filled.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(3, hashtext(current_schema() || ':bootstrap'))`); err != nil {
		return nil, fmt.Errorf("target: %w", err)
	}
	if err := refusePopulated(ctx, tx); err != nil {
		return nil, err
	}
	if err := migrations.RunTx(ctx, tx, "postgres"); err != nil {
		return nil, fmt.Errorf("target: %w", err)
	}
	cols, err := pgColumns(ctx, tx)
	if err != nil {
		return nil, err
	}
	sums, err := copyTables(ctx, ltx, tx, cols, true)
	if err != nil {
		return nil, err
	}
	if err := advanceSequences(ctx, tx); err != nil {
		return nil, err
	}
	if err := verifyCopy(ctx, tx, cols, sums, true); err != nil {
		return nil, err
	}
	counts := map[string]int64{}
	for t, s := range sums {
		counts[t] = s.rows
	}
	if check {
		return counts, tx.Rollback()
	}
	return counts, tx.Commit()
}

// refusePopulated refuses a schema with a newer migration or any row migrations did not seed.
func refusePopulated(ctx context.Context, tx dbConn) error {
	tables, err := catalogTables(ctx, tx, true)
	if err != nil {
		return err
	}
	if !slices.Contains(tables, "schema_migrations") {
		if len(tables) > 0 {
			return fmt.Errorf("%w: it has tables but no schema_migrations", ErrTargetPopulated)
		}
		return nil
	}
	v, err := SchemaVersion(ctx, tx)
	if err != nil {
		return err
	}
	if v > migrations.LatestVersion() {
		return fmt.Errorf("store: the target is at schema version %d, newer than this build's %d", v, migrations.LatestVersion())
	}
	for _, t := range tables {
		if slices.Contains(notData, t) {
			continue
		}
		var n int64
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+quote(t)).Scan(&n); err != nil {
			return err
		}
		if n > seeded[t] {
			return fmt.Errorf("%w (table %s has %d rows)", ErrTargetPopulated, t, n)
		}
	}
	return nil
}
