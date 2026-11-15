package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// OutOfOffice is a user's out-of-office period as KyPost last set it (KyPost's
// docs/OUT_OF_OFFICE.md). Away false means no period: no event, no automatic answers.
type OutOfOffice struct {
	UserID                      string
	Revision                    int64
	Away, AllDay, Tentative     bool
	Start, End                  time.Time
	Zone                        string
	CommentLocal, CommentRemote string
}

// ActiveAt reports whether new invitations are answered automatically at now.
func (o *OutOfOffice) ActiveAt(now time.Time) bool {
	return o != nil && o.Away && o.Tentative && !now.Before(o.Start) && now.Before(o.End)
}

func (u *userStore) OutOfOffice(ctx context.Context, userID string) (*OutOfOffice, error) {
	o := OutOfOffice{UserID: userID}
	var start, end int64
	err := u.store.db.QueryRowContext(ctx, u.store.rebind(`SELECT revision, away, start_at, end_at, all_day, zone, tentative, comment_local, comment_remote
FROM kypost_out_of_office WHERE user_id = ?`), userID).Scan(&o.Revision, &o.Away, &start, &end, &o.AllDay, &o.Zone, &o.Tentative, &o.CommentLocal, &o.CommentRemote)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	o.Start, o.End = time.Unix(start, 0).UTC(), time.Unix(end, 0).UTC()
	return &o, nil
}

func (u *userStore) SetOutOfOffice(ctx context.Context, o *OutOfOffice) (bool, error) {
	res, err := u.store.db.ExecContext(ctx, u.store.rebind(`INSERT INTO kypost_out_of_office
 (user_id, revision, away, start_at, end_at, all_day, zone, tentative, comment_local, comment_remote)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (user_id) DO UPDATE SET revision = excluded.revision, away = excluded.away, start_at = excluded.start_at,
 end_at = excluded.end_at, all_day = excluded.all_day, zone = excluded.zone, tentative = excluded.tentative,
 comment_local = excluded.comment_local, comment_remote = excluded.comment_remote
WHERE kypost_out_of_office.revision <= excluded.revision`),
		o.UserID, o.Revision, o.Away, o.Start.Unix(), o.End.Unix(), o.AllDay, o.Zone, o.Tentative, o.CommentLocal, o.CommentRemote)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}
