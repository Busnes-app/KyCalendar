package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Busnes-app/ky-primitives/password"
	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

type failingCount struct{ store.UserStore }

func (failingCount) CountUsers(context.Context) (int, error) { return 0, errors.New("db down") }

type countFails struct{ store.Store }

func (s countFails) Users() store.UserStore { return failingCount{s.Store.Users()} }

func openBootstrapStore(t *testing.T) store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// A database that cannot count its users is not an empty one: no second admin.
func TestBootstrapAdminFailsWhenUsersCannotBeCounted(t *testing.T) {
	t.Setenv("KY_ADMIN_PASSWORD", "")
	st := openBootstrapStore(t)
	dir := t.TempDir()
	if err := bootstrapAdmin(context.Background(), countFails{st}, dir); err == nil {
		t.Fatal("a failed count was read as an empty database")
	}
	if _, err := st.Users().GetUserByUsername(context.Background(), "admin"); err == nil {
		t.Fatal("a failed count created a bootstrap admin")
	}
}

// The generated password goes to a 0600 file in the data directory, never the log, and a retry
// after a start that died before the account was written reuses it.
func TestBootstrapAdminWritesThePasswordToAFileNotTheLog(t *testing.T) {
	t.Setenv("KY_ADMIN_PASSWORD", "")
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, initialAdminPasswordFile)

	// A previous start wrote the file, then died before CreateUser.
	if _, err := initialAdminPassword(dir, path); err != nil {
		t.Fatal(err)
	}
	st := openBootstrapStore(t)
	if err := bootstrapAdmin(ctx, st, dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("no password file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("password file mode %o, want 0600", info.Mode().Perm())
	}
	raw, _ := os.ReadFile(path)
	pw := strings.TrimSpace(string(raw))
	u, err := st.Users().GetUserByUsername(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := password.Verify(pw, u.PasswordHash); !ok || err != nil {
		t.Fatalf("the file's password does not open the admin account: %v", err)
	}
	if pw == "" || strings.Contains(logs.String(), pw) || !strings.Contains(logs.String(), path) {
		t.Errorf("log must name the file and never the password: %q", logs.String())
	}

	// Users exist now: a restart leaves the account and the file alone.
	if err := bootstrapAdmin(ctx, st, dir); err != nil {
		t.Fatal(err)
	}
	if n, _ := st.Users().CountUsers(ctx); n != 1 {
		t.Errorf("restart created another account: %d users", n)
	}
}
