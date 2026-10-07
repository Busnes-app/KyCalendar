package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/auth"
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

func TestEventsDuplicateCalendarIDs(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	cookie := loginAs(t, srv, st, "dee", "user")
	group := groupCalendar(t, st, "Dup")
	grantRole(t, st, group, "reader", "usr_dee")
	putObject(t, st, group, "weekly.ics", "weekly", recurringICS, 1791183600)
	var evs []eventJSON
	w := call(t, srv, "GET", "/api/events?start=2026-10-01T00:00:00Z&end=2026-10-20T00:00:00Z&calendar="+group.ID+","+group.ID, "", cookie)
	if err := json.Unmarshal(w.Body.Bytes(), &evs); err != nil || w.Code != http.StatusOK || len(evs) != 3 {
		t.Fatalf("duplicate ids: %d %d events %s", w.Code, len(evs), w.Body.String())
	}
}

func TestEventsTimeBudgetCoversEmptyCalendars(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	cookie := loginAs(t, srv, st, "eve", "user")
	defer api.SetExpandTimeForTest(0)()
	if w := call(t, srv, "GET", "/api/events?start=2026-10-01T00:00:00Z&end=2026-10-20T00:00:00Z", "", cookie); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty calendars with zero budget: %d %s", w.Code, w.Body.String())
	}
}

func TestEventsHideUnreadableCalendars(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	owner := loginAs(t, srv, st, "olga", "user")
	group := groupCalendar(t, st, "Private")
	putObject(t, st, group, "weekly.ics", "weekly", recurringICS, 1791183600)
	const q = "/api/events?start=2026-10-01T00:00:00Z&end=2026-10-20T00:00:00Z"
	// olga's own default calendar holds an event; a stranger must not see it either.
	call(t, srv, "GET", "/api/calendars", "", owner)
	cals, err := st.Calendars().ListCalendarsByOwner(context.Background(), "user", "usr_olga")
	if err != nil || len(cals) == 0 {
		t.Fatalf("olga calendars %v %v", cals, err)
	}
	putObject(t, st, cals[0], "mine.ics", "mine", recurringICS, 1791183600)
	var mine []eventJSON
	w := call(t, srv, "GET", q, "", owner)
	if err := json.Unmarshal(w.Body.Bytes(), &mine); err != nil || w.Code != http.StatusOK || len(mine) != 3 {
		t.Fatalf("owner should see her own 3 instances: %d %s", w.Code, w.Body.String())
	}
	stranger := loginAs(t, srv, st, "nate", "user")
	w = call(t, srv, "GET", q, "", stranger)
	var evs []eventJSON
	if err := json.Unmarshal(w.Body.Bytes(), &evs); err != nil || w.Code != http.StatusOK || len(evs) != 0 {
		t.Fatalf("non-member sees %d events: %s", len(evs), w.Body.String())
	}
}

func withIfMatch(t *testing.T, srv *api.Server, method, path, body, etag string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if etag != "" {
		req.Header.Set("If-Match", `"`+etag+`"`)
	}
	req.AddCookie(cookie)
	req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: "test-csrf"})
	req.Header.Set(auth.HeaderCSRF, "test-csrf")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

