package api_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/store"
)

type taskOut struct {
	CalendarID   string   `json:"calendar_id"`
	UID          string   `json:"uid"`
	ETag         string   `json:"etag"`
	Title        string   `json:"title"`
	Status       string   `json:"status"`
	Priority     int      `json:"priority"`
	Due          string   `json:"due"`
	Tags         []string `json:"tags"`
	Parent       string   `json:"parent"`
	Subtasks     int      `json:"subtasks"`
	SubtasksDone int      `json:"subtasks_done"`
	Editable     bool     `json:"editable"`
	RRule        string   `json:"rrule"`
}

func taskReq(t *testing.T, srv *api.Server, method, path, etag string, cookie *http.Cookie, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r *bytes.Reader
	if s, ok := body.(string); ok {
		r = bytes.NewReader([]byte(s))
	} else {
		data, _ := json.Marshal(body)
		r = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, path, r)
	req.Header.Set("Content-Type", "application/json")
	if etag != "" {
		req.Header.Set("If-Match", `"`+etag+`"`)
	}
	req.AddCookie(cookie)
	addCSRF(req, cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

// taskList makes a personal VTODO list for the user.
func taskList(t *testing.T, srv *api.Server, cookie *http.Cookie) string {
	t.Helper()
	w := doJSON(t, srv, "POST", "/api/calendars", cookie, map[string]any{"name": "Errands", "components": []string{"VTODO"}})
	if w.Code != http.StatusCreated {
		t.Fatalf("create list %d %s", w.Code, w.Body)
	}
	var c struct{ ID string }
	_ = json.Unmarshal(w.Body.Bytes(), &c)
	return c.ID
}

func listTasks(t *testing.T, srv *api.Server, cookie *http.Cookie, query string) []taskOut {
	t.Helper()
	w := taskReq(t, srv, "GET", "/api/tasks?tz=Europe/Berlin&"+query, "", cookie, "")
	if w.Code != http.StatusOK {
		t.Fatalf("list %s: %d %s", query, w.Code, w.Body)
	}
	var out struct{ Tasks []taskOut }
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out.Tasks
}

func createTask(t *testing.T, srv *api.Server, cookie *http.Cookie, list string, body map[string]any) taskOut {
	t.Helper()
	w := taskReq(t, srv, "POST", "/api/calendars/"+list+"/tasks", "", cookie, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("create task %d %s", w.Code, w.Body)
	}
	var out taskOut
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out
}

func TestTaskLifecycle(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	alice := loginAs(t, srv, st, "alice", "user")
	list := taskList(t, srv, alice)
	parent := createTask(t, srv, alice, list, map[string]any{"title": "Trip", "priority": 1, "due": "2026-11-20", "tags": []string{"Travel"}})
	child := createTask(t, srv, alice, list, map[string]any{"title": "Pack", "parent": parent.UID})
	_ = child
	got := listTasks(t, srv, alice, "")
	if len(got) != 2 || got[0].Title != "Trip" || got[0].Subtasks != 1 || got[0].Priority != 1 || !got[0].Editable {
		t.Fatalf("list %+v", got)
	}
	// Complete the child: it leaves the open list and counts on the parent.
	var c taskOut
	for _, x := range got {
		if x.UID == child.UID {
			c = x
		}
	}
	if w := taskReq(t, srv, "PATCH", "/api/tasks/"+list+"/"+c.UID, "", alice, map[string]any{"status": "COMPLETED"}); w.Code != http.StatusPreconditionRequired {
		t.Fatalf("no If-Match: %d", w.Code)
	}
	if w := taskReq(t, srv, "PATCH", "/api/tasks/"+list+"/"+c.UID, "stale", alice, map[string]any{"status": "COMPLETED"}); w.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale If-Match: %d", w.Code)
	}
	if w := taskReq(t, srv, "PATCH", "/api/tasks/"+list+"/"+c.UID, c.ETag, alice, map[string]any{"status": "COMPLETED"}); w.Code != http.StatusOK {
		t.Fatalf("complete: %d %s", w.Code, w.Body)
	}
	got = listTasks(t, srv, alice, "")
	if len(got) != 1 || got[0].SubtasksDone != 1 {
		t.Fatalf("after completing %+v", got)
	}
	if done := listTasks(t, srv, alice, "status=completed"); len(done) != 1 || done[0].UID != child.UID {
		t.Fatalf("completed view %+v", done)
	}
	comps, _ := st.Tasks().Completions(context.Background(), "usr_alice", time.Time{})
	if len(comps) != 1 {
		t.Fatalf("completion log %+v", comps)
	}
	// Filters.
	if f := listTasks(t, srv, alice, "priority=high&tags=travel&due_from=2026-11-20&due_to=2026-11-20"); len(f) != 1 {
		t.Fatalf("filters %+v", f)
	}
	if f := listTasks(t, srv, alice, "priority=low"); len(f) != 0 {
		t.Fatalf("priority filter %+v", f)
	}
	// Search, then delete.
	if f := listTasks(t, srv, alice, "q=tri"); len(f) != 1 {
		t.Fatalf("search %+v", f)
	}
	if w := taskReq(t, srv, "DELETE", "/api/tasks/"+list+"/"+got[0].UID, got[0].ETag, alice, ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete %d", w.Code)
	}
	if f := listTasks(t, srv, alice, "q=trip"); len(f) != 0 {
		t.Fatalf("deleted task still found %+v", f)
	}
}

func TestTaskBoundaries(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	alice := loginAs(t, srv, st, "alice", "user")
	list := taskList(t, srv, alice)
	for name, body := range map[string]map[string]any{
		"no title":       {},
		"blank title":    {"title": "  "},
		"long title":     {"title": strings.Repeat("x", 1001)},
		"control":        {"title": "a\x07b"},
		"long notes":     {"title": "t", "notes": strings.Repeat("x", 65537)},
		"many tags":      {"title": "t", "tags": strings.Split(strings.Repeat("a,", 21), ",")[:21]},
		"comma tag":      {"title": "t", "tags": []string{"a,b"}},
		"long tag":       {"title": "t", "tags": []string{strings.Repeat("x", 65)}},
		"priority":       {"title": "t", "priority": 10},
		"status":         {"title": "t", "status": "DONE"},
		"due":            {"title": "t", "due": "tomorrow"},
		"year":           {"title": "t", "due": "1800-01-01"},
		"zone":           {"title": "t", "zone": "Local"},
		"estimate":       {"title": "t", "estimate_min": 1441},
		"rrule":          {"title": "t", "due": "2026-11-20", "rrule": "FREQ=SECONDLY"},
		"rrule garbage":  {"title": "t", "due": "2026-11-20", "rrule": "FREQ=DAILY\r\nX-EVIL:1"},
		"rrule undated":  {"title": "t", "rrule": "FREQ=DAILY"},
		"missing parent": {"title": "t", "parent": "nope"},
		"reminders":      {"title": "t", "reminders": []map[string]any{{"at": "x"}}},
		"file":           {"title": "t", "files": []map[string]any{{"name": "a", "url": "http://x"}}},
	} {
		if w := taskReq(t, srv, "POST", "/api/calendars/"+list+"/tasks", "", alice, body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	if w := taskReq(t, srv, "POST", "/api/calendars/"+list+"/tasks", "", alice, strings.Repeat(" ", 300<<10)+"{}"); w.Code != http.StatusBadRequest {
		t.Errorf("oversize body: %d", w.Code)
	}
	// Depth: three levels, never four, and no cycles.
	a := createTask(t, srv, alice, list, map[string]any{"title": "a"})
	b := createTask(t, srv, alice, list, map[string]any{"title": "b", "parent": a.UID})
	c := createTask(t, srv, alice, list, map[string]any{"title": "c", "parent": b.UID})
	if w := taskReq(t, srv, "POST", "/api/calendars/"+list+"/tasks", "", alice, map[string]any{"title": "d", "parent": c.UID}); w.Code != http.StatusBadRequest {
		t.Errorf("fourth level: %d", w.Code)
	}
	for _, x := range listTasks(t, srv, alice, "") {
		if x.UID == a.UID {
			if w := taskReq(t, srv, "PATCH", "/api/tasks/"+list+"/"+a.UID, x.ETag, alice, map[string]any{"parent": c.UID}); w.Code != http.StatusBadRequest {
				t.Errorf("cycle: %d", w.Code)
			}
		}
	}
	// An events-only calendar takes no task.
	w := doJSON(t, srv, "POST", "/api/calendars", alice, map[string]any{"name": "Events"})
	var ev struct{ ID string }
	_ = json.Unmarshal(w.Body.Bytes(), &ev)
	if w := taskReq(t, srv, "POST", "/api/calendars/"+ev.ID+"/tasks", "", alice, map[string]any{"title": "t"}); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("events-only: %d", w.Code)
	}
}

func TestTaskAccess(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	alice := loginAs(t, srv, st, "alice", "user")
	bob := loginAs(t, srv, st, "bob", "user")
	carol := loginAs(t, srv, st, "carol", "user")
	adm := loginAs(t, srv, st, "root", "admin")
	list := taskList(t, srv, alice)
	mine := createTask(t, srv, alice, list, map[string]any{"title": "Private"})
	group := &store.Calendar{ID: "cal_team_tasks", OwnerKind: "group", OwnerID: "cal_team_tasks", Slug: "group", Name: "Team", Components: []string{"VTODO"}}
	if err := st.Calendars().CreateCalendar(ctx, group, 0); err != nil {
		t.Fatal(err)
	}
	grantRole(t, st, group, "reader", "usr_bob")
	grantRole(t, st, group, "editor", "usr_carol")
	shared := createTask(t, srv, carol, group.ID, map[string]any{"title": "Shared"})

	// Another user's list is missing, for reads and writes.
	if w := taskReq(t, srv, "PATCH", "/api/tasks/"+list+"/"+mine.UID, mine.ETag, bob, map[string]any{"status": "COMPLETED"}); w.Code != http.StatusNotFound {
		t.Errorf("bob on alice's task: %d", w.Code)
	}
	if w := taskReq(t, srv, "GET", "/api/tasks?lists="+list, "", bob, ""); w.Code != http.StatusNotFound {
		t.Errorf("bob lists alice's list: %d", w.Code)
	}
	if got := listTasks(t, srv, bob, "q=private"); len(got) != 0 {
		t.Errorf("search reached another user's list: %+v", got)
	}
	// A reader sees the shared task, read-only, and cannot complete it.
	got := listTasks(t, srv, bob, "")
	if len(got) != 1 || got[0].UID != shared.UID || got[0].Editable {
		t.Fatalf("reader's list %+v", got)
	}
	if w := taskReq(t, srv, "PATCH", "/api/tasks/"+group.ID+"/"+shared.UID, got[0].ETag, bob, map[string]any{"status": "COMPLETED"}); w.Code != http.StatusForbidden {
		t.Errorf("reader completed a task: %d", w.Code)
	}
	if w := taskReq(t, srv, "DELETE", "/api/tasks/"+group.ID+"/"+shared.UID, got[0].ETag, bob, ""); w.Code != http.StatusForbidden {
		t.Errorf("reader deleted a task: %d", w.Code)
	}
	if w := taskReq(t, srv, "PATCH", "/api/tasks/"+group.ID+"/"+shared.UID, got[0].ETag, carol, map[string]any{"status": "COMPLETED"}); w.Code != http.StatusOK {
		t.Errorf("editor completed: %d %s", w.Code, w.Body)
	}
	// Administrators never reach tasks.
	if w := taskReq(t, srv, "GET", "/api/tasks", "", adm, ""); w.Code != http.StatusForbidden {
		t.Errorf("admin listed tasks: %d", w.Code)
	}
	// An event UID is not a task.
	if w := taskReq(t, srv, "PATCH", "/api/tasks/"+list+"/no-such", "x", alice, map[string]any{}); w.Code != http.StatusNotFound {
		t.Errorf("missing task: %d", w.Code)
	}
}

func TestTaskSearchIsRateLimited(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	alice := loginAs(t, srv, st, "alice", "user")
	taskList(t, srv, alice)
	for i := range 31 {
		w := taskReq(t, srv, "GET", "/api/tasks?q=x", "", alice, "")
		if i < 30 && w.Code != http.StatusOK || i == 30 && w.Code != http.StatusTooManyRequests {
			t.Fatalf("search %d: %d", i, w.Code)
		}
	}
	if w := taskReq(t, srv, "GET", "/api/tasks?q="+strings.Repeat("a+", 11), "", alice, ""); w.Code != http.StatusBadRequest {
		t.Errorf("11 words: %d", w.Code)
	}
}

func TestTaskWebEditKeepsClientProperties(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	alice := loginAs(t, srv, st, "alice", "user")
	list := taskList(t, srv, alice)
	o := &store.CalendarObject{CalendarID: list, Name: "task-full.ics", Component: "VTODO", UID: "task-full", Data: []byte(taskFull)}
	if _, err := st.Calendars().PutObject(context.Background(), o, "", false, store.OwnerLimits{}); err != nil {
		t.Fatal(err)
	}
	stored, _ := st.Calendars().GetObjectByUID(context.Background(), list, "task-full")
	if w := taskReq(t, srv, "PATCH", "/api/tasks/"+list+"/task-full", stored.ETag, alice, map[string]any{"title": "Renew it"}); w.Code != http.StatusOK {
		t.Fatalf("edit %d %s", w.Code, w.Body)
	}
	after, _ := st.Calendars().GetObjectByUID(context.Background(), list, "task-full")
	text := strings.ReplaceAll(string(after.Data), "\r\n ", "")
	for _, keep := range []string{"X-APPLE-SORT-ORDER:42", "X-TASKS-ORG;X-P=1:kept", "RELATED-TO;RELTYPE=PARENT:task-parent", "RRULE:FREQ=WEEKLY;COUNT=4",
		"URL:https://registrar.example/renew?d=example.com", "TRIGGER;RELATED=END:-PT1H", "PRODID:+//IDN tasks.org//android-140102//EN", "SUMMARY:Renew it"} {
		if !strings.Contains(text, keep) {
			t.Errorf("lost %q:\n%s", keep, text)
		}
	}
}

func TestTaskAreasAndViews(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	alice := loginAs(t, srv, st, "alice", "user")
	bob := loginAs(t, srv, st, "bob", "user")
	list := taskList(t, srv, alice)
	bobs := taskList(t, srv, bob)
	if w := taskReq(t, srv, "PUT", "/api/tasks/areas", "", alice, []map[string]any{{"id": "work", "name": "Work", "lists": []string{bobs}}}); w.Code != http.StatusNotFound {
		t.Errorf("area with another user's list: %d", w.Code)
	}
	if w := taskReq(t, srv, "PUT", "/api/tasks/areas", "", alice, []map[string]any{{"id": "work", "name": "Work", "lists": []string{list}}}); w.Code != http.StatusOK {
		t.Fatalf("areas %d %s", w.Code, w.Body)
	}
	createTask(t, srv, alice, list, map[string]any{"title": "In work"})
	if got := listTasks(t, srv, alice, "area=work"); len(got) != 1 {
		t.Errorf("area filter %+v", got)
	}
	if w := taskReq(t, srv, "POST", "/api/tasks/views", "", alice, map[string]any{"name": "Hot", "query": map[string]any{"priority": []string{"high"}, "evil": 1}}); w.Code != http.StatusBadRequest {
		t.Errorf("unknown query key: %d", w.Code)
	}
	w := taskReq(t, srv, "POST", "/api/tasks/views", "", alice, map[string]any{"name": "Hot", "query": map[string]any{"priority": []string{"high"}}})
	if w.Code != http.StatusCreated {
		t.Fatalf("view %d %s", w.Code, w.Body)
	}
	var v struct{ ID string }
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	if w := taskReq(t, srv, "DELETE", "/api/tasks/views/"+v.ID, "", bob, ""); w.Code != http.StatusNotFound {
		t.Errorf("bob deleted alice's view: %d", w.Code)
	}
	// The inbox is created once and then reused.
	var first, second struct {
		CalendarID string `json:"calendar_id"`
	}
	_ = json.Unmarshal(taskReq(t, srv, "POST", "/api/tasks/inbox", "", alice, "").Body.Bytes(), &first)
	_ = json.Unmarshal(taskReq(t, srv, "POST", "/api/tasks/inbox", "", alice, "").Body.Bytes(), &second)
	if first.CalendarID == "" || first != second {
		t.Errorf("inbox %v %v", first, second)
	}
}

// Every rule the web form builds is accepted, with each way it can end.
func TestTaskAcceptsEveryWebRepeat(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	alice := loginAs(t, srv, st, "alice", "user")
	list := taskList(t, srv, alice)
	for _, rule := range []string{"FREQ=DAILY", "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR", "FREQ=WEEKLY;BYDAY=TU;INTERVAL=2", "FREQ=MONTHLY;BYMONTHDAY=10",
		"FREQ=MONTHLY;BYDAY=2TU", "FREQ=MONTHLY;BYDAY=-1FR", "FREQ=MONTHLY;BYMONTHDAY=-1;UNTIL=20270630", "FREQ=YEARLY;COUNT=5"} {
		task := createTask(t, srv, alice, list, map[string]any{"title": rule, "due": "2026-11-10", "rrule": rule})
		if w := taskReq(t, srv, "PATCH", "/api/tasks/"+list+"/"+task.UID, task.ETag, alice, map[string]any{"status": "COMPLETED"}); w.Code != http.StatusOK {
			t.Errorf("%s: complete %d %s", rule, w.Code, w.Body)
		}
	}
}

func TestTaskPastedImage(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	alice := loginAs(t, srv, st, "alice", "user")
	bob := loginAs(t, srv, st, "bob", "user")
	list := taskList(t, srv, alice)
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{1}, 100)...)
	enc := base64.StdEncoding.EncodeToString
	made := createTask(t, srv, alice, list, map[string]any{"title": "Screenshot 21:05", "image": map[string]any{"name": "Screenshot 21:05.png", "data": enc(png)}})
	for name, img := range map[string]map[string]any{
		"svg":       {"name": "x.svg", "data": enc([]byte(`<svg onload="alert(1)"/>`))},
		"gif":       {"name": "x.gif", "data": enc([]byte("GIF89a......"))},
		"oversize":  {"name": "x.png", "data": enc(append(png, make([]byte, 512<<10)...))},
		"not b64":   {"name": "x.png", "data": "!!!"},
		"bad name":  {"name": "a\"b.png", "data": enc(png)},
		"ctrl name": {"name": "a\nb.png", "data": enc(png)},
	} {
		if w := taskReq(t, srv, "POST", "/api/calendars/"+list+"/tasks", "", alice, map[string]any{"title": "t", "image": img}); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	w := taskReq(t, srv, "GET", "/api/tasks/"+list+"/"+made.UID+"/images/0", "", alice, "")
	if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), png) || w.Header().Get("Content-Type") != "image/png" ||
		w.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("image: %d %v", w.Code, w.Header())
	}
	if w := taskReq(t, srv, "GET", "/api/tasks/"+list+"/"+made.UID+"/images/1", "", alice, ""); w.Code != http.StatusNotFound {
		t.Errorf("missing index: %d", w.Code)
	}
	if w := taskReq(t, srv, "GET", "/api/tasks/"+list+"/"+made.UID+"/images/0", "", bob, ""); w.Code != http.StatusNotFound {
		t.Errorf("bob read alice's image: %d", w.Code)
	}
	// A client-written ATTACH that claims PNG but holds HTML is never served.
	evil := strings.Replace(todoICS("evil"), "END:VTODO", "ATTACH;ENCODING=BASE64;VALUE=BINARY;FMTTYPE=image/png:"+enc([]byte("<script>alert(1)</script>"))+"\r\nEND:VTODO", 1)
	if _, err := st.Calendars().PutObject(context.Background(), &store.CalendarObject{CalendarID: list, Name: "evil.ics", Component: "VTODO", UID: "evil", Data: []byte(evil)}, "", false, store.OwnerLimits{}); err != nil {
		t.Fatal(err)
	}
	if w := taskReq(t, srv, "GET", "/api/tasks/"+list+"/evil/images/0", "", alice, ""); w.Code != http.StatusNotFound {
		t.Errorf("served a non-image: %d", w.Code)
	}
}

