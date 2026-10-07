package store_test

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

// The People screen renames as an admin; the audit row names that admin, not "system".
func TestRenameUserAuditsTheActor(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_l", Username: "lee", Role: "user", Status: "active", SSOProvider: "local"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Users().RenameUser(ctx, "usr_admin", "usr_l", "lee2"); err != nil {
		t.Fatal(err)
	}
	recs, _, err := st.Audit().ListAuditRecords(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) == 0 || recs[0].Action != "user.renamed" || recs[0].UserID != "usr_admin" || recs[0].Details != "from=lee to=lee2" {
		t.Fatalf("audit %+v, want user.renamed by usr_admin", recs)
	}
}

// Usernames are unique ignoring case: "ann" and "ANN" would split one person's sign-in.
func TestUsernameCaseTwinsAreRefused(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	seedUsers(t, st,
		&store.User{ID: "usr_ann", Username: "ann", Role: "user", Status: "active", SSOProvider: "local"},
		&store.User{ID: "usr_bob", Username: "bob", Role: "user", Status: "active", SSOProvider: "local"},
	)
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_ann2", Username: "ANN", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "s"}); !errors.Is(err, store.ErrAlreadyExists) {
		t.Errorf("create a case twin: %v, want ErrAlreadyExists", err)
	}
	if err := st.Users().RenameUser(ctx, "usr_admin", "usr_bob", "Ann"); !errors.Is(err, store.ErrAlreadyExists) {
		t.Errorf("rename onto a case twin: %v, want ErrAlreadyExists", err)
	}
	if err := st.Users().RenameUser(ctx, "usr_admin", "usr_ann", "Ann"); err != nil {
		t.Errorf("a case-only rename of the same account: %v", err)
	}
}

func TestConcurrentCaseTwinCreatesKeepOne(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	start := make(chan struct{})
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i, name := range []string{"ann", "ANN"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = st.Users().CreateUser(ctx, &store.User{ID: "usr_" + name, Username: name, Role: "user", Status: "active", SSOProvider: "local"})
		}()
	}
	close(start)
	wg.Wait()
	if (errs[0] == nil) == (errs[1] == nil) {
		t.Fatalf("want exactly one create, got %v and %v", errs[0], errs[1])
	}
	for _, err := range errs {
		if err != nil && !errors.Is(err, store.ErrAlreadyExists) {
			t.Fatalf("refusal: %v, want ErrAlreadyExists", err)
		}
	}
}

// A create waits on "user-names" while another create holds it, then sees that create's row:
// the raw transaction stands in for a CreateUser("ann") caught between its check and commit.
func TestCaseTwinCreateWaitsForTheNameLock(t *testing.T) {
	cfg := testdb.Config(t)
	if cfg.Driver != "postgres" {
		t.Skip("postgres only: SQLite's single connection serialises the two")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	raw, err := sql.Open("pgx", cfg.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	first, err := raw.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Rollback()
	if _, err := first.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('user-names', 0))`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := first.ExecContext(ctx, `INSERT INTO users (id, username, email, display_name, password_hash, role, status, sso_provider, sso_subject, totp_secret_enc, totp_enabled, recovery_codes_hash, push_device_id, must_change_password, created_at, updated_at) VALUES ('usr_ann', 'ann', '', '', '', 'user', 'active', 'local', '', '', false, '[]', '', false, $1, $1)`, now); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		done <- st.Users().CreateUser(ctx, &store.User{ID: "usr_ANN", Username: "ANN", Role: "user", Status: "active", SSOProvider: "local"})
	}()
	for deadline := time.Now().Add(10 * time.Second); ; {
		var waiting int
		if err := raw.QueryRowContext(ctx, `SELECT COUNT(1) FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND wait_event = 'advisory'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("CreateUser never waited on the user-names lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := first.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, store.ErrAlreadyExists) {
		t.Fatalf("create of a case twin after the lock: %v, want ErrAlreadyExists", err)
	}
}
