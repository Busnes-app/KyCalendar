package store_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

// onPostgres skips a test of state only PostgreSQL replicas share.
func onPostgres(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("KY_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PostgreSQL only: set KY_TEST_POSTGRES_DSN")
	}
	return dsn
}

// openTwice opens two stores, two "replicas", on one database.
func openTwice(t *testing.T) (config.DatabaseConfig, store.Store, store.Store) {
	t.Helper()
	cfg := testdb.Config(t)
	a, err := store.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	b, err := store.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return cfg, a, b
}

// Concurrent hits from two replicas admit exactly the limit in total.
func TestSharedWindowAdmitsTheLimitAcrossReplicas(t *testing.T) {
	onPostgres(t)
	_, a, b := openTwice(t)
	var admitted atomic.Int32
	var wg sync.WaitGroup
	for i := range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st := a
			if i%2 == 1 {
				st = b
			}
			_, _, ok, err := st.Coordination().TakeAttempt(context.Background(), "acct:login:u@ip", 10, time.Minute)
			if err != nil {
				t.Error(err)
			}
			if ok {
				admitted.Add(1)
			}
		}()
	}
	wg.Wait()
	if n := admitted.Load(); n != 10 {
		t.Fatalf("admitted %d of 40, want exactly 10", n)
	}
}

