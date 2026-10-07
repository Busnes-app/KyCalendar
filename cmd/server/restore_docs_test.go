package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// docsResetQuery returns the reset-list SQL docs/RESTORE.md tells the operator to run.
func docsResetQuery(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "RESTORE.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, "ATTACH 'old-data/kycalendar.db'") {
			start, end := strings.Index(line, `"`), strings.LastIndex(line, `"`)
			return line[start+1 : end]
		}
	}
	t.Fatal("RESTORE.md has no reset-list query")
	return ""
}

func seedUsers(t *testing.T, path string, users []store.User, changed map[string]string) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: config.SQLiteDSN(path)})
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range users {
		if err := st.Users().CreateUser(ctx, &u); err != nil {
			t.Fatal(err)
		}
	}
	_ = st.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for id, at := range changed {
		if _, err := db.Exec(`INSERT INTO audit_records (user_id, action, created_at) VALUES (?, 'auth.password_changed', ?)`, id, at); err != nil {
			t.Fatal(err)
		}
	}
}

// The runbook's list must include accounts whose password changed after the capsule and
// accounts deleted since (the restore brings both back with old hashes), and nothing else.
func TestRestoreDocsResetListMatchesRestoredUsers(t *testing.T) {
	dir := t.TempDir()
	restored := filepath.Join(dir, "data", "kycalendar.db")
	old := filepath.Join(dir, "old-data", "kycalendar.db")
	for _, d := range []string{filepath.Dir(restored), filepath.Dir(old)} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	user := func(id, name, role, provider string) store.User {
		return store.User{ID: id, Username: name, Role: role, Status: "active", SSOProvider: provider, SSOSubject: id}
	}
	alice, bob, carol, dave, erin := user("u_alice", "alice", "user", "local"), user("u_bob", "bob", "admin", "local"),
		user("u_carol", "carol", "user", "local"), user("u_dave", "dave", "user", "local"), user("u_erin", "erin", "user", "kysignon")
	seedUsers(t, restored, []store.User{alice, bob, carol, dave, erin}, nil)
	// At loss time bob and dave were deleted; alice and bob changed passwords after the capsule, carol before it.
	seedUsers(t, old, []store.User{alice, carol, erin}, map[string]string{
		"u_alice": "2026-09-06 10:00:00.1 +0000 UTC",
		"u_bob":   "2026-09-06 11:00:00 +0000 UTC",
		"u_carol": "2026-09-01 09:00:00 +0000 UTC",
	})

	query := strings.ReplaceAll(docsResetQuery(t), "'old-data/kycalendar.db'", "'"+old+"'")
	attach, sel, ok := strings.Cut(query, "; ")
	if !ok {
		t.Fatalf("unexpected query shape: %s", query)
	}
	db, err := sql.Open("sqlite", restored)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1) // ATTACH is per connection
	if _, err := db.Exec(attach); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(strings.TrimSuffix(sel, ";"))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var name, role string
		if err := rows.Scan(&name, &role); err != nil {
			t.Fatal(err)
		}
		got = append(got, name)
	}
	if want := []string{"bob", "alice", "dave"}; !slices.Equal(got, want) {
		t.Fatalf("reset list = %v, want %v", got, want)
	}
}