func eventsIn(t *testing.T, srv *api.Server, cookie *http.Cookie, calID string) []eventJSON {
	t.Helper()
	w := call(t, srv, "GET", "/api/events?start=2026-10-01T00:00:00Z&end=2026-11-01T00:00:00Z&tz=Europe/Berlin&calendar="+calID, "", cookie)
	var evs []eventJSON
	if err := json.Unmarshal(w.Body.Bytes(), &evs); err != nil {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	return evs
}

func TestEventLifecycleOnAGroupCalendar(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	cookie := loginAs(t, srv, st, "eda", "user")
	group := groupCalendar(t, st, "Team")
	grantRole(t, st, group, "editor", "usr_eda")

	create := `{"title":"Sync","start":"2026-10-05T09:00:00+02:00","end":"2026-10-05T10:00:00+02:00","zone":"Europe/Berlin","repeat":{"freq":"weekly","weekdays":["MO"]}}`
	w := call(t, srv, "POST", "/api/calendars/"+group.ID+"/events", create, cookie)
	if w.Code != http.StatusCreated {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	var created struct{ UID, ETag string }
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	evs := eventsIn(t, srv, cookie, group.ID)
	if len(evs) != 4 || evs[0].Start != "2026-10-05T09:00:00+02:00" || !evs[0].Editable {
		t.Fatalf("created series %+v", evs)
	}

	// Move one occurrence.
	one := `{"title":"Sync (Tue)","start":"2026-10-13T09:00:00+02:00","end":"2026-10-13T10:00:00+02:00","zone":"Europe/Berlin","repeat":{"freq":"custom"},"scope":"this","recurrence_id":"` + evs[1].RecurrenceID + `"}`
	w = withIfMatch(t, srv, "PUT", "/api/events/"+group.ID+"/"+created.UID, one, created.ETag, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("edit one %d %s", w.Code, w.Body.String())
	}
	var updated struct{ ETag string }
	_ = json.Unmarshal(w.Body.Bytes(), &updated)
	evs = eventsIn(t, srv, cookie, group.ID)
	if evs[1].Title != "Sync (Tue)" || evs[1].Start != "2026-10-13T09:00:00+02:00" {
		t.Fatalf("moved occurrence %+v", evs[1])
	}

	// Delete another occurrence, then the series.
	w = withIfMatch(t, srv, "DELETE", "/api/events/"+group.ID+"/"+created.UID+"?scope=this&recurrence_id="+evs[2].RecurrenceID, "", updated.ETag, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("delete one %d %s", w.Code, w.Body.String())
	}
	_ = json.Unmarshal(w.Body.Bytes(), &updated)
	if got := eventsIn(t, srv, cookie, group.ID); len(got) != 3 {
		t.Fatalf("after deleting one: %+v", got)
	}
	w = withIfMatch(t, srv, "DELETE", "/api/events/"+group.ID+"/"+created.UID+"?scope=all", "", updated.ETag, cookie)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete all %d %s", w.Code, w.Body.String())
	}
	if got := eventsIn(t, srv, cookie, group.ID); len(got) != 0 {
		t.Fatalf("after deleting the series: %+v", got)
	}
}

func TestEventPutStaleETagIs412(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	cookie := loginAs(t, srv, st, "sam", "user")
	group := groupCalendar(t, st, "Team")
	grantRole(t, st, group, "editor", "usr_sam")
	w := call(t, srv, "POST", "/api/calendars/"+group.ID+"/events", `{"title":"A","start":"2026-10-07T09:00:00Z","end":"2026-10-07T10:00:00Z","zone":"UTC"}`, cookie)
	var created struct{ UID, ETag string }
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	before, _ := st.Calendars().GetObjectByUID(context.Background(), group.ID, created.UID)

	edit := `{"title":"B","start":"2026-10-07T09:00:00Z","end":"2026-10-07T10:00:00Z","zone":"UTC","scope":"all"}`
	if w := withIfMatch(t, srv, "PUT", "/api/events/"+group.ID+"/"+created.UID, edit, "0000", cookie); w.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale If-Match: %d %s", w.Code, w.Body.String())
	}
	if w := withIfMatch(t, srv, "PUT", "/api/events/"+group.ID+"/"+created.UID, edit, "", cookie); w.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing If-Match: %d", w.Code)
	}
	after, _ := st.Calendars().GetObjectByUID(context.Background(), group.ID, created.UID)
	if string(after.Data) != string(before.Data) || after.ETag != before.ETag {
		t.Fatal("a refused edit changed the stored object")
	}
}

func TestEventWriteRolesAndValidation(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	group := groupCalendar(t, st, "Team")
	reader := loginAs(t, srv, st, "ray", "user")
	editor := loginAs(t, srv, st, "eve", "user")
	grantRole(t, st, group, "reader", "usr_ray")
	grantRole(t, st, group, "editor", "usr_eve")
	ok := `{"title":"A","start":"2026-10-07T09:00:00Z","end":"2026-10-07T10:00:00Z","zone":"UTC"}`
	if w := call(t, srv, "POST", "/api/calendars/"+group.ID+"/events", ok, reader); w.Code != http.StatusForbidden {
		t.Fatalf("reader create %d, want 403", w.Code)
	}
	for _, bad := range []string{
		`{"title":"A","start":"2026-10-07T10:00:00Z","end":"2026-10-07T09:00:00Z","zone":"UTC"}`,
		`{"title":"A","start":"2026-10-07T09:00:00Z","end":"2026-10-07T10:00:00Z"}`,
		`{"title":"A","start":"2026-10-07T09:00:00Z","end":"2026-10-07T10:00:00Z","zone":"Local"}`,
		`{"title":"A","start":"2026-10-07","end":"2026-10-07","all_day":true}`,
		`{"title":"A","start":"2026-10-07T09:00:00Z","end":"2026-10-07T10:00:00Z","zone":"UTC","repeat":{"freq":"hourly"}}`,
		`{"title":"A","start":"2026-10-07T09:00:00Z","end":"2026-10-07T10:00:00Z","zone":"UTC","repeat":{"freq":"daily","weekdays":["MO"]}}`,
		`{"title":"A","start":"2026-10-07T09:00:00Z","end":"2026-10-07T10:00:00Z","zone":"UTC","repeat":{"freq":"custom"}}`,
		`{"title":"` + strings.Repeat("x", 1001) + `","start":"2026-10-07T09:00:00Z","end":"2026-10-07T10:00:00Z","zone":"UTC"}`,
	} {
		if w := call(t, srv, "POST", "/api/calendars/"+group.ID+"/events", bad, editor); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", bad, w.Code)
		}
	}
	if w := withIfMatch(t, srv, "PUT", "/api/events/"+group.ID+"/missing", ok, "x", editor); w.Code != http.StatusNotFound {
		t.Fatalf("missing event %d, want 404", w.Code)
	}
}

