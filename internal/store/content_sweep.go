package store

// The sweep (content encryption E12, CE6): seals the legacy rows of every calendar that has a
// key, in place, with no user present. It needs only the calendar's public key.

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Busnes-app/kycalendar/internal/contentkey"
	"github.com/Busnes-app/kycalendar/internal/skeleton"
)

// SweepBatch is how many legacy rows one sweep transaction seals.
const SweepBatch = 200

// sweepHook, when set by a test, runs after each row is sealed in the batch's transaction; an
// error aborts the batch as a crash would.
var sweepHook func(sealed int) error

// sweepReadHook, when set by a test, runs between a batch's read and its transaction.
var sweepReadHook func()

type sweepRow struct {
	cal, name, uid, etag string
	data                 []byte
	version              int64
}

// SweepCursor is where a sweep pass has reached: the last row a batch read.
type SweepCursor struct{ Calendar, Name string }

// SweepOnce seals up to limit legacy rows of keyed calendars after cursor in one transaction. It
// returns how many it sealed and the cursor after the rows it read; done is true when it read
// fewer than limit. A row it cannot seal byte for byte stays legacy and is passed over, so it
// never blocks the rows after it. It is resumable (each batch commits on its own,
// and a batch that dies changes nothing), idempotent (only rows still legacy and unchanged
// since they were read are touched) and non-destructive: a row's plaintext is replaced only
// after its sealed base was opened again and merges back to the exact original bytes. ETags
// and counted usage stay as they were, so clients refetch nothing and quotas do not move.
func (c contentStore) SweepOnce(ctx context.Context, after SweepCursor, limit int) (sealedN int, next SweepCursor, done bool, err error) {
	s := c.s
	rows, err := s.db.QueryContext(ctx, s.rebind(`SELECT o.calendar_id, o.name, o.uid, o.etag, o.data, o.parts_version FROM calendar_objects o
WHERE o.sealed IS NULL AND EXISTS (SELECT 1 FROM calendar_keys k WHERE k.calendar_id = o.calendar_id)
 AND (o.calendar_id > ? OR (o.calendar_id = ? AND o.name > ?))
ORDER BY o.calendar_id, o.name LIMIT ?`), after.Calendar, after.Calendar, after.Name, limit)
	if err != nil {
		return 0, after, false, err
	}
	var batch []sweepRow
	for rows.Next() {
		var r sweepRow
		if err := rows.Scan(&r.cal, &r.name, &r.uid, &r.etag, &r.data, &r.version); err != nil {
			rows.Close()
			return 0, after, false, err
		}
		batch = append(batch, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(batch) == 0 {
		return 0, after, err == nil, err
	}
	if sweepReadHook != nil {
		sweepReadHook()
	}
	n, err := c.sweepBatch(ctx, batch)
	if err != nil {
		return 0, after, false, err
	}
	last := batch[len(batch)-1]
	return n, SweepCursor{last.cal, last.name}, len(batch) < limit, nil
}

func (c contentStore) sweepBatch(ctx context.Context, batch []sweepRow) (int, error) {
	s := c.s
	tx, err := s.beginTx(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	keys := map[string]*contentkey.WorkerKey{} // one worker key per calendar per batch (E4)
	versions := map[string]uint32{}
	now := time.Now().UTC()
	n := 0
	for _, r := range batch {
		wk := keys[r.cal]
		if wk == nil {
			version, pub, keyed, err := s.currentCalendarKey(ctx, tx, r.cal)
			if err != nil {
				return 0, err
			}
			if !keyed {
				continue
			}
			if wk, err = contentkey.NewWorkerKey(); err != nil {
				return 0, err
			}
			wrapped, err := wk.WrapFor(r.cal, version, pub)
			if err != nil {
				return 0, err
			}
			if _, err := tx.ExecContext(ctx, s.rebind(`INSERT INTO worker_keys (id, calendar_id, wrapped, created_at, ended_at, key_version) VALUES (?, ?, ?, ?, ?, ?)`),
				wk.ID, r.cal, wrapped, now, now, version); err != nil {
				return 0, err
			}
			keys[r.cal], versions[r.cal] = wk, version
		}
		ok, err := s.sweepRow(ctx, tx, wk, versions[r.cal], r)
		if err != nil {
			return 0, err
		}
		if !ok {
			continue
		}
		n++
		if sweepHook != nil {
			if err := sweepHook(n); err != nil {
				return 0, err
			}
		}
	}
	return n, tx.Commit()
}

// sweepRow seals one legacy row; false when the row is not one object the skeleton can carry
// (it stays legacy and readable, as today) or changed since it was read.
func (s *SQLStore) sweepRow(ctx context.Context, tx *sql.Tx, wk *contentkey.WorkerKey, version uint32, r sweepRow) (bool, error) {
	skel, err := skeleton.Split(r.data)
	if err != nil {
		return false, nil // unparseable legacy bytes are served as stored; never rewritten
	}
	seq := r.version + 1
	part, err := wk.Seal(contentkey.Part{Kind: contentkey.PartBase, Seq: uint64(seq)}, r.cal, version, r.uid, r.data)
	if err != nil {
		return false, err
	}
	// Verify before replacing: the part opens, and merges with the skeleton to the original.
	opened, err := wk.Open(part, r.cal, r.uid)
	if err != nil || !bytes.Equal(opened, r.data) {
		return false, errors.New("store: sweep could not verify a sealed part")
	}
	merged, err := skeleton.Merge(skel, []skeleton.Layer{{Base: true, Seq: part.Seq, Plaintext: opened}})
	if err != nil || !bytes.Equal(merged, r.data) {
		return false, nil // would not come back byte for byte: leave it legacy
	}
	sealed, err := contentkey.EncodeParts([]contentkey.Part{part})
	if err != nil {
		return false, err
	}
	res, err := tx.ExecContext(ctx, s.rebind(`UPDATE calendar_objects SET data = ?, sealed = ?, parts_version = ?, plain_size = ?
WHERE calendar_id = ? AND name = ? AND sealed IS NULL AND etag = ?`), skel, sealed, seq, int64(len(r.data)), r.cal, r.name, r.etag)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	return affected == 1, err
}

// LegacyRows counts rows still in plaintext in calendars that have a key: what the sweep has
// left, and what the admin page will show.
func (c contentStore) LegacyRows(ctx context.Context) (int, error) {
	var n int
	err := c.s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM calendar_objects o WHERE o.sealed IS NULL
 AND EXISTS (SELECT 1 FROM calendar_keys k WHERE k.calendar_id = o.calendar_id)`).Scan(&n)
	return n, err
}
