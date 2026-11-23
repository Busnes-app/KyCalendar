package store

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The public grant writer currently accepts group calendars only. These fixtures model
// restored shared personal rows for D16 without opening a new personal-sharing surface.
func erasureWorld(t *testing.T, s *SQLStore) *quotaWorld {
	t.Helper()
	ctx := context.Background()
	w := newQuotaWorld(t, s, `{}`, "erased", "older", "newer", "reader", "default-outsider")
	if err := s.Settings().SetSetting(ctx, TenancySetting, "multi"); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"reader", "older", "newer", "erased"} {
		if _, err := s.db.ExecContext(ctx, s.rebind(`UPDATE users SET created_at = ? WHERE id = ?`), time.Unix(int64(i+1), 0).UTC(), "usr_"+name); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.put(s, "erased", "event", 100); err != nil {
		t.Fatal(err)
	}
	return w
}

func erasureShare(t *testing.T, s *SQLStore, cal, member, role string) {
	t.Helper()
	ctx := context.Background()
	id := "g_" + member + "_" + role
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO groups (id,display_name,org_id,created_at,updated_at) VALUES (?,?,?,?,?) ON CONFLICT DO NOTHING`, []any{id, id, "org-a", time.Now().UTC(), time.Now().UTC()}},
		{`INSERT INTO group_members (group_id,user_id,org_id) VALUES (?,?,?) ON CONFLICT DO NOTHING`, []any{id, "usr_" + member, "org-a"}},
		{`INSERT INTO calendar_grants (calendar_id,group_id,role,org_id) VALUES (?,?,?,?) ON CONFLICT DO NOTHING`, []any{cal, id, role, "org-a"}},
	} {
		if _, err := s.db.ExecContext(ctx, s.rebind(q.sql), q.args...); err != nil {
			t.Fatal(err)
		}
	}
}

func TestErasureTransfersSharedCalendars(t *testing.T) {
	for _, tc := range []struct{ name, olderRole, newerRole, want string }{
		{"manager before editor", "editor", "manager", "newer"},
		{"oldest manager", "manager", "manager", "older"},
		{"editor before reader", "editor", "reader", "older"},
		{"reader fallback", "reader", "reader", "reader"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eachDialect(t, func(t *testing.T, s *SQLStore) {
				ctx := context.Background()
				w := erasureWorld(t, s)
				cal := w.cal["erased"]
				// A distinct slug is necessary under the existing per-owner uniqueness constraint.
				if _, err := s.db.ExecContext(ctx, s.rebind(`UPDATE calendars SET slug='shared' WHERE id=?`), cal); err != nil {
					t.Fatal(err)
				}
				erasureShare(t, s, cal, "older", tc.olderRole)
				erasureShare(t, s, cal, "newer", tc.newerRole)
				erasureShare(t, s, cal, "reader", "reader")
				// One member's strongest of several grants decides their rank.
				erasureShare(t, s, cal, "newer", "reader")
				grantsBefore, err := s.Calendars().ListGrants(ctx, cal)
				if err != nil {
					t.Fatal(err)
				}
				before, err := s.Calendars().GetCalendarByID(ctx, cal)
				if err != nil {
					t.Fatal(err)
				}
				obj, err := s.Calendars().GetObject(ctx, cal, "event.ics")
				if err != nil {
					t.Fatal(err)
				}
				// The recipient already has their one allowed personal calendar.
				if err := s.Calendars().CreateCalendar(ctx, &Calendar{ID: "over-cap", OwnerKind: "user", OwnerID: "usr_" + tc.want, Slug: "extra"}, 1); !errors.Is(err, ErrQuotaExceeded) {
					t.Fatalf("cap fixture: %v", err)
				}
				if err := s.Users().DeleteUser(ctx, "usr_erased"); err != nil {
					t.Fatal(err)
				}
				after, err := s.Calendars().GetCalendarByID(ctx, cal)
				if err != nil {
					t.Fatal(err)
				}
				if after.OwnerID != "usr_"+tc.want {
					t.Fatalf("owner %s, want %s", after.OwnerID, tc.want)
				}
				// All calendar fields apart from owner, including org, slug and sync sequence, survive.
				before.OwnerID = after.OwnerID
				if !reflect.DeepEqual(before, after) {
					t.Fatalf("calendar changed: %+v -> %+v", before, after)
				}
				got, err := s.Calendars().GetObject(ctx, cal, "event.ics")
				if err != nil {
					t.Fatal(err)
				}
				if got.ETag != obj.ETag || !bytes.Equal(got.Data, obj.Data) || !got.ModifiedAt.Equal(obj.ModifiedAt) {
					t.Fatal("handover rewrote the object")
				}
				grants, err := s.Calendars().ListGrants(ctx, cal)
				if err != nil || !reflect.DeepEqual(grantsBefore, grants) {
					t.Fatalf("grants: %v %v", grants, err)
				}
				audit, _, err := s.Audit().ListOrgAuditRecords(ctx, "org-a", 0, 100)
				if err != nil {
					t.Fatal(err)
				}
				n := 0
				for _, r := range audit {
					if r.Action == "calendar.owner_transferred" {
						n++
						if r.Resource != cal || r.UserID != after.OwnerID || r.Details != "from=usr_erased to="+after.OwnerID {
							t.Fatalf("audit %+v", r)
						}
					}
				}
				if n != 1 {
					t.Fatalf("%d handover audit rows", n)
				}
				if err := s.Users().DeleteUser(ctx, "usr_erased"); !errors.Is(err, ErrNotFound) {
					t.Fatalf("replay: %v", err)
				}
			})
		})
	}
}

func TestErasureKeepsOtherMembersContent(t *testing.T) {
	eachDialect(t, func(t *testing.T, s *SQLStore) {
		ctx := context.Background()
		w := erasureWorld(t, s)
		// A manager who is inactive or an administrator cannot inherit.
		for _, name := range []string{"older", "newer"} {
			erasureShare(t, s, w.cal["erased"], name, "manager")
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE users SET status='inactive' WHERE id='usr_older'`); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE users SET role='admin' WHERE id='usr_newer'`); err != nil {
			t.Fatal(err)
		}
		// No eligible shared member: private calendar goes. Other calendars and reply bodies stay.
		if err := w.put(s, "reader", "reply", 100); err != nil {
			t.Fatal(err)
		}
		reply, err := s.Calendars().GetObject(ctx, w.cal["reader"], "reply.ics")
		if err != nil {
			t.Fatal(err)
		}
		group := &Calendar{ID: "team", OwnerKind: "group", OwnerID: "team", Slug: "team", Name: "Team"}
		if err := s.Calendars().CreateCalendar(ctx, group, 0); err != nil {
			t.Fatal(err)
		}
		if err := s.Users().DeleteUser(ctx, "usr_erased"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Calendars().GetCalendarByID(ctx, w.cal["erased"]); !errors.Is(err, ErrNotFound) {
			t.Fatalf("private calendar: %v", err)
		}
		if _, err := s.Calendars().GetCalendarByID(ctx, "team"); err != nil {
			t.Fatal(err)
		}
		got, err := s.Calendars().GetObject(ctx, w.cal["reader"], "reply.ics")
		if err != nil || got.ETag != reply.ETag {
			t.Fatalf("other member's reply: %v %v", got, err)
		}
	})
}

func TestErasureCollisionRollsBack(t *testing.T) {
	eachDialect(t, func(t *testing.T, s *SQLStore) {
		ctx := context.Background()
		w := erasureWorld(t, s)
		erasureShare(t, s, w.cal["erased"], "older", "manager")
		// Both calendars still have slug default. Only owner_id may change under D16.
		if err := s.Users().DeleteUser(ctx, "usr_erased"); err == nil {
			t.Fatal("slug collision was silently accepted")
		}
		if _, err := s.Users().GetUserByID(ctx, "usr_erased"); err != nil {
			t.Fatalf("erasure partly committed: %v", err)
		}
		cal, err := s.Calendars().GetCalendarByID(ctx, w.cal["erased"])
		if err != nil || cal.OwnerID != "usr_erased" {
			t.Fatalf("calendar lost: %+v %v", cal, err)
		}
		records, _, err := s.Audit().ListOrgAuditRecords(ctx, "org-a", 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range records {
			if r.Action == "calendar.owner_transferred" {
				t.Fatal("rolled-back transfer was audited")
			}
		}
	})
}

func TestSingleTenantErasureStillDeletesPersonalCalendars(t *testing.T) {
	eachDialect(t, func(t *testing.T, s *SQLStore) {
		ctx := context.Background()
		w := erasureWorld(t, s)
		erasureShare(t, s, w.cal["erased"], "older", "manager")
		if err := s.Settings().SetSetting(ctx, TenancySetting, "single"); err != nil {
			t.Fatal(err)
		}
		if err := s.Users().DeleteUser(ctx, "usr_erased"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Calendars().GetCalendarByID(ctx, w.cal["erased"]); !errors.Is(err, ErrNotFound) {
			t.Fatalf("single-tenant behavior changed: %v", err)
		}
	})
}

func TestErasureTransferTiesUseID(t *testing.T) {
	eachDialect(t, func(t *testing.T, s *SQLStore) {
		ctx := context.Background()
		w := erasureWorld(t, s)
		for _, name := range []string{"older", "newer"} {
			erasureShare(t, s, w.cal["erased"], name, "manager")
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE users SET created_at=(SELECT created_at FROM users WHERE id='usr_older') WHERE id='usr_newer'`); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx, s.rebind(`UPDATE calendars SET slug=? WHERE id=?`), "shared", w.cal["erased"]); err != nil {
			t.Fatal(err)
		}
		if err := s.Users().DeleteUser(ctx, "usr_erased"); err != nil {
			t.Fatal(err)
		}
		cal, err := s.Calendars().GetCalendarByID(ctx, w.cal["erased"])
		if err != nil || !strings.HasSuffix(cal.OwnerID, "newer") {
			t.Fatalf("tie owner: %+v %v", cal, err)
		}
	})
}

