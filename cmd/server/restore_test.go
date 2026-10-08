package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/ky-primitives/capsule"
	"github.com/Busnes-app/ky-primitives/recoveryclient"
	"github.com/Busnes-app/ky-primitives/recoverykey"
	"github.com/Busnes-app/kycalendar/internal/backup"
	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// restorableDB returns a migrated SQLite database holding a user with a session and an app
// password, and its sync epoch.
func restorableDB(t *testing.T) ([]byte, string) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "kycalendar.db")
	st, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: config.SQLiteDSN(path)})
	if err != nil {
		t.Fatal(err)
	}
	u := &store.User{ID: "usr_alice", Username: "alice", PasswordHash: "h", Role: "user", Status: "active", SSOProvider: "local"}
	if err := st.Users().CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := st.Sessions().CreateSession(ctx, &store.Session{TokenHash: "s", UserID: u.ID, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}, "h"); err != nil {
		t.Fatal(err)
	}
	if err := st.AppPasswords().Create(ctx, store.Seed, &store.AppPassword{ID: "ap", UserID: u.ID, Label: "phone", Hash: "h"}, 0); err != nil {
		t.Fatal(err)
	}
	epoch, err := st.Calendars().SyncEpoch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw, epoch
}

func sealFixture(t *testing.T, service string) (string, []string) {
	path, shares, _ := sealDBFixture(t, service)
	return path, shares
}

// sealDBFixture seals a real database at backup.DatabaseMember and returns its epoch.
func sealDBFixture(t *testing.T, service string) (string, []string, string) {
	t.Helper()
	db, epoch := restorableDB(t)
	priv, err := recoverykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	shares, err := recoverykey.Split(priv, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	key := recoveryclient.RecoveryKey{Public: priv.Public(), Threshold: 2, TotalShares: 3}
	payload := recoveryclient.Payload{ServiceName: service, AppVersion: "1.0.0",
		Files: []recoveryclient.File{{Path: backup.DatabaseMember, Data: db, Mode: 0600}}}
	raw, _, err := recoveryclient.Seal(payload, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "x.kycap")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	// Non-consecutive indices: {1,2} would let an XOR-shaped bug pass.
	return path, []string{shares[0].String(), shares[2].String()}, epoch
}

func TestRestoreExtractsWithTwoShares(t *testing.T) {
	path, shares, epoch := sealDBFixture(t, backup.ServiceName)
	target := t.TempDir()
	var out bytes.Buffer
	if err := restore(path, target, backup.ServiceName, shares, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), backup.ServiceName) {
		t.Fatalf("manifest not printed: %s", out.String())
	}

	ctx := context.Background()
	st, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: config.SQLiteDSN(filepath.Join(target, backup.DatabaseMember))})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Users().GetUserByID(ctx, "usr_alice"); err != nil {
		t.Fatal("restored data missing", err)
	}
	if _, err := st.Sessions().GetSession(ctx, "s"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("session survived the restore", err)
	}
	if list, err := st.AppPasswords().ListByUser(ctx, "usr_alice"); err != nil || len(list) != 0 {
		t.Fatalf("app passwords survived the restore: %d %v", len(list), err)
	}
	if got, err := st.Calendars().SyncEpoch(ctx); err != nil || got == epoch {
		t.Fatalf("epoch %q -> %q %v", epoch, got, err)
	}
}

func TestResetRestoredNamesTheMissingDatabase(t *testing.T) {
	dir := t.TempDir()
	err := resetRestored(context.Background(), dir)
	if want := filepath.Join(dir, "kycalendar.db"); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("got %v, want an error naming %s", err, want)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "kycalendar.db")); !os.IsNotExist(statErr) {
		t.Fatal("a reset on a missing database created one")
	}
}

func TestRestoreRefusesAnotherService(t *testing.T) {
	path, shares := sealFixture(t, "someone_else")
	err := restore(path, t.TempDir(), backup.ServiceName, shares, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "someone_else") {
		t.Fatalf("got %v, want a service-name refusal naming the capsule's service", err)
	}
}

func TestRestoreRefusesTheWrongKit(t *testing.T) {
	path, _ := sealFixture(t, backup.ServiceName)
	_, otherShares := sealFixture(t, backup.ServiceName)
	err := restore(path, t.TempDir(), backup.ServiceName, otherShares, &bytes.Buffer{})
	if !errors.Is(err, capsule.ErrWrongRecoveryKey) {
		t.Fatalf("got %v, want ErrWrongRecoveryKey", err)
	}
}

func TestRestoreRefusesOneShare(t *testing.T) {
	path, shares := sealFixture(t, backup.ServiceName)
	if err := restore(path, t.TempDir(), backup.ServiceName, shares[:1], &bytes.Buffer{}); err == nil {
		t.Fatal("one share of a 2-of-3 kit was accepted")
	}
}

// The SQLite DSN ends the path at '?' or '#': the reset would open a new, empty file elsewhere.
func TestRestoreRefusesAPathTheDSNCannotName(t *testing.T) {
	path, shares := sealFixture(t, backup.ServiceName)
	for _, name := range []string{"a?b", "a#b"} {
		target := filepath.Join(t.TempDir(), name)
		if err := restore(path, target, backup.ServiceName, shares, &bytes.Buffer{}); err == nil || errors.Is(err, errResetFailed) {
			t.Fatalf("%s: got %v, want a refusal before extracting", name, err)
		}
		if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Fatalf("%s: restore wrote the target before refusing", name)
		}
		dataDir := filepath.Join(t.TempDir(), name, "data")
		if err := resetRestored(context.Background(), dataDir); err == nil || !strings.Contains(err.Error(), "'?' or '#'") {
			t.Fatalf("%s: restore-reset got %v, want the path refusal", name, err)
		}
	}
}
