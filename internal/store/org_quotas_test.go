package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseOrgQuotas(t *testing.T) {
	show := func(q OrgQuotas) string {
		s := ""
		for _, p := range []*int64{q.MaxBytes, q.MaxBytesPerUser} {
			if p == nil {
				s += "- "
			} else {
				s += fmt.Sprint(*p, " ")
			}
		}
		if q.BookingPages == nil {
			return s + "-"
		}
		return s + fmt.Sprint(*q.BookingPages)
	}
	for raw, want := range map[string]string{
		`{}`: "- - -",
		`{"max_bytes":100,"max_bytes_per_user":10,"booking_pages":false}`: "100 10 false",
		`{"booking_pages":true}`:                         "- - true",
		`{"max_bytes":0,"max_bytes_per_user":-5}`:        "- - -",
		`{"max_bytes":1.5,"max_bytes_per_user":"10"}`:    "- - -",
		`{"max_bytes":null,"booking_pages":null}`:        "- - -",
		`{"booking_pages":"no","max_bytes":99999999999}`: "99999999999 - -",
		`not json`: "- - -",
	} {
		if got := show(parseOrgQuotas(raw)); got != want {
			t.Errorf("%s: %s, want %s", raw, got, want)
		}
	}
}

// quotaWorld is org-a, mirrored with quotas, and an owner with a calendar in it.
type quotaWorld struct {
	s   *SQLStore
	cal map[string]string // owner -> calendar ID
}

func newQuotaWorld(t *testing.T, s *SQLStore, quotas string, owners ...string) *quotaWorld {
	t.Helper()
	ctx := context.Background()
	if err := s.ApplyOrgs(ctx, "org-staff", []MirroredOrg{{ID: "org-a", Status: "active", Revision: 1, Enabled: true, Quotas: quotas}}, 1); err != nil {
		t.Fatal(err)
	}
	w := &quotaWorld{s: s, cal: map[string]string{}}
	for _, name := range owners {
		org := OrgID("org-a")
		if strings.HasPrefix(name, "default-") {
			org = DefaultOrg
		}
		u := &User{ID: "usr_" + name, Username: name, Email: name + "@example.com", Role: "user", Status: "active", SSOProvider: "local", OrgID: org}
		if err := s.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
		c := &Calendar{ID: "cal_" + name, OwnerKind: "user", OwnerID: u.ID, Slug: "default", Name: "C"}
		if err := s.Calendars().CreateCalendar(ctx, c, 0); err != nil {
			t.Fatal(err)
		}
		w.cal[name] = c.ID
	}
	return w
}

// object is an event of exactly n bytes.
func object(cal, name string, n int) *CalendarObject {
	head, tail := "BEGIN:VCALENDAR\r\nX-PAD:", "\r\nEND:VCALENDAR\r\n"
	return &CalendarObject{CalendarID: cal, Name: name + ".ics", Component: "VEVENT", UID: name,
		Data: []byte(head + strings.Repeat("a", n-len(head)-len(tail)) + tail)}
}

var installLimits = OwnerLimits{MaxObjects: 100, MaxBytes: 1 << 20}

func (w *quotaWorld) put(st Store, owner, name string, n int) error {
	_, err := st.Calendars().PutObject(context.Background(), object(w.cal[owner], name, n), "", false, installLimits)
	return err
}

