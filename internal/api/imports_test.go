package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/store"
)

type importJSON struct {
	Imported     int `json:"imported"`
	Duplicates   int `json:"duplicates"`
	SkippedCount int `json:"skipped_count"`
	Skipped      []struct {
		Row    int    `json:"row"`
		Reason string `json:"reason"`
	} `json:"skipped"`
	Stopped string `json:"stopped"`
}

const importCSV = "Subject,Start Date,Start Time,End Date,End Time,All Day Event,Description,Location\n" +
	"Standup,10/29/2026,9:00 AM,10/29/2026,9:15 AM,False,Daily,Room 1\n" +
	"Holiday,10/30/2026,,10/30/2026,,True,,\n" +
	"Broken,,,,,False,,\n"

func importInto(t *testing.T, srv *api.Server, cookie *http.Cookie, calID, query, body string) (int, importJSON) {
	t.Helper()
	w := call(t, srv, "POST", "/api/calendars/"+calID+"/import"+query, body, cookie)
	var v importJSON
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
			t.Fatalf("import body %s", w.Body.String())
		}
	}
	return w.Code, v
}

func TestImportCSV(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	cookie := loginAs(t, srv, st, "ivy", "user")
	call(t, srv, "GET", "/api/calendars", "", cookie) // creates the default calendar
	cal := personalCalendar(t, st, "usr_ivy")

	code, v := importInto(t, srv, cookie, cal.ID, "?tz=Europe/Berlin", importCSV)
	if code != http.StatusOK || v.Imported != 2 || v.Duplicates != 0 || v.SkippedCount != 1 || len(v.Skipped) != 1 || v.Skipped[0].Row != 4 || v.Stopped != "" {
		t.Fatalf("first import %d %+v", code, v)
	}
	if !audited(t, st, "calendar.import") {
		t.Fatal("import not audited")
	}

	w := call(t, srv, "GET", "/api/events?start=2026-10-29T00:00:00Z&end=2026-11-01T00:00:00Z&tz=Europe/Berlin", "", cookie)
	var evs []eventJSON
	if err := json.Unmarshal(w.Body.Bytes(), &evs); err != nil || len(evs) != 2 {
		t.Fatalf("events %d %s", w.Code, w.Body.String())
	}
	if e := evs[0]; e.Title != "Standup" || e.Start != "2026-10-29T09:00:00+01:00" || e.Location != "Room 1" || !e.Editable {
		t.Fatalf("standup %+v", e)
	}
	if e := evs[1]; !e.AllDay || e.Start != "2026-10-30" || e.End != "2026-10-31" {
		t.Fatalf("holiday %+v", e)
	}

	// The same file again adds nothing.
	code, v = importInto(t, srv, cookie, cal.ID, "?tz=Europe/Berlin", importCSV)
	if code != http.StatusOK || v.Imported != 0 || v.Duplicates != 2 {
		t.Fatalf("second import %d %+v", code, v)
	}
	w = call(t, srv, "GET", "/api/events?start=2026-10-29T00:00:00Z&end=2026-11-01T00:00:00Z&tz=Europe/Berlin", "", cookie)
	if err := json.Unmarshal(w.Body.Bytes(), &evs); err != nil || len(evs) != 2 {
		t.Fatalf("events after the second import %s", w.Body.String())
	}

	// Another calendar is not "already there": its UIDs are its own.
	w = call(t, srv, "POST", "/api/calendars", `{"name":"Second","components":["VEVENT"]}`, cookie)
	var second struct{ ID string }
	if err := json.Unmarshal(w.Body.Bytes(), &second); err != nil || w.Code != http.StatusCreated {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	if code, v = importInto(t, srv, cookie, second.ID, "?tz=Europe/Berlin", importCSV); code != http.StatusOK || v.Imported != 2 || v.Duplicates != 0 {
		t.Fatalf("import into a second calendar %d %+v", code, v)
	}
}

// TestImportCSVZoneAndDateOrder reads the same wall time in two zones and an ambiguous date in
// both orders: the zone moves the instant, date_order only decides what the file does not.
func TestImportCSVZoneAndDateOrder(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	cookie := loginAs(t, srv, st, "ivy", "user")
	call(t, srv, "GET", "/api/calendars", "", cookie)
	cal := personalCalendar(t, st, "usr_ivy")
	const file = "Subject,Start Date,Start Time\nAmbiguous,03/02/2026,9:00\n"
	for _, c := range []struct{ query, want, zone string }{
		{"?tz=America/New_York&date_order=mdy", "2026-03-02T14:00:00Z", "America/New_York"},
		{"?tz=Asia/Tokyo&date_order=dmy", "2026-02-03T00:00:00Z", "Asia/Tokyo"},
	} {
		if code, v := importInto(t, srv, cookie, cal.ID, c.query, file); code != http.StatusOK || v.Imported != 1 {
			t.Fatalf("%s: %d %+v", c.query, code, v)
		}
		w := call(t, srv, "GET", "/api/events?start=2026-01-01T00:00:00Z&end=2026-04-01T00:00:00Z&tz=UTC", "", cookie)
		// The event keeps the import zone, so it stays 9:00 there when viewed from elsewhere.
		if !strings.Contains(w.Body.String(), `"start":"`+c.want+`"`) || !strings.Contains(w.Body.String(), `"zone":"`+c.zone+`"`) {
			t.Fatalf("%s: want start %s in zone %s in %s", c.query, c.want, c.zone, w.Body.String())
		}
	}
}

// TestImportCSVHostileRows: control characters and out-of-range years skip their row; markup is
// stored as the text it is, never interpreted.
func TestImportCSVHostileRows(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	cookie := loginAs(t, srv, st, "ivy", "user")
	call(t, srv, "GET", "/api/calendars", "", cookie)
	cal := personalCalendar(t, st, "usr_ivy")
	file := "Subject,Start Date,Start Time,Description\n" +
		"Bell\x07,03/02/2026,9:00,\n" +
		"Far,03/02/1800,9:00,\n" +
		"\"<img src=x onerror=alert(1)>\",03/02/2026,10:00,\"=HYPERLINK(\"\"http://x\"\")\"\n" +
		"Long,03/02/2026,11:00," + strings.Repeat("d", 64<<10+1) + "\n"
	code, v := importInto(t, srv, cookie, cal.ID, "?tz=UTC", file)
	if code != http.StatusOK || v.Imported != 1 || v.SkippedCount != 3 {
		t.Fatalf("import %d %+v", code, v)
	}
	for i, want := range []int{2, 3, 5} {
		if v.Skipped[i].Row != want {
			t.Fatalf("skipped %+v", v.Skipped)
		}
	}
	w := call(t, srv, "GET", "/api/events?start=2026-03-01T00:00:00Z&end=2026-03-04T00:00:00Z&tz=UTC", "", cookie)
	var evs []struct{ Title, Description string }
	if err := json.Unmarshal(w.Body.Bytes(), &evs); err != nil || len(evs) != 1 || evs[0].Title != "<img src=x onerror=alert(1)>" || evs[0].Description != `=HYPERLINK("http://x")` {
		t.Fatalf("events %s", w.Body.String())
	}
}

func TestImportCSVRefusals(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	cookie := loginAs(t, srv, st, "ivy", "user")
	call(t, srv, "GET", "/api/calendars", "", cookie)
	cal := personalCalendar(t, st, "usr_ivy")
	ctx := context.Background()

	sub := &store.Calendar{ID: "cal_sub_ivy", OwnerKind: "user", OwnerID: "usr_ivy", Slug: "sub", Name: "Work"}
	if err := st.Calendars().CreateSubscribedCalendar(ctx, sub, &store.Subscription{URLSealed: "x", URLHost: "x.invalid", NextCheckAt: time.Now().Add(time.Hour)}, 0, 0); err != nil {
		t.Fatal(err)
	}
	tasks := &store.Calendar{ID: "cal_tasks_ivy", OwnerKind: "user", OwnerID: "usr_ivy", Slug: "tasks", Name: "Todo", Components: []string{"VTODO"}}
	if err := st.Calendars().CreateCalendar(ctx, tasks, 0); err != nil {
		t.Fatal(err)
	}
	reader := groupCalendar(t, st, "Team")
	grantRole(t, st, reader, "reader", "usr_ivy")
	otherCookie := loginAs(t, srv, st, "max", "user")
	call(t, srv, "GET", "/api/calendars", "", otherCookie)
	other := personalCalendar(t, st, "usr_max")

	for name, c := range map[string]struct {
		cal, query, body string
		want             int
	}{
		"subscribed calendar": {sub.ID, "?tz=UTC", importCSV, http.StatusForbidden},
		"reader":              {reader.ID, "?tz=UTC", importCSV, http.StatusForbidden},
		"tasks only":          {tasks.ID, "?tz=UTC", importCSV, http.StatusUnprocessableEntity},
		"bad zone":            {cal.ID, "?tz=Local", importCSV, http.StatusBadRequest},
		"bad date order":      {cal.ID, "?tz=UTC&date_order=ymd", importCSV, http.StatusBadRequest},
		"not a calendar":      {cal.ID, "?tz=UTC", "Name,Email\nIvy,ivy@example.com\n", http.StatusUnprocessableEntity},
		"no such calendar":    {"cal_nope", "?tz=UTC", importCSV, http.StatusNotFound},
		"another user's":      {other.ID, "?tz=UTC", importCSV, http.StatusNotFound},
		"over the row cap":    {cal.ID, "?tz=UTC", "Subject,Start Date\n" + strings.Repeat("X,03/02/2026\n", 5001), http.StatusUnprocessableEntity},
		"over 1 MiB":          {cal.ID, "?tz=UTC", "Subject,Start Date\nX,03/02/2026," + strings.Repeat("x", 1<<20) + "\n", http.StatusRequestEntityTooLarge},
	} {
		if code, _ := importInto(t, srv, cookie, c.cal, c.query, c.body); code != c.want {
			t.Errorf("%s: got %d, want %d", name, code, c.want)
		}
	}

	// A session cookie without its CSRF token is refused before the handler runs.
	req := httptest.NewRequest("POST", "/api/calendars/"+cal.ID+"/import?tz=UTC", strings.NewReader(importCSV))
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("no CSRF token: got %d, want 403", w.Code)
	}
	// No session at all.
	if w := call(t, srv, "POST", "/api/calendars/"+cal.ID+"/import?tz=UTC", importCSV, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("no session: got %d, want 401", w.Code)
	}
	// None of the refused requests wrote anything.
	for _, id := range []string{cal.ID, other.ID, reader.ID} {
		objs, err := st.Calendars().ListObjects(ctx, id)
		if err != nil || len(objs) != 0 {
			t.Errorf("%s holds %d objects after refusals (err %v)", id, len(objs), err)
		}
	}
}

