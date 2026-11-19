package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// CoordinationStore is what PostgreSQL replicas share beyond the data itself: rate windows,
// spent CAPTCHA salts and KyPost event IDs on database time, and single-owner locks. SQLite
// keeps the first three in process memory and never calls them; its locks are flocks.
type CoordinationStore interface {
	// TakeAttempt counts one hit in key's window and reports whether it is within limit, with
	// the window's generation (its expiry, for ReleaseAttempt) and the time left in it.
	TakeAttempt(ctx context.Context, key string, limit int, window time.Duration) (gen int64, left time.Duration, ok bool, err error)
	// ReleaseAttempt hands one hit back to the window gen, never to a newer one.
	ReleaseAttempt(ctx context.Context, key string, gen int64) error
	// AttemptHits reads key's live window without counting.
	AttemptHits(ctx context.Context, key string) (hits int, left time.Duration, err error)
	// SpendChallenge records a CAPTCHA salt until expiresMs (unix milliseconds) and reports
	// whether it was unspent.
	SpendChallenge(ctx context.Context, salt string, expiresMs int64) (bool, error)
	// RecordReplay records event ID id for window as attempt and reports whether it was new; an
	// expired record is replaced.
	RecordReplay(ctx context.Context, id, attempt string, window time.Duration) (bool, error)
	// ForgetReplay releases id so the sender's retry is accepted.
	ForgetReplay(ctx context.Context, id string) error
	// UndoReplay releases id only if attempt wrote its record: an attempt that failed with an
	// unknown outcome never releases an earlier request's record.
	UndoReplay(ctx context.Context, id, attempt string) error
	// CleanExpired deletes expired windows, salts and event IDs.
	CleanExpired(ctx context.Context) error
	// WithLock runs work as the single holder of name and reports whether it won. work's
	// context ends when ownership can no longer be proven.
	WithLock(ctx context.Context, name string, work func(context.Context) error) (bool, error)
	// Serialized runs work while holding name, waiting for another holder; SQLite runs it.
	Serialized(ctx context.Context, name string, work func(context.Context) error) error
}

// Lock bounds (variables so tests can shorten them). Acquisition is a connection checkout
// plus pg_try_advisory_lock; a probe that does not answer within probeTimeout ends ownership
// at once, without waiting for the driver to give up on the connection.
var (
	lockAcquireTimeout = 2 * time.Second
	lockProbeInterval  = time.Second
	lockProbeTimeout   = time.Second
)

// ProbeBound is the longest a lost lock goes unnoticed: one probe interval plus its timeout.
// A new owner waits longer than this before its first job.
func ProbeBound() time.Duration { return lockProbeInterval + lockProbeTimeout }

// ErrLockAcquire is a lock attempt that could not reach the database in time.
var ErrLockAcquire = errors.New("store: lock acquisition timed out")

type coordinationStore struct{ store *SQLStore }

func (s *SQLStore) Coordination() CoordinationStore { return &coordinationStore{store: s} }

// nowMillis is database time: no replica's clock defines a shared window.
func (c *coordinationStore) nowMillis() string {
	if c.store.driver == "postgres" {
		return "CAST(EXTRACT(EPOCH FROM statement_timestamp()) * 1000 AS BIGINT)"
	}
	return "CAST(unixepoch('subsec') * 1000 AS INTEGER)"
}

func (c *coordinationStore) TakeAttempt(ctx context.Context, key string, limit int, window time.Duration) (int64, time.Duration, bool, error) {
	if key == "" || len(key) > 200 || limit < 1 || window < time.Millisecond || window > 24*time.Hour {
		return 0, 0, false, errors.New("store: invalid security window")
	}
	now := c.nowMillis()
	// hits stops at limit+1: a full window stays full without growing.
	q := `INSERT INTO security_windows (key, hits, expires_at) VALUES (?, 1, ` + now + ` + ?)
ON CONFLICT (key) DO UPDATE SET
hits = CASE WHEN security_windows.expires_at <= ` + now + ` THEN 1
            WHEN security_windows.hits <= ? THEN security_windows.hits + 1 ELSE security_windows.hits END,
expires_at = CASE WHEN security_windows.expires_at <= ` + now + ` THEN ` + now + ` + ? ELSE security_windows.expires_at END
RETURNING hits, expires_at, expires_at - ` + now
	var hits, gen, left int64
	ms := window.Milliseconds()
	err := c.store.db.QueryRowContext(ctx, c.store.rebind(q), key, ms, limit, ms).Scan(&hits, &gen, &left)
	if err != nil {
		return 0, 0, false, err
	}
	return gen, time.Duration(left) * time.Millisecond, hits <= int64(limit), nil
}