func TestEventIfMatchMustBeOneStrongETag(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	cookie := loginAs(t, srv, st, "ifm", "user")
	group := groupCalendar(t, st, "Team")
	grantRole(t, st, group, "editor", "usr_ifm")
	w := call(t, srv, "POST", "/api/calendars/"+group.ID+"/events", `{"title":"A","start":"2026-10-07T09:00:00Z","end":"2026-10-07T10:00:00Z","zone":"UTC"}`, cookie)
	var created struct{ UID, ETag string }
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	before, _ := st.Calendars().GetObjectByUID(context.Background(), group.ID, created.UID)
	edit := `{"title":"B","start":"2026-10-07T09:00:00Z","end":"2026-10-07T10:00:00Z","zone":"UTC"}`
	for _, bad := range []string{`""`, `"*"`, `*`, `W/"x"`, `"a", "b"`, `abc`} {
		for _, m := range []string{"PUT", "DELETE"} {
			req := httptest.NewRequest(m, "/api/events/"+group.ID+"/"+created.UID+"?scope=all", strings.NewReader(edit))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("If-Match", bad)
			req.AddCookie(cookie)
			req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: "test-csrf"})
			req.Header.Set(auth.HeaderCSRF, "test-csrf")
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("%s If-Match %s: %d, want 400", m, bad, rec.Code)
			}
		}
	}
	after, err := st.Calendars().GetObjectByUID(context.Background(), group.ID, created.UID)
	if err != nil || after.ETag != before.ETag || string(after.Data) != string(before.Data) {
		t.Fatal("a malformed If-Match changed the stored object")
	}
}