func TestImportCSVStopsAtTheQuota(t *testing.T) {
	srv, st, cfg := setupTestServer(t)
	cfg.Calendar.MaxObjectsPerUser = 1
	cookie := loginAs(t, srv, st, "ivy", "user")
	call(t, srv, "GET", "/api/calendars", "", cookie)
	cal := personalCalendar(t, st, "usr_ivy")

	code, v := importInto(t, srv, cookie, cal.ID, "?tz=UTC", importCSV)
	if code != http.StatusOK || v.Imported != 1 || v.Stopped != "Stopped at row 3: Calendar storage is full" {
		t.Fatalf("import %d %+v", code, v)
	}
}

// TestImportCSVOutlastsTheWriteTimeout: a large import takes longer than the listener's
// WriteTimeout, which is sized for JSON replies; the handler extends its own deadline.
func TestImportCSVOutlastsTheWriteTimeout(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	cookie := loginAs(t, srv, st, "ivy", "user")
	call(t, srv, "GET", "/api/calendars", "", cookie)
	cal := personalCalendar(t, st, "usr_ivy")
	var file strings.Builder
	file.WriteString("Subject,Start Date,Start Time\n")
	for i := range 300 {
		fmt.Fprintf(&file, "Event %d,%d/%d/2026,10:00\n", i, i%12+1, i%28+1)
	}

	ts := httptest.NewUnstartedServer(srv)
	ts.Config.WriteTimeout = 50 * time.Millisecond
	ts.Start()
	defer ts.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/calendars/"+cal.ID+"/import?tz=UTC", bytes.NewReader([]byte(file.String())))
	req.AddCookie(cookie)
	addCSRF(req, cookie)
	start := time.Now()
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("import cut off after %v: %v", time.Since(start), err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	var v importJSON
	if res.StatusCode != http.StatusOK || json.Unmarshal(body, &v) != nil || v.Imported != 300 {
		t.Fatalf("import %d %s", res.StatusCode, body)
	}
	if took := time.Since(start); took < ts.Config.WriteTimeout {
		t.Skipf("the import took %v, under the WriteTimeout; nothing was proven", took)
	}
}

// TestImportCSVReportsTheFirstSkips: the response lists 100 skipped rows and counts them all.
func TestImportCSVReportsTheFirstSkips(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	cookie := loginAs(t, srv, st, "ivy", "user")
	call(t, srv, "GET", "/api/calendars", "", cookie)
	cal := personalCalendar(t, st, "usr_ivy")
	file := "Subject,Start Date\n" + strings.Repeat("X,not a date\n", 150)
	code, v := importInto(t, srv, cookie, cal.ID, "?tz=UTC", file)
	if code != http.StatusOK || v.SkippedCount != 150 || len(v.Skipped) != 100 || v.Skipped[99].Row != 101 {
		t.Fatalf("import %d count %d listed %d", code, v.SkippedCount, len(v.Skipped))
	}
}

// pausedWrites holds every event write until release is closed, signalling entered first, so a
// test can keep imports in flight.
type pausedWrites struct {
	store.Store
	entered chan string // calendar ID of each write that reached the store
	release chan struct{}
	unpause func()
}

func (p pausedWrites) Calendars() store.CalendarStore {
	return pausedCalendars{CalendarStore: p.Store.Calendars(), p: p}
}

type pausedCalendars struct {
	store.CalendarStore
	p pausedWrites
}

func (c pausedCalendars) PutObjectPlanned(ctx context.Context, o *store.CalendarObject, ifMatch string, ifNoneMatch bool, lim store.OwnerLimits, plan store.Planner) (bool, error) {
	c.p.entered <- o.CalendarID
	<-c.p.release
	return c.CalendarStore.PutObjectPlanned(ctx, o, ifMatch, ifNoneMatch, lim, plan)
}

func pauseWrites(t *testing.T, srv *api.Server, st store.Store) pausedWrites {
	p := pausedWrites{Store: st, entered: make(chan string, 64), release: make(chan struct{})}
	var once sync.Once
	p.unpause = func() { once.Do(func() { close(p.release) }) }
	t.Cleanup(p.unpause) // a failed test must not leave imports blocked under the closing store
	api.SetStoreForTest(srv, p)
	return p
}

// startImport posts importCSV in the background and returns the response once it ends.
func startImport(srv *api.Server, cookie *http.Cookie, calID string) <-chan *httptest.ResponseRecorder {
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest("POST", "/api/calendars/"+calID+"/import?tz=UTC", strings.NewReader(importCSV))
		req.AddCookie(cookie)
		addCSRF(req, cookie)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		done <- w
	}()
	return done
}

