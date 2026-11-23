package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/apppass"
	"github.com/Busnes-app/kycalendar/internal/store"
)

func TestQuotaAdmissionUsesEachSendersOrg(t *testing.T) {
	w, db, cfg := cancellationWorldCfg(t, 100000, 1000000)
	ctx := context.Background()
	multiTenant(t, w.st)
	setOrgQuotas(t, cfg, store.DefaultOrg, `{"max_bytes_per_user":20000}`)
	otherOrgUser(t, w.st, "erin")
	setOrgQuotas(t, cfg, "org-other", `{"max_bytes_per_user":2000}`)
	id, token, hash, err := apppass.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := w.st.AppPasswords().Create(ctx, store.Seed, &store.AppPassword{Scope: store.ScopeDAV, ID: id, UserID: "usr_erin", Label: "t", Hash: hash}, 0); err != nil {
		t.Fatal(err)
	}
	w.tokens["erin"] = token
	series := meeting("quota-series", "alice", "RRULE:FREQ=DAILY;COUNT=3", "DESCRIPTION:"+strings.Repeat("x", 3000), "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:bob@example.com")
	if code, _ := w.put("alice", "source.ics", series, ""); code != 201 {
		t.Fatalf("series: %d", code)
	}
	w.deliver()
	_, _, etag := w.get("alice", "source.ics")
	occurrence := meeting("quota-series", "alice", "RECURRENCE-ID:20261111T090000Z", "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:bob@example.com")
	occurrence = strings.ReplaceAll(occurrence, "20261110T", "20261111T")
	if code, _ := w.put("alice", "source.ics", occurrence, etag); code != 201 && code != 204 {
		t.Fatalf("override: %d", code)
	}
	w.deliver()
	_, _, etag = w.get("alice", "source.ics")
	copy, obj := w.copyOf("bob", "quota-series")
	if obj == nil || len(copy) <= 2000 || !strings.Contains(copy, "RRULE:") || !strings.Contains(copy, "RECURRENCE-ID:") {
		t.Fatal("missing sponsored series and override")
	}
	var reserved int64
	if err := db.QueryRow(`SELECT COALESCE(SUM(bytes),0) FROM cancellation_reservations WHERE sender_id='usr_alice'`).Scan(&reserved); err != nil || reserved <= 0 {
		t.Fatalf("source reservation: %d %v", reserved, err)
	}
	// A lower quota on the sender's own org must still apply when that sender is affected.
	for _, lowered := range []bool{false, true} {
		if lowered {
			setOrgQuotas(t, cfg, store.DefaultOrg, `{"max_bytes_per_user":1000}`)
		}
		t.Run(map[bool]string{false: "within_sender_limit", true: "above_sender_limit"}[lowered], func(t *testing.T) {
			event := meeting("small", "erin")
			if code, _ := w.put("erin", "small.ics", event, ""); code != 201 {
				t.Fatalf("event PUT: %d", code)
			}
			list := &store.Calendar{ID: "cal_erin_tasks", OwnerKind: "user", OwnerID: "usr_erin", Slug: "tasks", Name: "Tasks", Components: []string{"VTODO"}}
			if !lowered {
				if err := w.st.Calendars().CreateCalendar(ctx, list, 0); err != nil {
					t.Fatal(err)
				}
			}
			path := "/dav/usr_erin/calendars/tasks/task.ics"
			task := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VTODO\r\nUID:small-task\r\nDTSTAMP:20261001T000000Z\r\nSUMMARY:Small task\r\nEND:VTODO\r\nEND:VCALENDAR\r\n"
			r := rawDAV(t, w.ts, "PUT", path, "erin", token, task, map[string]string{"Content-Type": "text/calendar"})
			if r.StatusCode != http.StatusCreated {
				t.Fatalf("task PUT: %d", r.StatusCode)
			}
			for _, p := range []string{"/dav/usr_erin/calendars/default/small.ics", path} {
				r := rawDAV(t, w.ts, "DELETE", p, "erin", token, "", nil)
				if r.StatusCode != http.StatusNoContent {
					t.Fatalf("DELETE: %d", r.StatusCode)
				}
			}
		})
	}
	if code, _ := w.put("alice", "source.ics", occurrence, etag); code != http.StatusInsufficientStorage {
		t.Fatalf("affected sender above own limit: %d", code)
	}
}
