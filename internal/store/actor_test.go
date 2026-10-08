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
	ann := &store.User{ID: "usr_ann", Username: "ann", PasswordHash: "h_ann", Role: "user", Status: "active", SSOProvider: "local"}
	carol := &store.User{ID: "usr_carol", Username: "carol", Role: "user", Status: "inactive", SSOProvider: "kysignon", SSOSubject: "s1", SSOIssuer: "kyidentity https://a.example"}
	seedUsers(t, st, root, bob, ann, carol)
	seedSession(t, st, bob)
	bobActs := store.AdminActor(bob.ID, "tok_"+bob.ID)

	// Each write a revoked actor might still have in flight.
	writes := map[string]func(store.Actor) error{
		"reset ann":    func(a store.Actor) error { return st.Users().ResetPassword(ctx, a, ann.ID, "h_reset") },
		"promote ann":  func(a store.Actor) error { return st.Users().SetRole(ctx, a, ann.ID, "admin") },
		"disable ann":  func(a store.Actor) error { return st.Users().SetStatus(ctx, a, ann.ID, "inactive") },
		"restore self": func(a store.Actor) error { return st.Users().SetRole(ctx, a, bob.ID, "admin") },
		"enable self":  func(a store.Actor) error { return st.Users().SetStatus(ctx, a, bob.ID, "active") },
		"demote root":  func(a store.Actor) error { return st.Users().SetRole(ctx, a, root.ID, "user") },
		"reattach carol": func(a store.Actor) error {
			_, err := st.Users().ReattachSSOUser(ctx, a, carol.ID, "kyidentity https://b.example")
			return err
		},
		"bind sign-in": func(a store.Actor) error {
			_, err := st.Users().BindSignIn(ctx, a, store.SignInBinding{Disable: []string{"kysignon"}}, map[string]string{"signin_provider": "oidc"})
			return err
		},
		"create admin": func(a store.Actor) error {
			return st.Users().CreateUserAs(ctx, a, &store.User{ID: "usr_new", Username: "new", Role: "admin", Status: "active", SSOProvider: "local"})
		},
		"create person": func(a store.Actor) error {
			return st.Users().CreateUserAs(ctx, a, &store.User{ID: "usr_new2", Username: "new2", Role: "user", Status: "active", SSOProvider: "local"})
		},
	}
	unchanged := func(why string) {
		t.Helper()
		for _, want := range []*store.User{root, ann, carol} {
			if u, _ := st.Users().GetUserByID(ctx, want.ID); u.Role != want.Role || u.Status != want.Status || u.PasswordHash != want.PasswordHash || u.SSOIssuer != want.SSOIssuer {
				t.Errorf("%s: %s changed to role=%s status=%s hash=%s", why, want.ID, u.Role, u.Status, u.PasswordHash)
			}
		}
		if _, err := st.Settings().GetSetting(ctx, "signin_provider"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s: sign-in settings were written", why)
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
	if _, err := st.Users().BindSignIn(ctx, bobActs, store.SignInBinding{}, map[string]string{"signin_provider": "none"}); err != nil {
		t.Errorf("a live admin is refused by BindSignIn: %v", err)
	}
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
		"reset ann": func(ctx context.Context, st store.Store, a store.Actor) error {
			return st.Users().ResetPassword(ctx, a, "usr_ann", "h_reset")
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

// An SSO revocation of the acting administrator and an access write by that administrator are
// serialised on the actor's users row: the write locks it before its target, so a demotion
// committed first refuses the write and one arriving later waits for the write to commit. Never
// does the write land after the demotion committed.
func TestActorRowSerialisesWithSSORevocation(t *testing.T) {
	if testdb.Config(t).Driver != "postgres" {
		t.Skip("postgres only: SQLite's single connection serialises the two")
	}
	const oldBinding, newBinding = "kyidentity https://a.example", "kyidentity https://b.example"
	writes := map[string]struct {
		target func(*store.User) bool // true while the target is as seeded
		write  func(context.Context, store.Store, store.Actor) error
	}{
		"reattach": {
			target: func(u *store.User) bool { return u.SSOIssuer == oldBinding && u.Status == "inactive" },
			write: func(ctx context.Context, st store.Store, a store.Actor) error {
				_, err := st.Users().ReattachSSOUser(ctx, a, "usr_target", newBinding)
				return err
			},
		},
		"disable": {
			target: func(u *store.User) bool { return u.Status == "active" },
			write: func(ctx context.Context, st store.Store, a store.Actor) error {
				return st.Users().SetStatus(ctx, a, "usr_local", "inactive")
			},
		},
	}
	revocations := map[string]struct {
		raw string // the revocation's row-first statement, held open by a raw transaction
		run func(context.Context, store.Store) error
	}{
		"SetSSORole": {
			raw: `UPDATE users SET role = 'user' WHERE id = 'usr_sso'`,
			run: func(ctx context.Context, st store.Store) error { return st.Users().SetSSORole(ctx, "usr_sso", "user") },
		},
		"RevokeSSOUser": {
			raw: `UPDATE users SET status = 'inactive' WHERE id = 'usr_sso'`,
			run: func(ctx context.Context, st store.Store) error { return st.Users().RevokeSSOUser(ctx, "usr_sso", true) },
		},
	}
	for wname, w := range writes {
		for rname, r := range revocations {
			setup := func(t *testing.T) (context.Context, store.Store, *sql.DB, func() *store.User) {
				ctx := context.Background()
				cfg := testdb.Config(t)
				st, err := store.Open(ctx, cfg)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = st.Close() })
				sso := &store.User{ID: "usr_sso", Username: "sso", Role: "admin", Status: "active", SSOProvider: "kysignon", SSOSubject: "s0", SSOIssuer: oldBinding}
				seedUsers(t, st, sso,
					&store.User{ID: "usr_root", Username: "root", Role: "admin", Status: "active", SSOProvider: "local"},
					&store.User{ID: "usr_target", Username: "target", Role: "user", Status: "inactive", SSOProvider: "kysignon", SSOSubject: "s1", SSOIssuer: oldBinding},
					&store.User{ID: "usr_local", Username: "local", Role: "user", Status: "active", SSOProvider: "local"})
				seedSession(t, st, sso)
				raw, err := sql.Open("pgx", cfg.DSN)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = raw.Close() })
				target := map[string]string{"reattach": "usr_target", "disable": "usr_local"}[wname]
				get := func() *store.User {
					u, err := st.Users().GetUserByID(ctx, target)
					if err != nil {
						t.Fatal(err)
					}
					return u
				}
				return ctx, st, raw, get
			}
			actor := store.AdminActor("usr_sso", "tok_usr_sso")

			// The demotion holds the actor's row first: the write waits, then is refused.
			t.Run(wname+"/after "+rname, func(t *testing.T) {
				ctx, st, raw, target := setup(t)
				demotion, err := raw.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
				if err != nil {
					t.Fatal(err)
				}
				defer demotion.Rollback()
				for _, q := range []string{r.raw, `DELETE FROM sessions WHERE user_id = 'usr_sso'`} {
					if _, err := demotion.ExecContext(ctx, q); err != nil {
						t.Fatal(err)
					}
				}
				done := make(chan error, 1)
				go func() { done <- w.write(ctx, st, actor) }()
				waitForRowLockWaiters(t, ctx, raw, 1, done)
				if err := demotion.Commit(); err != nil {
					t.Fatal(err)
				}
				if err := <-done; !errors.Is(err, store.ErrActorRevoked) {
					t.Fatalf("write after the demotion committed: %v, want ErrActorRevoked", err)
				}
				if u := target(); !w.target(u) {
					t.Fatalf("a refused write changed the target: %+v", u)
				}
			})

			// The write holds the actor's row while it waits on its target: the demotion waits for it.
			t.Run(wname+"/before "+rname, func(t *testing.T) {
				ctx, st, raw, target := setup(t)
				hold, err := raw.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer hold.Rollback()
				if _, err := hold.ExecContext(ctx, `UPDATE users SET updated_at = updated_at WHERE id IN ('usr_target', 'usr_local')`); err != nil {
					t.Fatal(err)
				}
				wrote := make(chan error, 1)
				go func() { wrote <- w.write(ctx, st, actor) }()
				waitForRowLockWaiters(t, ctx, raw, 1, wrote)
				revoked := make(chan error, 1)
				go func() { revoked <- r.run(ctx, st) }()
				waitForRowLockWaiters(t, ctx, raw, 2, revoked)
				if err := hold.Commit(); err != nil {
					t.Fatal(err)
				}
				if err := <-wrote; err != nil {
					t.Fatalf("write ordered before the demotion: %v", err)
				}
				if err := <-revoked; err != nil {
					t.Fatalf("demotion: %v", err)
				}
				if u := target(); w.target(u) {
					t.Fatalf("the write did not land: %+v", u)
				}
			})
		}
	}
}

// waitForRowLockWaiters returns once n backends wait on a row lock; it fails the test if early
// finishes first, which means that call did not wait.
func waitForRowLockWaiters(t *testing.T, ctx context.Context, raw *sql.DB, n int, early chan error) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; {
		select {
		case err := <-early:
			t.Fatalf("finished without waiting (%v)", err)
		default:
		}
		var waiting int
		if err := raw.QueryRowContext(ctx, `SELECT COUNT(1) FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND wait_event IN ('transactionid', 'tuple')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d row-lock waiters, want %d", waiting, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