// refused waits for an import that must be turned away before it writes anything.
func refused(t *testing.T, p pausedWrites, done <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case w := <-done:
		return w
	case <-p.entered:
		t.Fatal("the import was admitted and reached the store")
	case <-time.After(5 * time.Second):
		t.Fatal("the import neither answered nor wrote")
	}
	return nil
}

func waitEntered(t *testing.T, p pausedWrites) {
	t.Helper()
	select {
	case <-p.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the import never reached the store")
	}
}

// TestImportCSVIsTrackedForShutdown: an import keeps writing after its client is gone, so
// shutdown must wait for it before the store closes, or the rest of the rows and the audit row
// are lost.
func TestImportCSVIsTrackedForShutdown(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	cookie := loginAs(t, srv, st, "ivy", "user")
	call(t, srv, "GET", "/api/calendars", "", cookie)
	cal := personalCalendar(t, st, "usr_ivy")
	p := pauseWrites(t, srv, st)

	done := startImport(srv, cookie, cal.ID)
	waitEntered(t, p)
	waited := make(chan struct{})
	go func() { defer close(waited); srv.WaitDetached() }()
	select {
	case <-waited:
		t.Fatal("WaitDetached returned with an import still writing; the store would close under it")
	case <-time.After(100 * time.Millisecond):
	}

	p.unpause()
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("WaitDetached did not return after the import finished")
	}
	if w := <-done; w.Code != http.StatusOK {
		t.Fatalf("import %d %s", w.Code, w.Body.String())
	}
	if !audited(t, st, "calendar.import") {
		t.Error("the wait returned before the import was audited")
	}
}

