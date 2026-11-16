package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/store"
)

type mcpTask struct {
	CalendarID string `json:"calendar_id"`
	UID        string `json:"uid"`
	ETag       string `json:"etag"`
	Title      string `json:"title"`
	Status     string `json:"status"`
	Start      string `json:"start"`
	Editable   bool   `json:"editable"`
}

func TestMCPTaskTools(t *testing.T) {
	w := newMCPWorld(t)
	ctx := context.Background()
	list := &store.Calendar{ID: "cal_alice_tasks", OwnerKind: "user", OwnerID: "usr_alice", Slug: "tasks", Name: "Tasks", Components: []string{"VTODO"}}
	if err := w.st.Calendars().CreateCalendar(ctx, list, 0); err != nil {
		t.Fatal(err)
	}
	groupTasks := &store.Calendar{ID: "cal_group_tasks", OwnerKind: "group", OwnerID: "cal_group_tasks", Slug: "group", Name: "Team", Components: []string{"VTODO"}}
	if err := w.st.Calendars().CreateCalendar(ctx, groupTasks, 0); err != nil {
		t.Fatal(err)
	}
	grantRole(t, w.st, groupTasks, "editor", "usr_alice")
	bob := loginAs(t, w.srv, w.st, "bob", "user")
	bobList := taskList(t, w.srv, bob)
	bobTask := createTask(t, w.srv, bob, bobList, map[string]any{"title": "Bob's"})
	webTask := createTask(t, w.srv, w.alice, list.ID, map[string]any{"title": "From the web"})
	createTask(t, w.srv, w.alice, groupTasks.ID, map[string]any{"title": "Group task"})

	_, readTok := seedToken(t, w.st, "usr_alice", store.ScopeMCPRead)
	_, ownTok := seedToken(t, w.st, "usr_alice", store.ScopeMCPWriteOwn)
	_, anyTok := seedToken(t, w.st, "usr_alice", store.ScopeMCPWriteAny)
	read, own, anyw := agent(t, w.srv, readTok), agent(t, w.srv, ownTok), agent(t, w.srv, anyTok)

	// Reads: personal lists only; the group task never appears.
	var got struct{ Tasks []mcpTask }
	if msg := callTool(t, read, "list_tasks", map[string]any{}, &got); msg != "" || len(got.Tasks) != 1 || got.Tasks[0].Title != "From the web" || got.Tasks[0].Editable {
		t.Fatalf("list_tasks %q %+v", msg, got)
	}
	if msg := callTool(t, read, "list_tasks", map[string]any{"calendar_ids": []string{groupTasks.ID}}, nil); !strings.Contains(msg, "No such calendar") {
		t.Errorf("group list: %q", msg)
	}
	if msg := callTool(t, read, "get_task", map[string]any{"calendar_id": bobList, "uid": bobTask.UID}, nil); !strings.Contains(msg, "No such calendar") {
		t.Errorf("another user's task: %q", msg)
	}
	if msg := callTool(t, read, "create_task", map[string]any{"calendar_id": list.ID, "title": "x"}, nil); msg != "read-only token" {
		t.Errorf("read token wrote: %q", msg)
	}
	if msg := callTool(t, read, "update_task", map[string]any{"calendar_id": list.ID, "uid": webTask.UID, "etag": webTask.ETag, "status": "COMPLETED"}, nil); msg != "read-only token" {
		t.Errorf("read token completed: %q", msg)
	}

	// write-own: creates and changes only its own tasks.
	var made struct{ UID, ETag string }
	if msg := callTool(t, own, "create_task", map[string]any{"calendar_id": list.ID, "title": "Agent's", "estimate_min": 30}, &made); msg != "" {
		t.Fatalf("create_task %q", msg)
	}
	if msg := callTool(t, own, "update_task", map[string]any{"calendar_id": list.ID, "uid": webTask.UID, "etag": webTask.ETag, "title": "hijack"}, nil); msg != "this agent can only change tasks it created" {
		t.Errorf("write-own on a web task: %q", msg)
	}
	place := map[string]any{"calendar_id": list.ID, "uid": made.UID, "etag": made.ETag, "start": "2026-11-16T10:00:00+01:00", "duration_min": 30, "zone": "Europe/Berlin"}
	if msg := callTool(t, own, "update_task", place, &made); msg != "" {
		t.Fatalf("place own task %q", msg)
	}
	if msg := callTool(t, own, "update_task", map[string]any{"calendar_id": list.ID, "uid": made.UID, "etag": made.ETag, "title": strings.Repeat("x", 1001)}, nil); !strings.Contains(msg, "title") {
		t.Errorf("oversize title: %q", msg)
	}
	if msg := callTool(t, own, "create_task", map[string]any{"calendar_id": groupTasks.ID, "title": "x"}, nil); !strings.Contains(msg, "No such calendar") {
		t.Errorf("group list write: %q", msg)
	}
	if msg := callTool(t, own, "create_task", map[string]any{"calendar_id": w.personal, "title": "x"}, nil); !strings.Contains(msg, "events, not tasks") {
		t.Errorf("events calendar: %q", msg)
	}

	// write-any: any personal task, completion included.
	if msg := callTool(t, anyw, "update_task", map[string]any{"calendar_id": list.ID, "uid": webTask.UID, "etag": webTask.ETag, "status": "COMPLETED"}, nil); msg != "" {
		t.Errorf("write-any complete: %q", msg)
	}
	var one mcpTask
	if msg := callTool(t, anyw, "get_task", map[string]any{"calendar_id": list.ID, "uid": webTask.UID}, &one); msg != "" || one.Status != "COMPLETED" {
		t.Errorf("after complete %q %+v", msg, one)
	}
	if msg := callTool(t, anyw, "update_task", map[string]any{"calendar_id": bobList, "uid": bobTask.UID, "etag": bobTask.ETag, "status": "COMPLETED"}, nil); !strings.Contains(msg, "No such calendar") {
		t.Errorf("write-any on bob's task: %q", msg)
	}

	// day_gaps: the placed task is never busy; an event is, without its title.
	put := `BEGIN:VCALENDAR` + "\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:ev1\r\nDTSTAMP:20261101T000000Z\r\nDTSTART:20261116T120000Z\r\nDTEND:20261116T130000Z\r\nSUMMARY:Secret lunch\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	if _, err := w.st.Calendars().PutObject(ctx, &store.CalendarObject{CalendarID: w.personal, Name: "ev1.ics", Component: "VEVENT", UID: "ev1", Data: []byte(put),
		FirstStart: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC).Unix(), LastEnd: ptrI64(time.Date(2026, 11, 16, 13, 0, 0, 0, time.UTC).Unix())}, "", false, store.OwnerLimits{}); err != nil {
		t.Fatal(err)
	}
	var gaps struct {
		Busy []struct{ Start, End string }
		Free []struct {
			Start, End string
			Minutes    int
		}
	}
	var anyOut any
	callTool(t, read, "day_gaps", map[string]any{"day": "2026-11-16", "tz": "UTC"}, &anyOut)
	data, _ := json.Marshal(anyOut)
	raw := string(data)
	_ = json.Unmarshal(data, &gaps)
	if strings.Contains(raw, "Secret") || len(gaps.Busy) != 1 || len(gaps.Free) != 2 || gaps.Free[0].Minutes != 240 {
		t.Errorf("day_gaps %s", raw)
	}
	if msg := callTool(t, read, "day_gaps", map[string]any{"day": "soon"}, nil); msg == "" {
		t.Error("bad day accepted")
	}
}

func ptrI64(v int64) *int64 { return &v }