// max_bytes bounds the org's stored objects and pending messages together, max_bytes_per_user
// replaces the install's per-owner cap, a lower quota refuses growth but never shrinking, and
// another org is not bounded by it.
func TestOrgQuotasBoundTheOrg(t *testing.T) {
	eachDialect(t, func(t *testing.T, s *SQLStore) {
		ctx := context.Background()
		w := newQuotaWorld(t, s, `{"max_bytes":3000,"max_bytes_per_user":2500}`, "alice", "bob", "default-carol")
		if err := w.put(s, "alice", "a1", 2000); err != nil {
			t.Fatal(err)
		}
		if err := w.put(s, "alice", "a2", 1000); !errors.Is(err, ErrQuotaExceeded) {
			t.Fatalf("alice over max_bytes_per_user (2500), under the install's 1 MiB: %v", err)
		}
		if err := w.put(s, "bob", "b1", 900); err != nil {
			t.Fatal(err)
		}
		if err := w.put(s, "bob", "b2", 200); !errors.Is(err, ErrQuotaExceeded) {
			t.Fatalf("org-a at 3100 of 3000: %v", err)
		}
		// A pending message counts: 2900 stored + 50 + a 100-byte body is over.
		msg := &OutboxMessage{ID: "m1", DedupeKey: "m1", SenderID: "usr_bob", Method: "REQUEST", Recipient: "mailto:x@example.net", Body: make([]byte, 100)}
		_, err := s.Calendars().PutObjectPlanned(ctx, object(w.cal["bob"], "b3", 50), "", false, installLimits,
			func([]byte) ([]byte, []*OutboxMessage, error) { return nil, []*OutboxMessage{msg}, nil })
		if !errors.Is(err, ErrQuotaExceeded) {
			t.Fatalf("a pending body past max_bytes: %v", err)
		}
		if err := w.put(s, "default-carol", "c1", 5000); err != nil {
			t.Fatalf("the default org is not org-a's: %v", err)
		}

		// A lower quota: alice may shrink and delete, never grow.
		if err := s.ApplyOrgs(ctx, "org-staff", []MirroredOrg{{ID: "org-a", Status: "active", Revision: 2, Enabled: true, Quotas: `{"max_bytes":1000}`}}, 2); err != nil {
			t.Fatal(err)
		}
		if err := w.put(s, "alice", "a1", 1500); err != nil {
			t.Fatalf("shrinking over quota: %v", err)
		}
		if err := w.put(s, "alice", "a1", 1600); !errors.Is(err, ErrQuotaExceeded) {
			t.Fatalf("growing over quota: %v", err)
		}
		if err := s.Calendars().DeleteObjectPlanned(ctx, w.cal["alice"], "a1.ics", "", installLimits, nil); err != nil {
			t.Fatalf("deleting over quota: %v", err)
		}
		// Absent keys fall back to the install's limits: 1 MiB per owner, no org total.
		if err := s.ApplyOrgs(ctx, "org-staff", []MirroredOrg{{ID: "org-a", Status: "active", Revision: 3, Enabled: true, Quotas: `{}`}}, 3); err != nil {
			t.Fatal(err)
		}
		if err := w.put(s, "alice", "a4", 500<<10); err != nil {
			t.Fatalf("no org quotas: %v", err)
		}
		if err := w.put(s, "alice", "a5", 600<<10); !errors.Is(err, ErrQuotaExceeded) {
			t.Fatalf("the install's per-owner cap: %v", err)
		}

		// A subscription refresh is bounded too.
		if err := s.ApplyOrgs(ctx, "org-staff", []MirroredOrg{{ID: "org-a", Status: "active", Revision: 4, Enabled: true, Quotas: `{"max_bytes":1000}`}}, 4); err != nil {
			t.Fatal(err)
		}
		feed := &Calendar{ID: "cal_feed", OwnerKind: "user", OwnerID: "usr_bob", Slug: "feed", Name: "F"}
		if err := s.Calendars().CreateSubscribedCalendar(ctx, feed, &Subscription{URLSealed: "x", URLHost: "example.net"}, 0, 0); err != nil {
			t.Fatal(err)
		}
		if err := s.Calendars().ReplaceSubscriptionObjects(ctx, "cal_feed", []*CalendarObject{object("", "f1", 2000)}, installLimits); !errors.Is(err, ErrQuotaExceeded) {
			t.Fatalf("a feed past max_bytes: %v", err)
		}
	})
}

