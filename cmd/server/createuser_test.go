package main

import (
	"context"
	"errors"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

func TestCreateUser(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	if err := createUser(ctx, st, "walter", "WalterInitial123!"); err != nil {
		t.Fatal(err)
	}
	u, err := st.Users().GetUserByUsername(ctx, "walter")
	if err != nil || u.Role != "user" || u.Status != "active" || u.SSOProvider != "local" || !u.MustChangePassword || u.PasswordHash == "" {
		t.Fatalf("created %+v %v", u, err)
	}
	if err := createUser(ctx, st, "walter", "AnotherPassword123!"); !errors.Is(err, errUserExists) {
		t.Fatalf("second create: %v, want errUserExists (never reset an account)", err)
	}
	for _, bad := range []struct{ name, pw string }{{"", "WalterInitial123!"}, {"x", "short"}} {
		if err := createUser(ctx, st, bad.name, bad.pw); err == nil {
			t.Errorf("createUser(%q, %q) accepted", bad.name, bad.pw)
		}
	}
}
