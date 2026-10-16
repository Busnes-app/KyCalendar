package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/emersion/go-ical"

	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// A calendar's component set is chosen at creation, shown in every view and never patched.
func TestCalendarComponentsAPI(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	cookie := loginAs(t, srv, st, "pia", "user")
	admin := loginAs(t, srv, st, "root", "admin")
	var c struct {
		ID         string
		Components []string
	}
	for body, want := range map[string]string{
		`{"name":"Work"}`:                                 "VEVENT",
		`{"name":"Todo","components":["VTODO"]}`:          "VTODO",
		`{"name":"Both","components":["VTODO","VEVENT"]}`: "VEVENT,VTODO",
	} {
		w := call(t, srv, "POST", "/api/calendars", body, cookie)
		if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil || w.Code != http.StatusCreated || strings.Join(c.Components, ",") != want {
			t.Fatalf("create %s: %d %s, want %s", body, w.Code, w.Body.String(), want)
		}
	}
	if w := call(t, srv, "POST", "/api/calendars", `{"name":"J","components":["VJOURNAL"]}`, cookie); w.Code != http.StatusBadRequest {
		t.Fatalf("VJOURNAL: %d, want 400", w.Code)
	}
	if w := call(t, srv, "PATCH", "/api/calendars/"+c.ID, `{"components":["VEVENT"]}`, cookie); w.Code != http.StatusBadRequest {
		t.Fatalf("patching components: %d, want 400", w.Code)
	}
	w := call(t, srv, "POST", "/api/admin/calendars", `{"name":"Shared list","components":["VTODO"]}`, admin)
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil || w.Code != http.StatusCreated || strings.Join(c.Components, ",") != "VTODO" {
		t.Fatalf("group task list: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, srv, "POST", "/api/admin/calendars", `{"name":"J","components":["VJOURNAL"]}`, admin); w.Code != http.StatusBadRequest {
		t.Fatalf("group VJOURNAL: %d, want 400", w.Code)
	}
}

// The event views and the event API ignore tasks: a task list with an undated task shows no
// events, takes no events, and its tasks cannot be edited or deleted as events.
func TestEventAPIIgnoresTasks(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	cookie := loginAs(t, srv, st, "pia", "user")
	ctx := context.Background()
	list := &store.Calendar{ID: "cal_tasks", OwnerKind: "user", OwnerID: "usr_pia", Slug: "tasks", Name: "Tasks", Components: []string{"VTODO"}}
	if err := st.Calendars().CreateCalendar(ctx, list, 0); err != nil {
		t.Fatal(err)
	}
	for name, ics := range map[string]string{"task-someday.ics": taskUndated, "task-due.ics": taskDue} {
		if _, err := davbackend.Write(ctx, st, list, name, decode(t, ics), []byte(ics), "", true, store.OwnerLimits{}); err != nil {
			t.Fatal(err)
		}
	}
	w := call(t, srv, "GET", "/api/events?start=2026-10-01T00:00:00Z&end=2026-11-01T00:00:00Z", "", cookie)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("events over a task list: %d %s", w.Code, w.Body.String())
	}
	w = call(t, srv, "POST", "/api/calendars/"+list.ID+"/events", `{"title":"A","start":"2026-10-07T09:00:00Z","end":"2026-10-07T10:00:00Z","zone":"UTC"}`, cookie)
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "tasks_only") {
		t.Fatalf("event into a task list: %d %s", w.Code, w.Body.String())
	}
	o, _ := st.Calendars().GetObjectByUID(ctx, list.ID, "task-due")
	if w := withIfMatch(t, srv, "PUT", "/api/events/"+list.ID+"/task-due", `{"title":"x","start":"2026-10-07T09:00:00Z","end":"2026-10-07T10:00:00Z","zone":"UTC"}`, o.ETag, cookie); w.Code != http.StatusNotFound {
		t.Fatalf("editing a task as an event: %d %s", w.Code, w.Body.String())
	}
	if w := withIfMatch(t, srv, "DELETE", "/api/events/"+list.ID+"/task-due", "", o.ETag, cookie); w.Code != http.StatusNotFound {
		t.Fatalf("deleting a task as an event: %d %s", w.Code, w.Body.String())
	}
	if _, err := st.Calendars().GetObjectByUID(ctx, list.ID, "task-due"); err != nil {
		t.Fatalf("task after the refused event calls: %v", err)
	}
}

// An agent sees which calendars hold events, and its event tools cannot reach a task.
func TestMCPIgnoresTasks(t *testing.T) {
	w := newMCPWorld(t)
	ctx := context.Background()
	_, tok := seedToken(t, w.st, "usr_alice", store.ScopeMCPWriteAny)
	cs := agent(t, w.srv, tok)
	var cals struct {
		Calendars []struct {
			ID         string
			Components []string
		} `json:"calendars"`
	}
	if msg := callTool(t, cs, "list_calendars", map[string]any{}, &cals); msg != "" || len(cals.Calendars) != 1 || strings.Join(cals.Calendars[0].Components, ",") != "VEVENT" {
		t.Fatalf("list_calendars: %q %+v", msg, cals)
	}

	both := &store.Calendar{ID: "cal_both", OwnerKind: "user", OwnerID: "usr_alice", Slug: "both", Name: "Both", Components: []string{"VEVENT", "VTODO"}}
	if err := w.st.Calendars().CreateCalendar(ctx, both, 0); err != nil {
		t.Fatal(err)
	}
	o, err := davbackend.Write(ctx, w.st, both, "task-due.ics", decode(t, taskDue), []byte(taskDue), "", true, store.OwnerLimits{})
	if err != nil {
		t.Fatal(err)
	}
	update := map[string]any{"calendar_id": both.ID, "uid": "task-due", "etag": o.ETag, "title": "hijacked"}
	if msg := callTool(t, cs, "update_event", update, nil); !strings.Contains(msg, "No such event") {
		t.Fatalf("update_event on a task: %q, want No such event", msg)
	}
}

func decode(t *testing.T, s string) *ical.Calendar {
	t.Helper()
	cal, err := ical.NewDecoder(strings.NewReader(s)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	return cal
}