func TestErasureAuditFailureRollsBack(t *testing.T) {
	eachDialect(t, func(t *testing.T, s *SQLStore) {
		ctx := context.Background()
		w := erasureWorld(t, s)
		erasureShare(t, s, w.cal["erased"], "older", "manager")
		if _, err := s.db.ExecContext(ctx, s.rebind(`UPDATE calendars SET slug='shared' WHERE id=?`), w.cal["erased"]); err != nil {
			t.Fatal(err)
		}
		trigger := `CREATE TRIGGER refuse_handover_audit BEFORE INSERT ON audit_records WHEN NEW.action='calendar.owner_transferred' BEGIN SELECT RAISE(ABORT,'audit unavailable'); END;`
		if s.driver == "postgres" {
			trigger = `CREATE FUNCTION refuse_handover_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='calendar.owner_transferred' THEN RAISE EXCEPTION 'audit unavailable'; END IF; RETURN NEW; END; $$; CREATE TRIGGER refuse_handover_audit BEFORE INSERT ON audit_records FOR EACH ROW EXECUTE FUNCTION refuse_handover_audit();`
		}
		if _, err := s.db.ExecContext(ctx, trigger); err != nil {
			t.Fatal(err)
		}
		if err := s.Users().DeleteUser(ctx, "usr_erased"); err == nil {
			t.Fatal("erased despite audit failure")
		}
		if _, err := s.Users().GetUserByID(ctx, "usr_erased"); err != nil {
			t.Fatalf("user lost: %v", err)
		}
		cal, err := s.Calendars().GetCalendarByID(ctx, w.cal["erased"])
		if err != nil || cal.OwnerID != "usr_erased" {
			t.Fatalf("owner changed: %+v %v", cal, err)
		}
	})
}

func TestConcurrentErasuresKeepSharedCalendars(t *testing.T) {
	eachDialect(t, func(t *testing.T, s *SQLStore) {
		ctx := context.Background()
		w := erasureWorld(t, s)
		for _, name := range []string{"erased", "older"} {
			cal := w.cal[name]
			if _, err := s.db.ExecContext(ctx, s.rebind(`UPDATE calendars SET slug=? WHERE id=?`), name, cal); err != nil {
				t.Fatal(err)
			}
			erasureShare(t, s, cal, "reader", "manager")
		}
		start := make(chan struct{})
		done := make(chan error, 2)
		for _, name := range []string{"erased", "older"} {
			go func(name string) { <-start; done <- s.Users().DeleteUser(ctx, "usr_"+name) }(name)
		}
		close(start)
		for range 2 {
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}
		for _, name := range []string{"erased", "older"} {
			cal, err := s.Calendars().GetCalendarByID(ctx, w.cal[name])
			if err != nil || cal.OwnerID != "usr_reader" {
				t.Fatalf("shared calendar lost: %+v %v", cal, err)
			}
		}
	})
}