// A refund reaches only the window that admitted it: a slow success cannot hand a hit back
// to the window after it.
func TestRefundRestoresOnlyItsOwnWindow(t *testing.T) {
	onPostgres(t)
	_, a, b := openTwice(t)
	ctx := context.Background()
	old, _, _, err := a.Coordination().TakeAttempt(ctx, "k", 5, 300*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)
	if _, _, _, err := b.Coordination().TakeAttempt(ctx, "k", 5, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := a.Coordination().ReleaseAttempt(ctx, "k", old); err != nil {
		t.Fatal(err)
	}
	if hits, _, _ := b.Coordination().AttemptHits(ctx, "k"); hits != 1 {
		t.Fatalf("hits %d after a refund to the expired window, want 1", hits)
	}
	gen, _, _, _ := b.Coordination().TakeAttempt(ctx, "k", 5, time.Minute)
	_ = a.Coordination().ReleaseAttempt(ctx, "k", gen)
	if hits, _, _ := a.Coordination().AttemptHits(ctx, "k"); hits != 1 {
		t.Fatalf("hits %d after its own refund, want 1", hits)
	}
}

// A CAPTCHA salt and a KyPost event ID are spent once across replicas; a forgotten event ID
// is accepted again, an expired one is replaced, and only its own attempt undoes a record.
func TestReplayAndSpendAcrossReplicas(t *testing.T) {
	onPostgres(t)
	_, a, b := openTwice(t)
	ctx := context.Background()
	exp := time.Now().Add(time.Minute).UnixMilli()
	if ok, err := a.Coordination().SpendChallenge(ctx, "0123456789abcdef0123456789abcdef", exp); !ok || err != nil {
		t.Fatalf("first spend: %v %v", ok, err)
	}
	if ok, _ := b.Coordination().SpendChallenge(ctx, "0123456789abcdef0123456789abcdef", exp); ok {
		t.Fatal("a salt was spent twice across replicas")
	}
	if fresh, err := a.Coordination().RecordReplay(ctx, "evt-1", "t", time.Minute); !fresh || err != nil {
		t.Fatalf("first record: %v %v", fresh, err)
	}
	if fresh, _ := b.Coordination().RecordReplay(ctx, "evt-1", "t", time.Minute); fresh {
		t.Fatal("an event ID recorded on A was new on B")
	}
	if err := b.Coordination().ForgetReplay(ctx, "evt-1"); err != nil {
		t.Fatal(err)
	}
	if fresh, _ := a.Coordination().RecordReplay(ctx, "evt-1", "t", 50*time.Millisecond); !fresh {
		t.Fatal("a forgotten event ID was refused")
	}
	time.Sleep(100 * time.Millisecond)
	if fresh, _ := b.Coordination().RecordReplay(ctx, "evt-1", "t2", time.Minute); !fresh {
		t.Fatal("an expired event ID was refused")
	}
	// Only the attempt that wrote a record undoes it; a takeover of an expired record is the
	// taker's (t2), no longer the expired writer's (t).
	if err := a.Coordination().UndoReplay(ctx, "evt-1", "t"); err != nil {
		t.Fatal(err)
	}
	if fresh, _ := a.Coordination().RecordReplay(ctx, "evt-1", "u", time.Minute); fresh {
		t.Fatal("another attempt's undo released the record")
	}
	if err := a.Coordination().UndoReplay(ctx, "evt-1", "t2"); err != nil {
		t.Fatal(err)
	}
	if fresh, _ := a.Coordination().RecordReplay(ctx, "evt-1", "u", time.Minute); !fresh {
		t.Fatal("the writing attempt's undo kept the record")
	}
	if err := a.Coordination().CleanExpired(ctx); err != nil {
		t.Fatal(err)
	}
}

// heldBy is the backend holding name's session lock.
func lockHolder(t *testing.T, st store.Store, name string) int {
	t.Helper()
	pid, err := store.LockHolderForTest(st, name)
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

// One replica owns a lock at a time; terminating the owner's session ends its work within
// the probe bound, and another replica takes the lock after.
func TestLockLossCancelsTheOwner(t *testing.T) {
	onPostgres(t)
	_, a, b := openTwice(t)
	ctx := context.Background()
	entered, lost := make(chan struct{}), make(chan time.Time, 1)
	go func() {
		_, _ = a.Coordination().WithLock(ctx, "scheduled-worker", func(owned context.Context) error {
			close(entered)
			<-owned.Done()
			lost <- time.Now()
			if !errors.Is(context.Cause(owned), store.ErrOwnershipLost) {
				t.Errorf("owned context ended with %v, want ErrOwnershipLost", context.Cause(owned))
			}
			return nil
		})
	}()
	<-entered
	if won, err := b.Coordination().WithLock(ctx, "scheduled-worker", func(context.Context) error { return nil }); won || err != nil {
		t.Fatalf("second replica won=%v err=%v while the first owns the lock", won, err)
	}
	killed := time.Now()
	if err := store.TerminateBackendForTest(b, lockHolder(t, b, "scheduled-worker")); err != nil {
		t.Fatal(err)
	}
	select {
	case at := <-lost:
		if d := at.Sub(killed); d > 3*time.Second {
			t.Fatalf("owned work cancelled %v after the session ended, want within 3 s", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("owned work was never cancelled")
	}
	won, err := b.Coordination().WithLock(ctx, "scheduled-worker", func(context.Context) error { return nil })
	if !won || err != nil {
		t.Fatalf("takeover: won=%v err=%v", won, err)
	}
}

// A frozen database bounds both taking a lock and holding one: acquisition returns within 3 s,
// and an owner whose probes go unanswered loses its work within the probe bound.
func TestFrozenDatabaseBoundsTheLock(t *testing.T) {
	dsn := onPostgres(t)
	cfg := testdb.Config(t)
	relay := testdb.NewRelay(t, testdb.PostgresTarget(t, dsn))
	st, err := store.Open(context.Background(), testdb.Via(t, cfg, relay))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	entered, lost := make(chan struct{}), make(chan time.Time, 1)
	go func() {
		_, _ = st.Coordination().WithLock(ctx, "held", func(owned context.Context) error {
			close(entered)
			<-owned.Done()
			lost <- time.Now()
			return nil
		})
	}()
	<-entered
	relay.Freeze()
	frozen := time.Now()
	select {
	case at := <-lost:
		if d := at.Sub(frozen); d > 3*time.Second {
			t.Fatalf("owner kept its work %v into the freeze, want within 3 s", d)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("an owner on a frozen database kept its work")
	}

	start := time.Now()
	won, err := st.Coordination().WithLock(ctx, "another", func(context.Context) error { return nil })
	if d := time.Since(start); won || err == nil || d > 3*time.Second {
		t.Fatalf("acquisition on a frozen database: won=%v err=%v after %v, want a refusal within 3 s", won, err, d)
	}
	relay.Thaw()
}

// Migrations and the first-admin bootstrap share one session lock: a store opening while
// another process holds it waits for it.
func TestMigrationsWaitForTheBootstrapLock(t *testing.T) {
	onPostgres(t)
	cfg, a, _ := openTwice(t)
	release, held := make(chan struct{}), make(chan struct{})
	go func() {
		_ = a.Coordination().Serialized(context.Background(), "bootstrap", func(context.Context) error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	opened := make(chan error, 1)
	go func() {
		st, err := store.Open(context.Background(), cfg)
		if err == nil {
			st.Close()
		}
		opened <- err
	}()
	select {
	case err := <-opened:
		t.Fatalf("migrations ran while the bootstrap lock was held: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	close(release)
	if err := <-opened; err != nil {
		t.Fatal(err)
	}
}

// SQLite: a flock beside the database, held until work returns.
func TestSQLiteLocksAreFlocks(t *testing.T) {
	t.Setenv("KY_TEST_POSTGRES_DSN", "")
	_, a, b := openTwice(t)
	ctx := context.Background()
	won, err := a.Coordination().WithLock(ctx, "backup-run", func(context.Context) error {
		if won, err := b.Coordination().WithLock(ctx, "backup-run", func(context.Context) error { return nil }); won || err != nil {
			t.Errorf("a second holder won=%v err=%v", won, err)
		}
		return nil
	})
	if !won || err != nil {
		t.Fatal(won, err)
	}
	if won, _ := b.Coordination().WithLock(ctx, "backup-run", func(context.Context) error { return nil }); !won {
		t.Fatal("the lock was not released")
	}
}

// A restore starts a new set of replicas: the configuration record and rate windows go; spent
// CAPTCHA salts and KyPost event IDs only ever refuse, so unexpired ones stay.
func TestResetAfterRestoreKeepsOnlyLiveRefusals(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	c := st.Coordination()
	live, gone := time.Now().Add(time.Hour).UnixMilli(), time.Now().Add(-time.Hour).UnixMilli()
	if err := st.Settings().SetSetting(ctx, "replica_config_fingerprint", "{}"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := c.TakeAttempt(ctx, "acct:x", 5, time.Hour); err != nil {
		t.Fatal(err)
	}
	for salt, exp := range map[string]int64{"live-salt": live, "gone-salt": gone} {
		if _, err := c.SpendChallenge(ctx, salt, exp); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.RecordReplay(ctx, "live-evt", "t", time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RecordReplay(ctx, "gone-evt", "t", time.Millisecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := st.ResetAfterRestore(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Settings().GetSetting(ctx, "replica_config_fingerprint"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the replica record survived the reset: %v", err)
	}
	if hits, _, _ := c.AttemptHits(ctx, "acct:x"); hits != 0 {
		t.Fatalf("a rate window survived the reset: %d", hits)
	}
	if ok, _ := c.SpendChallenge(ctx, "live-salt", live); ok {
		t.Fatal("an unexpired spent salt was forgotten by the reset")
	}
	if ok, _ := c.SpendChallenge(ctx, "gone-salt", live); !ok {
		t.Fatal("an expired salt was kept by the reset")
	}
	if fresh, _ := c.RecordReplay(ctx, "live-evt", "t", time.Hour); fresh {
		t.Fatal("an unexpired KyPost event ID was forgotten by the reset")
	}
	if n, _ := store.RowCountForTest(st, "kypost_replay", "event_id = 'gone-evt'"); n != 0 {
		t.Fatal("an expired KyPost event ID was kept by the reset")
	}
}