// A CalDAV client stores a rule that never matches; completing it on the web is refused at once
// and the stored task is untouched (security review, PR 158).
func TestTaskCompleteUnreachableRule(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	alice := loginAs(t, srv, st, "alice", "user")
	list := taskList(t, srv, alice)
	ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VTODO\r\nUID:never\r\nDTSTAMP:20261001T000000Z\r\nSUMMARY:Never\r\n" +
		"DUE:20261101T090000Z\r\nRRULE:FREQ=HOURLY;BYMONTH=2;BYMONTHDAY=30\r\nEND:VTODO\r\nEND:VCALENDAR\r\n"
	if _, err := st.Calendars().PutObject(context.Background(), &store.CalendarObject{CalendarID: list, Name: "never.ics", Component: "VTODO", UID: "never", Data: []byte(ics)}, "", false, store.OwnerLimits{}); err != nil {
		t.Fatal(err)
	}
	o, _ := st.Calendars().GetObjectByUID(context.Background(), list, "never")
	start := time.Now()
	w := taskReq(t, srv, "PATCH", "/api/tasks/"+list+"/never", o.ETag, alice, map[string]any{"status": "COMPLETED"})
	if w.Code != http.StatusBadRequest || time.Since(start) > 2*time.Second {
		t.Fatalf("complete: %d after %v", w.Code, time.Since(start))
	}
	if after, _ := st.Calendars().GetObjectByUID(context.Background(), list, "never"); after.ETag != o.ETag {
		t.Fatal("the stored task changed")
	}
}

