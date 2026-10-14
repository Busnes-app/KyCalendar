package store_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

// An operator DSN carrying any _pragma keeps foreign keys on (every ON DELETE CASCADE depends on
// them) and keeps each default pragma it does not name itself.
func TestSQLiteDSNPragmasKeepForeignKeysOn(t *testing.T) {
	ctx := context.Background()
	for _, query := range []string{"?_pragma=busy_timeout(1000)", "?_pragma=foreign_keys(OFF)", "?cache=shared&_pragma=synchronous(FULL)"} {
		t.Run(query, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "kc.db")
			st, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: path + query})
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			if err := st.Groups().CreateGroup(ctx, &store.Group{ID: "grp_1", DisplayName: "Crew"}); err != nil {
				t.Fatal(err)
			}
			if err := st.Groups().AddGroupMember(ctx, "grp_1", "usr_nobody"); err == nil {
				t.Fatal("a member with no user row was stored: foreign keys are off")
			}
			raw, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			var mode string
			if err := raw.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
				t.Fatalf("journal_mode %q %v, want wal", mode, err)
			}
		})
	}
}

// A transient failure reading members is an error, never an empty group: a reconciling IdP
// would otherwise re-add every member.
func TestListGroupsReportsAMemberReadFailure(t *testing.T) {
	ctx := context.Background()
	cfg := testdb.Config(t)
	st, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Groups().CreateGroup(ctx, &store.Group{ID: "grp_1", DisplayName: "Crew"}); err != nil {
		t.Fatal(err)
	}
	driver := map[string]string{"sqlite": "sqlite", "postgres": "pgx"}[cfg.Driver]
	raw, err := sql.Open(driver, cfg.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.ExecContext(ctx, "ALTER TABLE group_members RENAME TO group_members_gone"); err != nil {
		t.Fatal(err)
	}
	if groups, _, err := st.Groups().ListGroups(ctx, 0, 10, ""); err == nil {
		t.Fatalf("ListGroups hid the failure: %+v", groups[0])
	}
	if _, err := st.Groups().GetGroupByID(ctx, "grp_1"); err == nil || errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetGroupByID: %v, want the read error", err)
	}
}