// A delete whose queued messages outweigh the object it removes grows the org, and is refused
// at max_bytes like any other growth; a delete that shrinks the org passes.
func TestADeleteThatGrowsTheOrgIsRefused(t *testing.T) {
	eachDialect(t, func(t *testing.T, s *SQLStore) {
		ctx := context.Background()
		w := newQuotaWorld(t, s, `{"max_bytes":3000}`, "alice")
		if err := w.put(s, "alice", "a1", 2500); err != nil {
			t.Fatal(err)
		}
		if err := w.put(s, "alice", "a2", 200); err != nil {
			t.Fatal(err)
		}
		notify := func(n int) Planner {
			msg := &OutboxMessage{ID: fmt.Sprint("d", n), DedupeKey: fmt.Sprint("d", n), SenderID: "usr_alice", Method: "CANCEL", Recipient: "mailto:x@example.net", Body: make([]byte, n)}
			return func([]byte) ([]byte, []*OutboxMessage, error) { return nil, []*OutboxMessage{msg}, nil }
		}
		if err := s.Calendars().DeleteObjectPlanned(ctx, w.cal["alice"], "a2.ics", "", installLimits, notify(600)); !errors.Is(err, ErrQuotaExceeded) {
			t.Fatalf("a delete queueing 600 bytes for 200 at 2700 of 3000: %v", err)
		}
		if err := s.Calendars().DeleteObjectPlanned(ctx, w.cal["alice"], "a2.ics", "", installLimits, notify(100)); err != nil {
			t.Fatalf("a delete that shrinks the org: %v", err)
		}
	})
}

// A missing org_usage row refuses the write: an org is never unmetered.
func TestOrgWithoutUsageRowRefusesWrites(t *testing.T) {
	eachDialect(t, func(t *testing.T, s *SQLStore) {
		w := newQuotaWorld(t, s, `{"max_bytes":3000}`, "alice")
		if _, err := s.db.ExecContext(context.Background(), s.rebind(`DELETE FROM org_usage WHERE org_id = ?`), "org-a"); err != nil {
			t.Fatal(err)
		}
		if err := w.put(s, "alice", "a1", 100); err == nil || errors.Is(err, ErrQuotaExceeded) {
			t.Fatalf("no usage row: %v", err)
		}
	})
}

// Two replicas writing at once with room for one: exactly one succeeds (calendar-total holds
// both, so the second sees the first's bytes).
func TestOrgQuotaAdmitsOneOfTwoParallelWriters(t *testing.T) {
	target := pgTarget(t)
	ctx := context.Background()
	var replicas [2]*SQLStore
	for i := range replicas {
		st, err := openPostgres(ctx, target, 4, 4, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		replicas[i] = st.(*SQLStore)
	}
	w := newQuotaWorld(t, replicas[0], `{"max_bytes":3000}`, "alice", "bob")
	if err := w.put(replicas[0], "alice", "a1", 1500); err != nil {
		t.Fatal(err)
	}
	for round := range 5 {
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for i, st := range replicas {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs[i] = w.put(st, []string{"alice", "bob"}[i], fmt.Sprintf("r%d-%d", round, i), 1000)
			}()
		}
		wg.Wait()
		ok := 0
		for _, err := range errs {
			if err == nil {
				ok++
			} else if !errors.Is(err, ErrQuotaExceeded) {
				t.Fatal(err)
			}
		}
		if ok != 1 {
			t.Fatalf("round %d: %d of two writers fit one free slot: %v", round, ok, errs)
		}
		// Free the slot again for the next round.
		for i := range replicas {
			_ = replicas[0].Calendars().DeleteObject(ctx, w.cal[[]string{"alice", "bob"}[i]], fmt.Sprintf("r%d-%d.ics", round, i), "")
		}
	}
}

