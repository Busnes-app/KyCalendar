package api_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func cancellationWorld(t *testing.T, cap, total int64) (*schedWorld, *sql.DB) {
	t.Helper()
	t.Setenv("KYCALENDAR_SCHEDULING", "true")
	t.Setenv("KY_CALENDAR_MAX_BYTES_PER_USER", fmt.Sprint(cap))
	t.Setenv("KY_CALENDAR_MAX_BYTES_TOTAL", fmt.Sprint(total))
	srv, st, cfg := setupTestServer(t)
	w := &schedWorld{t: t, srv: srv, st: st, ts: httptest.NewServer(srv), tokens: map[string]string{}}
	t.Cleanup(w.ts.Close)
	for _, name := range []string{"alice", "bob", "carol", "dan"} {
		w.tokens[name] = schedUser(t, st, name)
	}
	driver := "sqlite"
	if cfg.Database.Driver == "postgres" {
		driver = "pgx"
	}
	db, err := sql.Open(driver, cfg.Database.DSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return w, db
}

func reservationUsage(t *testing.T, db *sql.DB) (used, reserved int64, n int) {
	t.Helper()
	if err := db.QueryRow(`SELECT COALESCE(SUM(bytes),0),COUNT(*) FROM cancellation_reservations`).Scan(&reserved, &n); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT (SELECT bytes FROM calendar_usage WHERE id=1)+(SELECT COALESCE(SUM(LENGTH(body)),0) FROM scheduling_outbox WHERE status='pending')+(SELECT COALESCE(SUM(bytes),0) FROM cancellation_reservations)`).Scan(&used); err != nil {
		t.Fatal(err)
	}
	return
}

func cancellationMeeting(uid string) string {
	return meeting(uid, "alice", "DESCRIPTION:"+strings.Repeat("x", 600), "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:bob@example.com", "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:carol@example.com", "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:dan@example.com")
}

func TestCancellationReservationRejectsInsufficientAdmission(t *testing.T) {
	w, db := cancellationWorld(t, 4004, 4004)
	if code, _ := w.put("alice", "reserve.ics", cancellationMeeting("reserve"), ""); code != http.StatusInsufficientStorage {
		t.Fatalf("admission without cancellation capacity: %d", code)
	}
	used, reserved, n := reservationUsage(t, db)
	if used != 0 || reserved != 0 || n != 0 {
		t.Fatalf("refused admission left data or capacity: %d %d %d", used, reserved, n)
	}
}

func TestAdmittedMeetingCancelsWithOrdinaryQuotaFull(t *testing.T) {
	const cap int64 = 12000
	w, db := cancellationWorld(t, cap, cap)
	ctx := context.Background()
	body := cancellationMeeting("reserve")
	if code, _ := w.put("alice", "reserve.ics", body, ""); code != 201 {
		t.Fatalf("admit: %d", code)
	}
	w.deliver()
	_, reserved, n := reservationUsage(t, db)
	if reserved == 0 || n != 1 {
		t.Fatalf("no future cancellation reservation: %d %d", reserved, n)
	}
	_, _, etag := w.get("alice", "reserve.ics")
	if code, _ := w.put("alice", "reserve.ics", body, etag); code != 201 && code != 204 {
		t.Fatalf("repeat: %d", code)
	}
	_, again, n := reservationUsage(t, db)
	if again != reserved || n != 1 {
		t.Fatal("repeat stacked a reservation")
	}
	_, _, etag = w.get("alice", "reserve.ics")
	body = strings.Replace(body, strings.Repeat("x", 600), strings.Repeat("x", 700), 1)
	if code, _ := w.put("alice", "reserve.ics", body, etag); code != 201 && code != 204 {
		t.Fatalf("update: %d", code)
	}
	w.deliver()
	used, grown, n := reservationUsage(t, db)
	if grown <= reserved || n != 1 {
		t.Fatal("update did not replace cancellation liability")
	}
	// Ordinary data can fill only the unreserved remainder, including at the instance cap.
	base := meeting("ordinary", "alice")
	padding := int(cap-used) - len(base) - len("DESCRIPTION:\r\n")
	if padding < 0 {
		t.Fatalf("no ordinary headroom: used=%d", used)
	}
	filler := strings.Replace(base, "END:VEVENT", "DESCRIPTION:"+strings.Repeat("f", padding)+"\r\nEND:VEVENT", 1)
	if code, _ := w.put("alice", "ordinary.ics", filler, ""); code != 201 {
		t.Fatalf("fill ordinary usable budget: %d", code)
	}
	used, reserved, n = reservationUsage(t, db)
	if used != cap || n != 1 {
		t.Fatalf("expected exact ordinary usable cap, got %d / %d", used, cap)
	}
	if code, _ := w.put("bob", "steal.ics", meeting("steal", "bob"), ""); code != 507 {
		t.Fatalf("another owner consumed reserved instance capacity: %d", code)
	}
	_, _, etag = w.get("alice", "reserve.ics")
	if code, _ := w.put("alice", "reserve.ics", strings.Replace(body, "SUMMARY:Planning", "SUMMARY:Much larger planning", 1), etag); code != 507 {
		t.Fatalf("over-budget update: %d", code)
	}
	after, afterReserve, afterN := reservationUsage(t, db)
	if after != used || afterReserve != reserved || afterN != n {
		t.Fatal("refused update changed capacity")
	}
	// Deleting converts reserved bytes to the unchanged asynchronous CANCEL work.
	res := rawDAV(t, w.ts, "DELETE", "/dav/usr_alice/calendars/default/reserve.ics", "alice", w.tokens["alice"], "", map[string]string{"If-Match": etag})
	if res.StatusCode != 204 {
		t.Fatalf("admitted meeting could not cancel with ordinary budget full: %d %s", res.StatusCode, readAll(res))
	}
	used, reserved, n = reservationUsage(t, db)
	if used > cap || reserved != 0 || n != 0 {
		t.Fatalf("cancel did not convert/reclaim capacity: %d %d %d", used, reserved, n)
	}
	if _, o := w.copyOf("bob", "reserve"); o == nil {
		t.Fatal("cancellation was delivered synchronously")
	}
	due, err := w.st.Calendars().DueOutbox(ctx, time.Now().Add(time.Minute), 100)
	if err != nil || len(due) != 3 {
		t.Fatalf("expected three queued CANCELs: %d %v", len(due), err)
	}
	w.deliver()
	for _, name := range []string{"bob", "carol", "dan"} {
		if _, o := w.copyOf(name, "reserve"); o != nil {
			t.Fatalf("unanswered copy survived CANCEL for %s", name)
		}
	}
	used, reserved, n = reservationUsage(t, db)
	if used != int64(len(filler)) || reserved != 0 || n != 0 {
		t.Fatalf("liability not reclaimed after delivery: %d %d %d", used, reserved, n)
	}
}

func TestCancellationReservationConcurrentAdmissionAndReclamation(t *testing.T) {
	const cap int64 = 12000
	w, db := cancellationWorld(t, cap, cap)
	ctx := context.Background()
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("concurrent-%d", i)
			codes[i], _ = w.put("alice", name+".ics", cancellationMeeting(name), "")
		}(i)
	}
	wg.Wait()
	winner := -1
	for i, code := range codes {
		if code == 201 {
			if winner != -1 {
				t.Fatal("concurrent admissions double-spent cancellation capacity")
			}
			winner = i
		} else if code != 507 {
			t.Fatalf("unexpected admission status: %d", code)
		}
	}
	if winner < 0 {
		t.Fatal("neither admitted meeting fit")
	}
	used, _, n := reservationUsage(t, db)
	if used > cap || n != 1 {
		t.Fatal("concurrent reservation bound")
	}
	name := fmt.Sprintf("concurrent-%d", winner)
	_, _, etag := w.get("alice", name+".ics")
	res := rawDAV(t, w.ts, "DELETE", "/dav/usr_alice/calendars/default/"+name+".ics", "alice", w.tokens["alice"], "", map[string]string{"If-Match": etag})
	if res.StatusCode != 204 {
		t.Fatalf("cancel with REQUESTs still queued: %d %s", res.StatusCode, readAll(res))
	}
	w.deliver()
	if used, _, n := reservationUsage(t, db); used != 0 || n != 0 {
		t.Fatalf("cancelled admission retained capacity: %d %d", used, n)
	}
	// Removing every guest ends the source's future cancellation liability without deleting it.
	body := cancellationMeeting("remove-guests")
	if code, _ := w.put("alice", "remove.ics", body, ""); code != 201 {
		t.Fatalf("reclaimed capacity not reusable: %d", code)
	}
	w.deliver()
	_, _, etag = w.get("alice", "remove.ics")
	var lines []string
	for _, line := range strings.Split(body, "\r\n") {
		if !strings.HasPrefix(line, "ATTENDEE") {
			lines = append(lines, line)
		}
	}
	if code, _ := w.put("alice", "remove.ics", strings.Join(lines, "\r\n"), etag); code != 201 && code != 204 {
		t.Fatalf("remove guests: %d", code)
	}
	w.deliver()
	if _, reserved, n := reservationUsage(t, db); reserved != 0 || n != 0 {
		t.Fatal("removed guests retained cancellation liability")
	}
	// Calendar deletion uses the same reservation conversion and leaves no orphan rows.
	if code, _ := w.put("alice", "calendar-delete.ics", cancellationMeeting("calendar-delete"), ""); code != 201 {
		t.Fatalf("calendar deletion fixture: %d", code)
	}
	cal, err := w.st.Calendars().GetCalendarBySlug(ctx, "user", "usr_alice", "default")
	if err != nil {
		t.Fatal(err)
	}
	alice, err := w.st.Users().GetUserByID(ctx, "usr_alice")
	if err != nil {
		t.Fatal(err)
	}
	if res := call(t, w.srv, "DELETE", "/api/calendars/"+cal.ID, "", sessionFor(t, w.st, alice)); res.Code != 204 {
		t.Fatalf("calendar deletion: %d %s", res.Code, res.Body.String())
	}
	w.deliver()
	if used, reserved, n := reservationUsage(t, db); used != 0 || reserved != 0 || n != 0 {
		t.Fatal("calendar deletion left data or reservations")
	}
}

func TestGroupSourceRetainsSenderCancellationCapacity(t *testing.T) {
	const cap int64 = 12000
	w, db := cancellationWorld(t, cap, 50000)
	ctx := context.Background()
	team := groupCalendar(t, w.st, "Source")
	grantRole(t, w.st, team, "manager", "usr_alice")
	path := "/dav/usr_alice/calendars/_" + team.ID + "/source.ics"
	body := cancellationMeeting("group-source")
	res := rawDAV(t, w.ts, "PUT", path, "alice", w.tokens["alice"], body, map[string]string{"Content-Type": "text/calendar"})
	if res.StatusCode != 201 {
		t.Fatalf("group admit: %d %s", res.StatusCode, readAll(res))
	}
	w.deliver()
	source, err := w.st.Calendars().GetObject(ctx, team.ID, "source.ics")
	if err != nil {
		t.Fatal(err)
	}
	used, reserved, _ := reservationUsage(t, db)
	// Physical group source bytes belong to the group; future CANCELs belong to their sender.
	senderUsed := used - int64(len(source.Data))
	base := meeting("group-ordinary", "alice")
	padding := int(cap-senderUsed) - len(base) - len("DESCRIPTION:\r\n")
	if padding < 0 {
		t.Fatal("group source exhausted unexpected sender capacity")
	}
	filler := strings.Replace(base, "END:VEVENT", "DESCRIPTION:"+strings.Repeat("f", padding)+"\r\nEND:VEVENT", 1)
	if code, _ := w.put("alice", "group-ordinary.ics", filler, ""); code != 201 {
		t.Fatalf("fill sender ordinary budget: %d", code)
	}
	// X-properties do not queue a REQUEST, but do enlarge the existing full-CANCEL liability.
	grown := strings.Replace(body, "END:VEVENT", "X-EXTRA:"+strings.Repeat("x", 1000)+"\r\nEND:VEVENT", 1)
	res = rawDAV(t, w.ts, "PUT", path, "alice", w.tokens["alice"], grown, map[string]string{"Content-Type": "text/calendar", "If-Match": `"` + source.ETag + `"`})
	if res.StatusCode != 507 {
		t.Fatalf("group write bypassed full sender reservation budget: %d %s", res.StatusCode, readAll(res))
	}
	_, after, _ := reservationUsage(t, db)
	if after != reserved {
		t.Fatal("refused group write changed capacity")
	}
	res = rawDAV(t, w.ts, "DELETE", path, "alice", w.tokens["alice"], "", map[string]string{"If-Match": `"` + source.ETag + `"`})
	if res.StatusCode != 204 {
		t.Fatalf("group source cannot cancel at exact sender bound: %d %s", res.StatusCode, readAll(res))
	}
	used, reserved, n := reservationUsage(t, db)
	if used != cap || reserved != 0 || n != 0 {
		t.Fatalf("group capacity conversion: %d %d %d", used, reserved, n)
	}
	w.deliver()
}
