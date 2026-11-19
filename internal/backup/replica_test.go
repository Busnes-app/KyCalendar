package backup_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Busnes-app/ky-primitives/recoveryclient"
	"github.com/Busnes-app/ky-primitives/recoverykey"
	"github.com/Busnes-app/kycalendar/internal/backup"
	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

// replicas opens n servers' worth of configuration and store on one test database (either
// driver), each with its own data directory, as replicas have.
func replicas(t *testing.T, n int) ([]*config.Config, []store.Store) {
	t.Helper()
	db := testdb.Config(t)
	var cfgs []*config.Config
	var sts []store.Store
	for range n {
		cfg := &config.Config{}
		cfg.Server.AppName, cfg.Server.Port = "Busnes Calendar", 8080
		cfg.Database = db
		cfg.Database.DataDir = t.TempDir()
		if db.Driver == "sqlite" {
			// One SQLite file; each "replica" still has its own recovery.pub directory.
			cfg.Database.DSN = db.DSN
		}
		cfg.Security.EncryptionKey = bytes.Repeat([]byte{1}, 32)
		st, err := store.Open(context.Background(), cfg.Database)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		cfgs, sts = append(cfgs, cfg), append(sts, st)
	}
	return cfgs, sts
}

func newKey(t *testing.T, k, n int) recoveryclient.RecoveryKey {
	t.Helper()
	priv, err := recoverykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return recoveryclient.RecoveryKey{Public: priv.Public(), Threshold: k, TotalShares: n}
}

func onPostgres(t *testing.T) {
	t.Helper()
	if os.Getenv("KY_TEST_POSTGRES_DSN") == "" {
		t.Skip("PostgreSQL only: set KY_TEST_POSTGRES_DSN")
	}
}

