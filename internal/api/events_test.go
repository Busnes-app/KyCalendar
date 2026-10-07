package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/store"
)

const recurringICS = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:weekly\r\nDTSTAMP:20261001T000000Z\r\nDTSTART;TZID=Europe/Berlin:20261005T090000\r\nDTEND;TZID=Europe/Berlin:20261005T100000\r\nRRULE:FREQ=WEEKLY\r\nSUMMARY:<b>Standup</b>\r\nLOCATION:Room 1\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

func putObject(t *testing.T, st store.Store, cal *store.Calendar, name, uid, data string, first int64) {
	t.Helper()
	o := &store.CalendarObject{CalendarID: cal.ID, Name: name, UID: uid, Data: []byte(data), FirstStart: first}
	if _, err := st.Calendars().PutObject(context.Background(), o, "", false, store.OwnerLimits{}); err != nil {
		t.Fatal(err)
	}
}

type eventJSON struct {
	CalendarID   string `json:"calendar_id"`
	UID          string `json:"uid"`
	RecurrenceID string `json:"recurrence_id"`
	ETag         string `json:"etag"`
	Title        string `json:"title"`
	Location     string `json:"location"`
	Start        string `json:"start"`
	End          string `json:"end"`
	AllDay       bool   `json:"all_day"`
	Recurring    bool   `json:"recurring"`
	Editable     bool   `json:"editable"`
	Repeat       struct {
		Freq string `json:"freq"`
	} `json:"repeat"`
}

func TestCalendarsAndEventsForAReader(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	cookie := loginAs(t, srv, st, "rita", "user")
	group := groupCalendar(t, st, "Team")
	grantRole(t, st, group, "reader", "usr_rita")
	putObject(t, st, group, "weekly.ics", "weekly", recurringICS, 1791183600)

	w := call(t, srv, "GET", "/api/calendars", "", cookie)
	var cals []struct{ ID, Kind, Role, DAVPath string }
	if err := json.Unmarshal(w.Body.Bytes(), &cals); err != nil || w.Code != http.StatusOK {
		t.Fatalf("calendars %d %s", w.Code, w.Body.String())
	}
	if len(cals) != 2 || cals[0].Kind != "personal" || cals[0].Role != "owner" || cals[1].ID != group.ID || cals[1].Role != "reader" {
		t.Fatalf("calendars %+v", cals)
	}

	w = call(t, srv, "GET", "/api/events?start=2026-10-01T00:00:00Z&end=2026-10-20T00:00:00Z&tz=America/New_York", "", cookie)
	var evs []eventJSON
	if err := json.Unmarshal(w.Body.Bytes(), &evs); err != nil || w.Code != http.StatusOK {
		t.Fatalf("events %d %s", w.Code, w.Body.String())
	}
	if len(evs) != 3 {
		t.Fatalf("want 3 weekly instances, got %+v", evs)
	}
	e := evs[0]
	if e.Start != "2026-10-05T03:00:00-04:00" || e.Title != "<b>Standup</b>" || e.Editable || !e.Recurring || e.Repeat.Freq != "weekly" || e.RecurrenceID != "20261005T070000Z" || e.ETag == "" {
		t.Fatalf("instance %+v", e)
	}
}

func TestEventsValidation(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	cookie := loginAs(t, srv, st, "vic", "user")
	for _, q := range []string{
		"",
		"?start=2026-10-01T00:00:00Z",
		"?start=2026-10-02T00:00:00Z&end=2026-10-01T00:00:00Z",
		"?start=2026-01-01T00:00:00Z&end=2027-02-15T00:00:00Z",
		"?start=2026-10-01T00:00:00Z&end=2026-10-02T00:00:00Z&tz=Not/AZone",
		"?start=2026-10-01T00:00:00Z&end=2026-10-02T00:00:00Z&tz=Local",
	} {
		if w := call(t, srv, "GET", "/api/events"+q, "", cookie); w.Code != http.StatusBadRequest {
			t.Errorf("%q: %d, want 400", q, w.Code)
		}
	}
	other := groupCalendar(t, st, "Secret")
	if w := call(t, srv, "GET", "/api/events?start=2026-10-01T00:00:00Z&end=2026-10-02T00:00:00Z&calendar="+other.ID, "", cookie); w.Code != http.StatusNotFound {
		t.Errorf("unreadable calendar: %d, want 404", w.Code)
	}
}

func TestEventsInstanceCap(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	cookie := loginAs(t, srv, st, "hal", "user")
	group := groupCalendar(t, st, "Busy")
	grantRole(t, st, group, "editor", "usr_hal")
	minutely := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:hourly\r\nDTSTAMP:20261001T000000Z\r\nDTSTART:20260101T000000Z\r\nDTEND:20260101T000500Z\r\nRRULE:FREQ=HOURLY\r\nSUMMARY:x\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	putObject(t, st, group, "hourly.ics", "hourly", minutely, 1767225600)
	w := call(t, srv, "GET", "/api/events?start=2026-01-01T00:00:00Z&end=2026-12-31T00:00:00Z", "", cookie)
	if w.Code != http.StatusUnprocessableEntity || !json.Valid(w.Body.Bytes()) {
		t.Fatalf("hourly for a year: %d %s", w.Code, w.Body.String())
	}
}

func TestEventsTimeBudget(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	cookie := loginAs(t, srv, st, "tim", "user")
	group := groupCalendar(t, st, "Slow")
	grantRole(t, st, group, "reader", "usr_tim")
	putObject(t, st, group, "weekly.ics", "weekly", recurringICS, 1791183600)
	const q = "/api/events?start=2026-10-01T00:00:00Z&end=2026-10-20T00:00:00Z"

	restore := api.SetExpandTimeForTest(0)
	w := call(t, srv, "GET", q, "", cookie)
	var body struct{ Code string }
	if w.Code != http.StatusUnprocessableEntity || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Code != "too_many_instances" {
		t.Fatalf("zero budget: %d %s", w.Code, w.Body.String())
	}
	restore()
	if w := call(t, srv, "GET", q, "", cookie); w.Code != http.StatusOK {
		t.Fatalf("restored budget: %d %s", w.Code, w.Body.String())
	}
}
