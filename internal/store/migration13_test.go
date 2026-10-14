package store_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

// Login lookups (LOWER(...) = LOWER(?)), UserGrants (group_members by user) and per-user
// revocation (mfa_challenges, device_pairings by user) each have an index to use.
func TestMigration13LookupsUseIndexes(t *testing.T) {
	ctx := context.Background()
	cfg := testdb.Config(t)
	st, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	db := rawDB(t, cfg)
	want := map[string]string{
		"idx_users_username_lower": `SELECT id FROM users WHERE LOWER(username) = LOWER(?)`,
		"idx_users_email_lower":    `SELECT id FROM users WHERE LOWER(email) = LOWER(?)`,
		"idx_group_members_user":   `SELECT group_id FROM group_members WHERE user_id = ?`,
		"idx_mfa_challenges_user":  `DELETE FROM mfa_challenges WHERE user_id = ?`,
		"idx_pairings_user":        `DELETE FROM device_pairings WHERE user_id = ?`,
	}
	for index, query := range want {
		if cfg.Driver == "postgres" {
			// The planner prefers a sequential scan on empty tables; check the index exists.
			var n int
			if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_indexes WHERE schemaname = current_schema() AND indexname = $1`, index).Scan(&n); err != nil || n != 1 {
				t.Errorf("%s: %d, %v", index, n, err)
			}
			continue
		}
		rows, err := db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, "x")
		if err != nil {
			t.Fatal(err)
		}
		var plan strings.Builder
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan.WriteString(detail + "\n")
		}
		rows.Close()
		if !strings.Contains(plan.String(), index) {
			t.Errorf("%s does not use %s:\n%s", query, index, plan.String())
		}
	}
}