// orgUsed adds the org's pending bodies, reservations and its senders' cancellation growth to
// org_usage, and nothing of another org's.
func TestOrgUsedCountsReservationsAndGrowth(t *testing.T) {
	eachDialect(t, func(t *testing.T, s *SQLStore) {
		ctx := context.Background()
		w := newQuotaWorld(t, s, `{}`, "alice", "bob", "default-carol")
		event := func(organizer, extra string) string {
			return "BEGIN:VEVENT\r\nUID:m-" + organizer + "\r\nDTSTAMP:20261101T000000Z\r\nDTSTART:20261210T090000Z\r\n" + extra +
				"ORGANIZER:mailto:" + organizer + "@example.com\r\nATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:bob@example.com\r\nSEQUENCE:1\r\nEND:VEVENT\r\n"
		}
		wrap := func(method, events string) []byte {
			return []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\n" + method + events + "END:VCALENDAR\r\n")
		}
		// alice (org-a) and carol (the default org) each organize a daily meeting whose copy bob
		// holds, sponsored by its organizer, with a reservation on the source and one occurrence's
		// CANCEL pending.
		var pending int64
		for _, organizer := range []string{"alice", "default-carol"} {
			data := wrap("", event(organizer, "RRULE:FREQ=DAILY;COUNT=10\r\n"))
			cancel := wrap("METHOD:CANCEL\r\n", strings.Replace(event(organizer, "RECURRENCE-ID:20261211T090000Z\r\nSTATUS:CANCELLED\r\n"), "DTSTART:20261210", "DTSTART:20261211", 1))
			if organizer == "alice" {
				pending = int64(len(cancel))
			}
			row := &OutboxMessage{ID: "c-" + organizer, DedupeKey: "c-" + organizer, SenderID: "usr_" + organizer, Method: "CANCEL", Recipient: "mailto:bob@example.com", Body: cancel}
			src := &CalendarObject{CalendarID: w.cal[organizer], Name: "m.ics", Component: "VEVENT", UID: "m-" + organizer, Data: data}
			if _, err := s.Calendars().PutObjectPlanned(ctx, src, "", false, OwnerLimits{}, func([]byte) ([]byte, []*OutboxMessage, error) { return nil, []*OutboxMessage{row}, nil }); err != nil {
				t.Fatal(err)
			}
			cp := &CalendarObject{CalendarID: w.cal["bob"], Name: organizer + ".ics", Component: "VEVENT", UID: "m-" + organizer, Data: data}
			if _, err := s.Calendars().PutObject(ctx, cp, "", false, OwnerLimits{}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.ExecContext(ctx, s.rebind(`INSERT INTO cancellation_reservations (calendar_id, name, sender_id, bytes) VALUES (?, 'm.ics', ?, 700)`), w.cal[organizer], "usr_"+organizer); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.ExecContext(ctx, s.rebind(`UPDATE calendar_objects SET invitation_sender = ?, invitation_recipient = 'usr_bob' WHERE calendar_id = ? AND name = ?`), "usr_"+organizer, w.cal["bob"], organizer+".ics"); err != nil {
				t.Fatal(err)
			}
		}
		tx, err := s.beginTx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		c := &calendarStore{store: s}
		growth, err := c.cancellationGrowth(ctx, tx, "", "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if growth["usr_alice"] <= 0 || growth["usr_default-carol"] <= 0 {
			t.Fatalf("the fixture has no cancellation growth: %v", growth)
		}
		var stored int64
		if err := tx.QueryRowContext(ctx, s.rebind(`SELECT bytes FROM org_usage WHERE org_id = ?`), "org-a").Scan(&stored); err != nil {
			t.Fatal(err)
		}
		used, err := c.orgUsed(ctx, tx, "org-a")
		if err != nil {
			t.Fatal(err)
		}
		if want := stored + pending + 700 + growth["usr_alice"]; used != want {
			t.Fatalf("org-a used %d, want %d stored + %d pending + 700 reserved + %d growth", used, stored, pending, growth["usr_alice"])
		}
	})
}