// Completions from any client feed the log once (owner answer, 2026-11-15): a phone's completion
// over CalDAV counts, the web's completion synced back from the phone does not count again, and
// un-completing on the phone removes it.
func TestTaskCompletionsFromEveryClient(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	token := ssoUser(t, st, "dana")
	dana, _ := st.Users().GetUserByID(context.Background(), "usr_dana")
	cookie := sessionFor(t, st, dana)
	list := taskList(t, srv, cookie)
	cals, _ := st.Calendars().ListCalendarsByOwner(context.Background(), "user", "usr_dana")
	var slug string
	for _, c := range cals {
		if c.ID == list {
			slug = c.Slug
		}
	}
	path := "/dav/usr_dana/calendars/" + slug + "/phone.ics"
	open := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nBEGIN:VTODO\r\nUID:phone\r\nDTSTAMP:20261101T000000Z\r\nCREATED:20261101T000000Z\r\nSUMMARY:Run\r\nDUE;VALUE=DATE:20261110\r\nSTATUS:NEEDS-ACTION\r\nEND:VTODO\r\nEND:VCALENDAR\r\n"
	done := strings.Replace(open, "STATUS:NEEDS-ACTION", "STATUS:COMPLETED\r\nCOMPLETED:20261112T080000Z", 1)
	put := func(body string) {
		t.Helper()
		if r := rawDAV(t, ts, "PUT", path, "dana", token, body, map[string]string{"Content-Type": "text/calendar"}); r.StatusCode >= 300 {
			t.Fatalf("PUT %d", r.StatusCode)
		}
	}
	count := func() int {
		got, err := st.Tasks().Completions(context.Background(), "usr_dana", time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		return len(got)
	}
	put(open)
	put(done)
	if n := count(); n != 1 {
		t.Fatalf("phone completion: %d rows", n)
	}
	put(done) // the same completion synced again
	if n := count(); n != 1 {
		t.Fatalf("repeated sync: %d rows", n)
	}
	put(open) // un-completed on the phone
	if n := count(); n != 0 {
		t.Fatalf("un-complete: %d rows", n)
	}
	// Web completion, then the phone syncs it back: one row.
	o, _ := st.Calendars().GetObjectByUID(context.Background(), list, "phone")
	if w := taskReq(t, srv, "PATCH", "/api/tasks/"+list+"/phone", o.ETag, cookie, map[string]any{"status": "COMPLETED"}); w.Code != http.StatusOK {
		t.Fatalf("web complete %d", w.Code)
	}
	after, _ := st.Calendars().GetObjectByUID(context.Background(), list, "phone")
	put(string(after.Data))
	if n := count(); n != 1 {
		t.Fatalf("web then phone: %d rows", n)
	}
}

func TestGoalsHabitsInsights(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	alice := loginAs(t, srv, st, "alice", "user")
	bob := loginAs(t, srv, st, "bob", "user")
	list := taskList(t, srv, alice)
	bobs := taskList(t, srv, bob)
	goal := func(cookie *http.Cookie, lists []string) *httptest.ResponseRecorder {
		return taskReq(t, srv, "POST", "/api/tasks/goals", "", cookie, map[string]any{"title": "Ship the relaunch", "period": "quarter", "starts": "2026-10-01", "ends": "2026-12-31", "lists": lists})
	}
	if w := goal(alice, []string{bobs}); w.Code != http.StatusNotFound {
		t.Errorf("goal on bob's list: %d", w.Code)
	}
	for name, b := range map[string]map[string]any{
		"long period": {"title": "x", "period": "custom", "starts": "2026-01-01", "ends": "2027-06-01"},
		"bad period":  {"title": "x", "period": "decade", "starts": "2026-01-01", "ends": "2026-02-01"},
		"no title":    {"title": " ", "period": "year", "starts": "2026-01-01", "ends": "2026-12-31"},
	} {
		if w := taskReq(t, srv, "POST", "/api/tasks/goals", "", alice, b); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	w := goal(alice, []string{list})
	if w.Code != http.StatusCreated {
		t.Fatalf("goal %d %s", w.Code, w.Body)
	}
	var g struct{ ID string }
	_ = json.Unmarshal(w.Body.Bytes(), &g)
	if w := taskReq(t, srv, "GET", "/api/tasks/goals/"+g.ID+"/progress?tz=UTC", "", bob, ""); w.Code != http.StatusNotFound {
		t.Errorf("bob read alice's goal: %d", w.Code)
	}
	done := createTask(t, srv, alice, list, map[string]any{"title": "Done one"})
	if w := taskReq(t, srv, "PATCH", "/api/tasks/"+list+"/"+done.UID, done.ETag, alice, map[string]any{"status": "COMPLETED"}); w.Code != http.StatusOK {
		t.Fatal(w.Code)
	}
	createTask(t, srv, alice, list, map[string]any{"title": "Open one", "due": "2020-01-01"})
	var prog struct {
		Lists []struct {
			Done, Open int
			Stalled    bool
		}
	}
	_ = json.Unmarshal(taskReq(t, srv, "GET", "/api/tasks/goals/"+g.ID+"/progress?tz=UTC", "", alice, "").Body.Bytes(), &prog)
	if len(prog.Lists) != 1 || prog.Lists[0].Open != 1 || prog.Lists[0].Stalled {
		t.Errorf("progress %+v", prog)
	}
	// Habits: a plain task is refused; a daily habit answers its streak.
	if w := taskReq(t, srv, "GET", "/api/tasks/"+list+"/"+done.UID+"/habit?tz=UTC", "", alice, ""); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("not a habit: %d", w.Code)
	}
	h := createTask(t, srv, alice, list, map[string]any{"title": "Stretch", "due": time.Now().Format(time.DateOnly), "rrule": "FREQ=DAILY", "habit": true})
	if w := taskReq(t, srv, "PATCH", "/api/tasks/"+list+"/"+h.UID, h.ETag, alice, map[string]any{"status": "COMPLETED"}); w.Code != http.StatusOK {
		t.Fatal(w.Code)
	}
	var hv struct{ Current, Longest int }
	_ = json.Unmarshal(taskReq(t, srv, "GET", "/api/tasks/"+list+"/"+h.UID+"/habit?tz=UTC", "", alice, "").Body.Bytes(), &hv)
	if hv.Current != 1 || hv.Longest != 1 {
		t.Errorf("habit %+v", hv)
	}
	// Insights: 12 weeks, the list's open and overdue, 7 days.
	var in struct {
		Weeks []struct{ Done int }
		Lists []struct{ Open, Overdue int }
		Days  []struct{ FreeMin int `json:"free_min"` }
	}
	_ = json.Unmarshal(taskReq(t, srv, "GET", "/api/tasks/insights?tz=UTC", "", alice, "").Body.Bytes(), &in)
	if len(in.Weeks) != 12 || in.Weeks[11].Done != 2 || len(in.Days) != 7 || in.Days[0].FreeMin != 600 {
		t.Errorf("insights %+v", in)
	}
	found := false
	for _, l := range in.Lists {
		found = found || l.Overdue == 1
	}
	if !found {
		t.Errorf("no overdue list %+v", in.Lists)
	}
}