// TestImportCSVOnePerUser: a second import from the same person while one is writing is refused
// with a message the web app shows; other people still import, up to the server-wide cap.
func TestImportCSVOnePerUser(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	cookie := loginAs(t, srv, st, "ivy", "user")
	call(t, srv, "GET", "/api/calendars", "", cookie)
	cal := personalCalendar(t, st, "usr_ivy")
	p := pauseWrites(t, srv, st)

	first := startImport(srv, cookie, cal.ID)
	waitEntered(t, p)
	w := refused(t, p, startImport(srv, cookie, cal.ID))
	if w.Code != http.StatusConflict || codeOf(t, w.Body.Bytes()) != "import_running" || !strings.Contains(w.Body.String(), "still running") {
		t.Fatalf("second import while the first runs: %d %s", w.Code, w.Body.String())
	}

	// Other people fill the remaining server-wide slots; one more is turned away.
	var others []<-chan *httptest.ResponseRecorder
	for i := 1; i < api.MaxConcurrentImportsForTest; i++ {
		name := fmt.Sprintf("user%d", i)
		c := loginAs(t, srv, st, name, "user")
		call(t, srv, "GET", "/api/calendars", "", c)
		others = append(others, startImport(srv, c, personalCalendar(t, st, "usr_"+name).ID))
		waitEntered(t, p)
	}
	extra := loginAs(t, srv, st, "extra", "user")
	call(t, srv, "GET", "/api/calendars", "", extra)
	w = refused(t, p, startImport(srv, extra, personalCalendar(t, st, "usr_extra").ID))
	if w.Code != http.StatusServiceUnavailable || codeOf(t, w.Body.Bytes()) != "imports_busy" {
		t.Fatalf("import over the server-wide cap: %d %s", w.Code, w.Body.String())
	}

	p.unpause()
	for _, done := range append(others, first) {
		if w := <-done; w.Code != http.StatusOK {
			t.Fatalf("import %d %s", w.Code, w.Body.String())
		}
	}
	// Slots are given back: the same person can import again.
	if code, v := importInto(t, srv, cookie, cal.ID, "?tz=UTC", importCSV); code != http.StatusOK || v.Duplicates != 2 {
		t.Fatalf("import after the first finished: %d %+v", code, v)
	}
}
