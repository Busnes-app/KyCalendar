package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

func TestReadPasswordLine(t *testing.T) {
	for in, want := range map[string]string{"Secret123!\n": "Secret123!", "Secret123!\r\n": "Secret123!", "Secret123!": "Secret123!", "a b\nsecond\n": "a b"} {
		if got, err := readPasswordLine(strings.NewReader(in)); err != nil || got != want {
			t.Errorf("readPasswordLine(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := readPasswordLine(strings.NewReader("")); err == nil {
		t.Error("empty stdin accepted")
	}
}

func TestResetPassword(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	if err := createUser(ctx, st, "walter", "WalterInitial123!"); err != nil {
		t.Fatal(err)
	}
	before, _ := st.Users().GetUserByUsername(ctx, "walter")
	if err := resetPassword(ctx, st, "walter", "WalterTemporary123!"); err != nil {
		t.Fatal(err)
	}
	after, err := st.Users().GetUserByUsername(ctx, "walter")
	if err != nil || after.Role != "user" || after.Status != "active" || !after.MustChangePassword || after.PasswordHash == before.PasswordHash {
		t.Fatalf("after reset %+v %v", after, err)
	}

	if err := resetPassword(ctx, st, "nobody", "WalterTemporary123!"); !errors.Is(err, errNoSuchUser) {
		t.Fatalf("unknown user: %v", err)
	}
	if err := resetPassword(ctx, st, "Walter", "WalterTemporary123!"); !errors.Is(err, errNoSuchUser) {
		t.Fatalf("other spelling: %v", err)
	}
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_sso", Username: "sso", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "sub"}); err != nil {
		t.Fatal(err)
	}
	if err := resetPassword(ctx, st, "sso", "WalterTemporary123!"); !errors.Is(err, errNotLocal) {
		t.Fatalf("SSO account: %v", err)
	}
	if err := resetPassword(ctx, st, "walter", "short"); err == nil {
		t.Fatal("weak password accepted")
	}
}
