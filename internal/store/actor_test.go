package store_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

// An access write authorised before its actor was demoted or signed out must fail at commit time,
// leaving the target and the actor as they were.
func TestAccessWritesRecheckTheActor(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	root := &store.User{ID: "usr_root", Username: "root", Role: "admin", Status: "active", SSOProvider: "local"}
	bob := &store.User{ID: "usr_bob", Username: "bob", Role: "admin", Status: "active", SSOProvider: "local"}
	ann := &store.User{ID: "usr_ann", Username: "ann", Role: "user", Status: "active", SSOProvider: "local"}
	seedUsers(t, st, root, bob, ann)
	seedSession(t, st, bob)
	bobActs := store.AdminActor(bob.ID, "tok_"+bob.ID)

	// Each write a revoked actor might still have in flight.
	writes := map[string]func(store.Actor) error{
		"promote ann":  func(a store.Actor) error { return st.Users().SetRole(ctx, a, ann.ID, "admin") },
		"disable ann":  func(a store.Actor) error { return st.Users().SetStatus(ctx, a, ann.ID, "inactive") },
		"restore self": func(a store.Actor) error { return st.Users().SetRole(ctx, a, bob.ID, "admin") },
		"enable self":  func(a store.Actor) error { return st.Users().SetStatus(ctx, a, bob.ID, "active") },
		"demote root":  func(a store.Actor) error { return st.Users().SetRole(ctx, a, root.ID, "user") },
		"create admin": func(a store.Actor) error {
			return st.Users().CreateUserAs(ctx, a, &store.User{ID: "usr_new", Username: "new", Role: "admin", Status: "active", SSOProvider: "local"})
		},
		"create person": func(a store.Actor) error {
			return st.Users().CreateUserAs(ctx, a, &store.User{ID: "usr_new2", Username: "new2", Role: "user", Status: "active", SSOProvider: "local"})
		},
	}
	unchanged := func(why string) {
		t.Helper()
		for _, want := range []*store.User{root, ann} {
			if u, _ := st.Users().GetUserByID(ctx, want.ID); u.Role != want.Role || u.Status != want.Status {
				t.Errorf("%s: %s changed to role=%s status=%s", why, want.ID, u.Role, u.Status)
			}
		}
		for _, id := range []string{"usr_new", "usr_new2"} {
			if _, err := st.Users().GetUserByID(ctx, id); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("%s: %s was created", why, id)
			}
		}
	}
	refused := func(why string, a store.Actor, wantBob string) {
		t.Helper()
		for name, write := range writes {
			if err := write(a); !errors.Is(err, store.ErrActorRevoked) {
				t.Errorf("%s, %s: %v, want ErrActorRevoked", why, name, err)
			}
		}
		unchanged(why)
		if u, _ := st.Users().GetUserByID(ctx, bob.ID); u.Role+"/"+u.Status != wantBob {
			t.Errorf("%s: bob is %s/%s, want %s", why, u.Role, u.Status, wantBob)
		}
	}

	refused("zero actor", store.Actor{}, "admin/active")
	refused("unknown session", store.AdminActor(bob.ID, "tok_nobody"), "admin/active")
	seedSession(t, st, ann)
	refused("another user's session", store.AdminActor(bob.ID, "tok_"+ann.ID), "admin/active")
	if err := st.Sessions().DeleteSession(ctx, "tok_"+ann.ID); err != nil {
		t.Fatal(err)
	}

	// The finding: root demotes bob, which revokes bob's sessions, while bob's request is in flight.
	if err := st.Users().SetRole(ctx, store.AdminActor(root.ID, seedRootSession(t, st, root)), bob.ID, "user"); err != nil {
		t.Fatalf("root demotes bob: %v", err)
	}
	refused("demoted and signed out", bobActs, "user/active")

	// Each condition alone refuses: a fresh session does not make a demoted bob an admin again.
	seedSession(t, st, bob)
	refused("demoted with a live session", bobActs, "user/active")
	if err := st.Users().SetRole(ctx, store.System, bob.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	// SetStatus revokes sessions and CreateSession refuses an inactive account, so a whole-row
	// write leaves the session in place to isolate the status condition.
	seedSession(t, st, bob)
	off, err := st.Users().GetUserByID(ctx, bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	off.Status = "inactive"
	if err := st.Users().UpdateUser(ctx, off); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Sessions().GetSession(ctx, "tok_"+bob.ID); err != nil {
		t.Fatalf("the session should survive the whole-row write: %v", err)
	}
	refused("disabled with a live session", bobActs, "admin/inactive")

	// A live administrator's writes go through.
	if err := st.Users().SetStatus(ctx, store.System, bob.ID, "active"); err != nil {
		t.Fatal(err)
	}
	seedSession(t, st, bob)
	if err := st.Users().SetRole(ctx, bobActs, ann.ID, "admin"); err != nil {
		t.Errorf("a live admin promotes ann: %v", err)
	}
	if err := st.Users().CreateUserAs(ctx, bobActs, &store.User{ID: "usr_ok", Username: "ok", Role: "admin", Status: "active", SSOProvider: "local"}); err != nil {
		t.Errorf("a live admin creates an admin: %v", err)
	}
}

func seedRootSession(t *testing.T, st store.Store, u *store.User) string {
	t.Helper()
	seedSession(t, st, u)
	return "tok_" + u.ID
}

// On Postgres the recheck waits for a demotion holding the local-admins lock, then sees it.
func TestActorRecheckWaitsForTheAccessLock(t *testing.T) {
	if testdb.Config(t).Driver != "postgres" {
		t.Skip("postgres only: SQLite's single connection serialises the two")
	}
	for name, write := range map[string]func(context.Context, store.Store, store.Actor) error{
		"create admin": func(ctx context.Context, st store.Store, a store.Actor) error {
			return st.Users().CreateUserAs(ctx, a, &store.User{ID: "usr_new", Username: "new", Role: "admin", Status: "active", SSOProvider: "local"})
		},
		"promote ann": func(ctx context.Context, st store.Store, a store.Actor) error {
			return st.Users().SetRole(ctx, a, "usr_ann", "admin")
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			cfg := testdb.Config(t)
			st, err := store.Open(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			bob := &store.User{ID: "usr_bob", Username: "bob", Role: "admin", Status: "active", SSOProvider: "local"}
			seedUsers(t, st, bob,
				&store.User{ID: "usr_root", Username: "root", Role: "admin", Status: "active", SSOProvider: "local"},
				&store.User{ID: "usr_ann", Username: "ann", Role: "user", Status: "active", SSOProvider: "local"})
			seedSession(t, st, bob)

			raw, err := sql.Open("pgx", cfg.DSN)
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			demotion, err := raw.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer demotion.Rollback()
			if _, err := demotion.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('local-admins', 0))`); err != nil {
				t.Fatal(err)
			}
			if _, err := demotion.ExecContext(ctx, `UPDATE users SET role = 'user' WHERE id = 'usr_bob'`); err != nil {
				t.Fatal(err)
			}

			done := make(chan error, 1)
			go func() { done <- write(ctx, st, store.AdminActor(bob.ID, "tok_"+bob.ID)) }()
			for deadline := time.Now().Add(10 * time.Second); ; {
				var waiting int
				if err := raw.QueryRowContext(ctx, `SELECT COUNT(1) FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND wait_event = 'advisory'`).Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting > 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("the write never waited on the local-admins lock")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err := demotion.Commit(); err != nil {
				t.Fatal(err)
			}
			if err := <-done; !errors.Is(err, store.ErrActorRevoked) {
				t.Fatalf("write after the demotion committed: %v, want ErrActorRevoked", err)
			}
		})
	}
}