// A key pinned on A reaches B: B writes its own recovery.pub from the sealed record and seals
// to the same key. A tampered or substituted record, or a stale recovery.pub on B, refuses.
func TestPinnedKeyReachesEveryReplica(t *testing.T) {
	onPostgres(t)
	cfgs, sts := replicas(t, 2)
	ctx := context.Background()
	k := newKey(t, 2, 3)
	if err := backup.PinRecoveryKey(ctx, cfgs[0], sts[0], k); err != nil {
		t.Fatal(err)
	}
	if err := backup.SyncRecoveryKey(ctx, cfgs[1], sts[1]); err != nil {
		t.Fatal(err)
	}
	got, err := recoveryclient.LoadRecoveryKey(cfgs[1].Database.DataDir, backup.Settings(ctx, sts[1].Settings()))
	if err != nil || got.Public.ID() != k.Public.ID() || got.Threshold != 2 || got.TotalShares != 3 {
		t.Fatalf("B loads %v %v", got.Public.ID(), err)
	}

	sealed, err := sts[0].Settings().GetSetting(ctx, backup.RecoveryKeyRecordSetting)
	if err != nil {
		t.Fatal(err)
	}
	refuses := func(name string, want error) {
		t.Helper()
		_ = os.Remove(recoveryclient.RecoveryKeyPath(cfgs[1].Database.DataDir))
		if err := backup.SyncRecoveryKey(ctx, cfgs[1], sts[1]); !errors.Is(err, want) {
			t.Errorf("%s: %v, want %v", name, err, want)
		}
		if _, err := os.Stat(recoveryclient.RecoveryKeyPath(cfgs[1].Database.DataDir)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s: B wrote a recovery.pub", name)
		}
	}
	flipped := []byte(sealed)
	flipped[len(flipped)/2] ^= 1
	if err := sts[0].Settings().SetSetting(ctx, backup.RecoveryKeyRecordSetting, string(flipped)); err != nil {
		t.Fatal(err)
	}
	refuses("flipped byte", backup.ErrRecoveryKeyTampered)

	// A database writer without the encryption key swaps in their own key: the pin row and a
	// record sealed under another key.
	other := newKey(t, 2, 3)
	thief := *cfgs[0]
	thief.Security.EncryptionKey = bytes.Repeat([]byte{9}, 32)
	thiefCfg, thiefSt := replicasWithKey(t, &thief)
	if err := backup.PinRecoveryKey(ctx, thiefCfg, thiefSt, other); err != nil {
		t.Fatal(err)
	}
	forged, _ := thiefSt.Settings().GetSetting(ctx, backup.RecoveryKeyRecordSetting)
	for key, val := range map[string]string{"kyrecovery_key_id": other.Public.ID(), backup.RecoveryKeyRecordSetting: forged} {
		if err := forceSetting(cfgs[0], key, val); err != nil {
			t.Fatal(err)
		}
	}
	refuses("substituted key", backup.ErrRecoveryKeyTampered)

	// Put the real pin back, then give B a recovery.pub naming another key.
	for key, val := range map[string]string{"kyrecovery_key_id": k.Public.ID(), backup.RecoveryKeyRecordSetting: sealed} {
		if err := forceSetting(cfgs[0], key, val); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.Remove(recoveryclient.RecoveryKeyPath(cfgs[1].Database.DataDir))
	if err := os.WriteFile(recoveryclient.RecoveryKeyPath(cfgs[1].Database.DataDir), other.Public.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := backup.SyncRecoveryKey(ctx, cfgs[1], sts[1]); !errors.Is(err, recoveryclient.ErrKeyMismatch) {
		t.Fatalf("a stale recovery.pub: %v, want ErrKeyMismatch", err)
	}
}

// replicasWithKey opens another schema-sharing store under cfg (a database writer's view).
func replicasWithKey(t *testing.T, cfg *config.Config) (*config.Config, store.Store) {
	t.Helper()
	c := *cfg
	tmp := testdb.Config(t) // a scratch schema: the thief seals there, then copies rows over
	c.Database = tmp
	c.Database.DataDir = t.TempDir()
	st, err := store.Open(context.Background(), c.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return &c, st
}

// The pinned key with another split is refused, on pin and on pairing, and leaves the split;
// the same key and split again succeeds.
func TestPinnedTopologyIsWriteOnce(t *testing.T) {
	cfgs, sts := replicas(t, 1)
	ctx := context.Background()
	k := newKey(t, 2, 3)
	if err := backup.PinRecoveryKey(ctx, cfgs[0], sts[0], k); err != nil {
		t.Fatal(err)
	}
	if err := backup.PinRecoveryKey(ctx, cfgs[0], sts[0], k); err != nil {
		t.Fatalf("the same key and split again: %v", err)
	}
	wider := k
	wider.TotalShares = 5
	if err := backup.PinRecoveryKey(ctx, cfgs[0], sts[0], wider); !errors.Is(err, backup.ErrTopologyPinned) || !errors.Is(err, fs.ErrExist) {
		t.Fatalf("another split: %v, want ErrTopologyPinned (fs.ErrExist)", err)
	}
	// The database refuses it too, even past the lock (a writer that lost it).
	if err := backup.Settings(ctx, sts[0].Settings()).Set("kyrecovery_total_shares", "5"); !errors.Is(err, backup.ErrTopologyPinned) {
		t.Fatalf("the topology row was overwritten: %v", err)
	}
	got, err := recoveryclient.LoadRecoveryKey(cfgs[0].Database.DataDir, backup.Settings(ctx, sts[0].Settings()))
	if err != nil || got.TotalShares != 3 {
		t.Fatalf("the split changed: %+v %v", got, err)
	}
}

// Two servers pin different keys at once: one wins, the other is refused, both read the
// winner, and no server holds a recovery.pub for the loser.
func TestConcurrentPinsOfDifferentKeys(t *testing.T) {
	cfgs, sts := replicas(t, 2)
	ctx := context.Background()
	keys := []recoveryclient.RecoveryKey{newKey(t, 2, 3), newKey(t, 2, 3)}
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); errs[i] = backup.PinRecoveryKey(ctx, cfgs[i], sts[i], keys[i]) }()
	}
	wg.Wait()
	winner := -1
	for i, err := range errs {
		switch {
		case err == nil:
			winner = i
		case !errors.Is(err, fs.ErrExist):
			t.Fatalf("pin %d: %v", i, err)
		}
	}
	if winner < 0 || errs[1-winner] == nil {
		t.Fatalf("want one win and one refusal: %v", errs)
	}
	for i := range 2 {
		_ = backup.SyncRecoveryKey(ctx, cfgs[i], sts[i])
		raw, err := os.ReadFile(recoveryclient.RecoveryKeyPath(cfgs[i].Database.DataDir))
		if err == nil && !bytes.Equal(raw, keys[winner].Public.Bytes()) {
			t.Fatalf("server %d holds the loser's recovery.pub", i)
		}
		k, err := recoveryclient.LoadRecoveryKey(cfgs[i].Database.DataDir, backup.Settings(ctx, sts[i].Settings()))
		if err == nil && k.Public.ID() != keys[winner].Public.ID() {
			t.Fatalf("server %d reads %s, not the winner", i, k.Public.ID())
		}
	}
}