func TestEventWriteRefusesAnOverlargeObject(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	cookie := loginAs(t, srv, st, "big", "user")
	group := groupCalendar(t, st, "Team")
	grantRole(t, st, group, "editor", "usr_big")
	pad := strings.Repeat("x", 1<<20-len(recurringICS)-100)
	data := strings.Replace(recurringICS, "SUMMARY:", "X-PAD:"+pad+"\r\nSUMMARY:", 1)
	putObject(t, st, group, "weekly.ics", "weekly", data, 1790000000)
	o, _ := st.Calendars().GetObjectByUID(context.Background(), group.ID, "weekly")
	evs := eventsIn(t, srv, cookie, group.ID)
	if len(evs) == 0 {
		t.Fatal("seed not listed")
	}
	one := `{"title":"T","description":"` + strings.Repeat("d", 5000) + `","start":"2026-10-12T09:00:00+02:00","end":"2026-10-12T10:00:00+02:00","zone":"Europe/Berlin","repeat":{"freq":"custom"},"scope":"this","recurrence_id":"` + evs[1].RecurrenceID + `"}`
	w := withIfMatch(t, srv, "PUT", "/api/events/"+group.ID+"/weekly", one, o.ETag, cookie)
	if w.Code != http.StatusRequestEntityTooLarge || !strings.Contains(w.Body.String(), `"too_large"`) {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

func TestEventTextAndDateLimits(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	cookie := loginAs(t, srv, st, "lim", "user")
	group := groupCalendar(t, st, "Team")
	grantRole(t, st, group, "editor", "usr_lim")
	for _, bad := range []string{
		`{"title":"a\rb","start":"2026-10-07T09:00:00Z","end":"2026-10-07T10:00:00Z","zone":"UTC"}`,
		`{"title":"A","location":"a\u0000b","start":"2026-10-07T09:00:00Z","end":"2026-10-07T10:00:00Z","zone":"UTC"}`,
		`{"title":"A","description":"a\u001bb","start":"2026-10-07T09:00:00Z","end":"2026-10-07T10:00:00Z","zone":"UTC"}`,
		`{"title":"A","start":"1899-12-31T09:00:00Z","end":"1900-01-01T10:00:00Z","zone":"UTC"}`,
		`{"title":"A","start":"2026-10-07T09:00:00Z","end":"9001-01-01T10:00:00Z","zone":"UTC"}`,
		`{"title":"A","start":"1850-10-07","end":"1850-10-08","all_day":true}`,
	} {
		if w := call(t, srv, "POST", "/api/calendars/"+group.ID+"/events", bad, cookie); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", bad, w.Code)
		}
	}
	ok := `{"title":"multi\nline\ttab","start":"2026-10-07T09:00:00Z","end":"2026-10-07T10:00:00Z","zone":"UTC"}`
	if w := call(t, srv, "POST", "/api/calendars/"+group.ID+"/events", ok, cookie); w.Code != http.StatusCreated {
		t.Fatalf("newline and tab are allowed: %d %s", w.Code, w.Body.String())
	}
}

func TestEventEditErrorsAndOwnerFlow(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	cookie := loginAs(t, srv, st, "own", "user")
	mine := personalCalendar(t, st, "usr_own")
	other := groupCalendar(t, st, "Other")
	grantRole(t, st, other, "editor", "usr_own")
	mk := func(cal string) (string, string) {
		w := call(t, srv, "POST", "/api/calendars/"+cal+"/events", `{"title":"A","start":"2026-10-07T09:00:00Z","end":"2026-10-07T10:00:00Z","zone":"UTC"}`, cookie)
		if w.Code != http.StatusCreated {
			t.Fatalf("create %d %s", w.Code, w.Body.String())
		}
		var c struct{ UID, ETag string }
		_ = json.Unmarshal(w.Body.Bytes(), &c)
		return c.UID, c.ETag
	}
	uid, etag := mk(mine.ID)
	body := `{"title":"B","start":"2026-10-07T09:00:00Z","end":"2026-10-07T10:00:00Z","zone":"UTC"`
	for _, sc := range []string{"bogus"} {
		if w := withIfMatch(t, srv, "PUT", "/api/events/"+mine.ID+"/"+uid, body+`,"scope":"`+sc+`"}`, etag, cookie); w.Code != http.StatusBadRequest {
			t.Errorf("PUT scope=%s %d", sc, w.Code)
		}
		if w := withIfMatch(t, srv, "DELETE", "/api/events/"+mine.ID+"/"+uid+"?scope="+sc, "", etag, cookie); w.Code != http.StatusBadRequest {
			t.Errorf("DELETE scope=%s %d", sc, w.Code)
		}
	}
	w := withIfMatch(t, srv, "PUT", "/api/events/"+mine.ID+"/"+uid, body+`,"scope":"this","recurrence_id":"x"}`, etag, cookie)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "does not repeat") {
		t.Errorf("non-recurring this: %d %s", w.Code, w.Body.String())
	}
	rw := call(t, srv, "POST", "/api/calendars/"+mine.ID+"/events", `{"title":"R","start":"2026-10-07T09:00:00Z","end":"2026-10-07T10:00:00Z","zone":"UTC","repeat":{"freq":"daily"}}`, cookie)
	var rc struct{ UID, ETag string }
	_ = json.Unmarshal(rw.Body.Bytes(), &rc)
	w = withIfMatch(t, srv, "PUT", "/api/events/"+mine.ID+"/"+rc.UID, body+`,"repeat":{"freq":"custom"},"scope":"this","recurrence_id":"20300101T000000Z"}`, rc.ETag, cookie)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "No such occurrence") {
		t.Errorf("bad occurrence: %d %s", w.Code, w.Body.String())
	}
	w = withIfMatch(t, srv, "PUT", "/api/events/"+mine.ID+"/"+uid, body+`}`, etag, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("owner edit %d %s", w.Code, w.Body.String())
	}
	var up struct{ ETag string }
	_ = json.Unmarshal(w.Body.Bytes(), &up)
	if w = withIfMatch(t, srv, "DELETE", "/api/events/"+mine.ID+"/"+uid, "", up.ETag, cookie); w.Code != http.StatusNoContent {
		t.Fatalf("owner delete %d", w.Code)
	}
	// A UID that lives in another calendar is not found under this {cal}.
	uid2, etag2 := mk(mine.ID)
	if w = withIfMatch(t, srv, "PUT", "/api/events/"+other.ID+"/"+uid2, body+`}`, etag2, cookie); w.Code != http.StatusNotFound {
		t.Fatalf("cross-calendar PUT %d", w.Code)
	}
}