func (c *coordinationStore) ReleaseAttempt(ctx context.Context, key string, gen int64) error {
	_, err := c.store.db.ExecContext(ctx, c.store.rebind(`UPDATE security_windows SET hits = hits - 1 WHERE key = ? AND expires_at = ? AND hits > 0`), key, gen)
	return err
}

func (c *coordinationStore) AttemptHits(ctx context.Context, key string) (int, time.Duration, error) {
	var hits, left int64
	err := c.store.db.QueryRowContext(ctx, c.store.rebind(`SELECT hits, expires_at - `+c.nowMillis()+` FROM security_windows WHERE key = ? AND expires_at > `+c.nowMillis()), key).Scan(&hits, &left)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, nil
	}
	return int(hits), time.Duration(left) * time.Millisecond, err
}

func (c *coordinationStore) SpendChallenge(ctx context.Context, salt string, expiresMs int64) (bool, error) {
	if salt == "" || len(salt) > 64 {
		return false, errors.New("store: invalid challenge salt")
	}
	res, err := c.store.db.ExecContext(ctx, c.store.rebind(`INSERT INTO spent_challenges (salt, expires_at) VALUES (?, ?) ON CONFLICT (salt) DO NOTHING`), salt, expiresMs)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (c *coordinationStore) RecordReplay(ctx context.Context, id, attempt string, window time.Duration) (bool, error) {
	if id == "" || len(id) > 200 {
		return false, errors.New("store: invalid event ID")
	}
	now := c.nowMillis()
	// Insert, or take over an expired record; a live one stays and nothing is returned.
	q := `INSERT INTO kypost_replay (event_id, expires_at, attempt) VALUES (?, ` + now + ` + ?, ?)
ON CONFLICT (event_id) DO UPDATE SET expires_at = excluded.expires_at, attempt = excluded.attempt WHERE kypost_replay.expires_at <= ` + now + `
RETURNING event_id`
	var got string
	err := c.store.db.QueryRowContext(ctx, c.store.rebind(q), id, window.Milliseconds(), attempt).Scan(&got)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (c *coordinationStore) ForgetReplay(ctx context.Context, id string) error {
	_, err := c.store.db.ExecContext(ctx, c.store.rebind(`DELETE FROM kypost_replay WHERE event_id = ?`), id)
	return err
}

func (c *coordinationStore) UndoReplay(ctx context.Context, id, attempt string) error {
	_, err := c.store.db.ExecContext(ctx, c.store.rebind(`DELETE FROM kypost_replay WHERE event_id = ? AND attempt = ?`), id, attempt)
	return err
}

func (c *coordinationStore) CleanExpired(ctx context.Context) error {
	for _, table := range []string{"security_windows", "spent_challenges", "kypost_replay"} {
		if _, err := c.store.db.ExecContext(ctx, "DELETE FROM "+table+" WHERE expires_at < "+c.nowMillis()); err != nil {
			return err
		}
	}
	return nil
}

// advisoryKey names a session advisory lock in key space 3, scoped to the schema so tests and
// tenants sharing one PostgreSQL never contend. Transaction locks (lockKey) use another space.
const advisoryKey = `3, hashtext(current_schema() || ':' || $1)`

// WithLock elects one holder of name. PostgreSQL: a session advisory lock on a connection of
// its own, discarded afterwards so the lock never returns to the pool; use a direct
// connection or session pooling, never transaction pooling. Taking it is bounded by
// lockAcquireTimeout. While work runs the connection is probed every lockProbeInterval; a
// probe that fails or does not answer within lockProbeTimeout cancels work's context. Release
// order: cancel, wait for work, stop the probe, discard the connection. SQLite: a flock beside
// the database (host-local; a SQLite file is never shared by replicas).
func (c *coordinationStore) WithLock(ctx context.Context, name string, work func(context.Context) error) (bool, error) {
	if c.store.driver != "postgres" {
		return withFlock(ctx, c.store.lockPath, name, work)
	}
	acquire, cancelAcquire := context.WithTimeout(ctx, lockAcquireTimeout)
	defer cancelAcquire()
	conn, won, err := c.tryLock(acquire, name)
	if err != nil || !won {
		if err != nil && acquire.Err() != nil && ctx.Err() == nil {
			err = fmt.Errorf("%w: %v", ErrLockAcquire, err)
		}
		return false, err
	}
	defer discard(conn)

	owned, cancel := context.WithCancelCause(ctx)
	probeDone := make(chan struct{})
	go func() {
		defer close(probeDone)
		probe(owned, conn, cancel)
	}()
	err = work(owned)
	cancel(nil)
	<-probeDone
	return true, err
}

// tryLock checks out a connection and tries the lock, both under ctx. The driver may finish
// the query after ctx ends, so the result is awaited in the background and ctx alone decides.
func (c *coordinationStore) tryLock(ctx context.Context, name string) (*sql.Conn, bool, error) {
	type result struct {
		conn *sql.Conn
		won  bool
		err  error
	}
	done := make(chan result, 1)
	go func() {
		conn, err := c.store.db.Conn(ctx)
		if err != nil {
			done <- result{err: err}
			return
		}
		var won bool
		err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(`+advisoryKey+`)`, name).Scan(&won)
		done <- result{conn, won, err}
	}()
	select {
	case r := <-done:
		if r.err != nil || !r.won {
			if r.conn != nil {
				discard(r.conn)
			}
			return nil, false, r.err
		}
		return r.conn, true, nil
	case <-ctx.Done():
		go func() { // whatever arrives later is released with its connection
			if r := <-done; r.conn != nil {
				discard(r.conn)
			}
		}()
		return nil, false, ctx.Err()
	}
}

// ErrOwnershipLost is the cause of an owned context the probe ended.
var ErrOwnershipLost = errors.New("store: lock ownership lost")

func probe(owned context.Context, conn *sql.Conn, cancel context.CancelCauseFunc) {
	ticker := time.NewTicker(lockProbeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-owned.Done():
			return
		case <-ticker.C:
		}
		ping, stop := context.WithTimeout(owned, lockProbeTimeout)
		res := make(chan error, 1)
		go func() { res <- conn.PingContext(ping) }()
		var err error
		select {
		case err = <-res:
		case <-ping.Done():
			err = ping.Err()
		}
		stop()
		if err != nil && owned.Err() == nil {
			cancel(fmt.Errorf("%w: %v", ErrOwnershipLost, err))
			return
		}
	}
}

// discard closes conn's physical connection instead of pooling it: closing the session
// releases every lock it holds, even if an unlock would fail.
func discard(conn *sql.Conn) {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	_ = conn.Close()
}

func (c *coordinationStore) Serialized(ctx context.Context, name string, work func(context.Context) error) error {
	if c.store.driver != "postgres" {
		return work(ctx)
	}
	conn, err := c.store.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer discard(conn)
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(`+advisoryKey+`)`, name); err != nil {
		return err
	}
	return work(ctx)
}

// withFlock holds an exclusive flock on a persistent file beside the database for name.
// Closing the descriptor (or process exit) releases it; the inode is never unlinked.
func withFlock(ctx context.Context, dbPath, name string, work func(context.Context) error) (bool, error) {
	if dbPath == "" {
		return false, errors.New("store: SQLite locks need a database file")
	}
	path := fmt.Sprintf("%s.%x.lock", dbPath, sha256.Sum256([]byte(name)))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return false, err
	}
	defer f.Close()
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) {
			return false, nil
		}
		return false, err
	}
	return true, work(ctx)
}

// WithSQLiteInstance holds the server-instance lock on the SQLite database at path, so a
// second server or migrate-postgres on the same file is refused. CLI deposit, export,
// restore-reset and the user commands share the file without it.
func WithSQLiteInstance(ctx context.Context, path string, work func(context.Context) error) (bool, error) {
	canonical, err := canonicalPath(path)
	if err != nil {
		return false, err
	}
	return withFlock(ctx, canonical, "server-instance", work)
}

func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return abs, nil
}