// One server re-pairs back and forth while another reads: a read never pairs one URL with the
// other pairing's token (spec AC19).
func TestPairingReadsAreCoherent(t *testing.T) {
	cfgs, sts := replicas(t, 2)
	ctx := context.Background()
	sealer, err := backup.NewSealer(cfgs[0])
	if err != nil {
		t.Fatal(err)
	}
	k := newKey(t, 2, 3)
	if err := backup.PinRecoveryKey(ctx, cfgs[0], sts[0], k); err != nil {
		t.Fatal(err)
	}
	_ = backup.SyncRecoveryKey(ctx, cfgs[1], sts[1])
	readDir := cfgs[1].Database.DataDir
	if cfgs[1].Database.Driver == "sqlite" {
		readDir = cfgs[0].Database.DataDir // one SQLite server: one recovery.pub
	}
	pairs := map[string]string{"https://a.example": "token-a", "https://b.example": "token-b"}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 200 {
			url := "https://a.example"
			if i%2 == 1 {
				url = "https://b.example"
			}
			s := backup.Settings(ctx, sts[0].Settings())
			_ = recoveryclient.ClearPairing(s)
			if err := recoveryclient.StorePairing(s, sealer, url, pairs[url]); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	reads := 0
	for {
		select {
		case <-done:
			if reads == 0 {
				t.Fatal("no read overlapped the re-pairing")
			}
			return
		default:
		}
		p, err := recoveryclient.LoadPairing(readDir, backup.Settings(ctx, sts[1].Settings()), sealer)
		if err != nil {
			continue // between unpair and pair
		}
		reads++
		if pairs[p.URL] != p.Token {
			t.Fatalf("read paired %s with another pairing's token", p.URL)
		}
	}
}

// A read failure is an error, never "unpaired".
func TestPairedReportsAReadFailure(t *testing.T) {
	_, sts := replicas(t, 1)
	sts[0].Close()
	if paired, err := backup.Paired(backup.Settings(context.Background(), sts[0].Settings())); err == nil || paired {
		t.Fatalf("a closed store read as paired=%v err=%v", paired, err)
	}
}

// Snapshot scratch is 0700 and locked while in use; a crash leftover is removed at start,
// while a directory another process holds, a symlink and an unrelated directory stay.
func TestScratchIsPrivateLockedAndSwept(t *testing.T) {
	dir := t.TempDir()
	held, release, err := backup.NewScratch(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(held); fi.Mode().Perm() != 0o700 {
		t.Fatalf("scratch mode %o, want 700", fi.Mode().Perm())
	}
	leftover := filepath.Join(dir, "snapshot-crashed")
	if err := os.Mkdir(leftover, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(leftover, "kycalendar.db"), []byte("plaintext"), 0o600)
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(dir, "snapshot-link")); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(dir, "keep-me")
	_ = os.Mkdir(unrelated, 0o700)

	n, err := backup.RemoveStaleSnapshots(dir)
	if err != nil || n != 1 {
		t.Fatalf("removed %d (%v), want the one leftover", n, err)
	}
	for _, p := range []string{held, filepath.Join(dir, "snapshot-link"), unrelated, target} {
		if _, err := os.Lstat(p); err != nil {
			t.Errorf("%s was removed: %v", p, err)
		}
	}
	if _, err := os.Stat(leftover); !errors.Is(err, fs.ErrNotExist) {
		t.Error("the crash leftover survived")
	}
	release()
	if _, err := os.Stat(held); !errors.Is(err, fs.ErrNotExist) {
		t.Error("release left the scratch behind")
	}
}

// Collect leaves no scratch behind, on either driver; on PostgreSQL the database member is the
// verified SQLite snapshot and its drill passes.
func TestCollectSnapshotsEitherDriver(t *testing.T) {
	t.Setenv("KY_PORT", "8080")
	t.Setenv("KY_DB_DRIVER", "sqlite")
	cfg, st := anyDriver(t)
	sts := []store.Store{st}
	c := &store.Calendar{ID: "cal_1", OwnerKind: "user", OwnerID: "usr_a", Slug: "default", Name: "Cal"}
	if err := sts[0].Calendars().CreateCalendar(context.Background(), c, 0); err != nil {
		t.Fatal(err)
	}
	payload, err := backup.Collect(context.Background(), cfg, sts[0], "test")
	if err != nil {
		t.Fatal(err)
	}
	if n := payload.VerificationRecipe["calendar_count"]; n != int64(1) {
		t.Fatalf("calendar_count %v, want 1", n)
	}
	entries, _ := os.ReadDir(cfg.Database.DataDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "snapshot-") {
			t.Fatalf("scratch %s left behind", e.Name())
		}
	}
	root := backup.DrillRoot(cfg)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	result, err := recoveryclient.Drill(context.Background(), root, payload, backup.Checks)
	if err != nil || !result.Passed {
		t.Fatalf("drill: %+v %v", result, err)
	}
}

// forceSetting writes a settings row past the store, as anyone with database access can.
func forceSetting(cfg *config.Config, key, val string) error {
	db, err := sql.Open("pgx", cfg.Database.DSN)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO server_settings (key, value, updated_at) VALUES ($1, $2, now())
ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, val)
	return err
}

// anyDriver is one server on the test database's driver, with SQLite at the path backups
// require.
func anyDriver(t *testing.T) (*config.Config, store.Store) {
	t.Helper()
	cfgs, sts := replicas(t, 1)
	cfg := cfgs[0]
	if cfg.Database.Driver != "sqlite" {
		return cfg, sts[0]
	}
	cfg.Database.DSN = config.SQLiteDSN(filepath.Join(cfg.Database.DataDir, "kycalendar.db"))
	st, err := store.Open(context.Background(), cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return cfg, st
}
