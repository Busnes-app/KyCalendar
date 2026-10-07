package main

import (
	"context"
	"errors"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

func TestRenameUser(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	for _, name := range []string{"admin", "walter"} {
		if err := createUser(ctx, st, name, "InitialPassword123!"); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := st.Users().GetUserByUsername(ctx, "admin")
	if err := renameUser(ctx, st, "admin", "breakglass"); err != nil {
		t.Fatal(err)
	}
	after, err := st.Users().GetUserByUsername(ctx, "breakglass")
	if err != nil || after.ID != before.ID || after.PasswordHash != before.PasswordHash || after.Role != before.Role {
		t.Fatalf("after rename %+v %v", after, err)
	}
	if _, err := st.Users().GetUserByUsername(ctx, "admin"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("old name still resolves: %v", err)
	}
	recs, _, _ := st.Audit().ListAuditRecords(ctx, 0, 10)
	if len(recs) == 0 || recs[0].Action != "user.renamed" || recs[0].Resource != before.ID {
		t.Fatalf("rename not audited: %+v", recs)
	}

	for _, tc := range []struct {
		from, to string
		want     error
	}{
		{"nobody", "xx1", errNoSuchUser},
		{"Walter", "xx2", errNoSuchUser}, // exact name only, as reset-password
		{"walter", "breakglass", errUserExists},
		{"walter", "BreakGlass", errUserExists}, // no confusable twin of an existing name
	} {
		if err := renameUser(ctx, st, tc.from, tc.to); !errors.Is(err, tc.want) {
			t.Errorf("rename %q -> %q: want %v, got %v", tc.from, tc.to, tc.want, err)
		}
	}
	if err := renameUser(ctx, st, "walter", "bad name!"); err == nil {
		t.Error("invalid target name accepted")
	}
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_sso", Username: "sso", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "sub"}); err != nil {
		t.Fatal(err)
	}
	if err := renameUser(ctx, st, "sso", "sso2"); !errors.Is(err, errNotLocal) {
		t.Fatalf("SSO account: %v", err)
	}
}
