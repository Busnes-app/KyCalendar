package api_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Busnes-app/kycalendar/internal/agentskill"
	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/apppass"
	"github.com/Busnes-app/kycalendar/internal/appversion"
	"github.com/Busnes-app/kycalendar/internal/auth"
	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// mcpWorld is alice (everyday, editor on a group calendar) with her personal calendar.
type mcpWorld struct {
	srv      *api.Server
	st       store.Store
	alice    *http.Cookie
	personal string
	group    *store.Calendar
}

func newMCPWorld(t *testing.T) *mcpWorld {
	t.Helper()
	srv, st, _ := setupTestServer(t)
	w := &mcpWorld{srv: srv, st: st, alice: loginAs(t, srv, st, "alice", "user")}
	ctx := context.Background()
	if err := davbackend.EnsureDefault(ctx, st, "usr_alice", 0); err != nil {
		t.Fatal(err)
	}
	cals, err := st.Calendars().ListCalendarsByOwner(ctx, "user", "usr_alice")
	if err != nil || len(cals) != 1 {
		t.Fatalf("personal calendars: %v %d", err, len(cals))
	}
	w.personal = cals[0].ID
	w.group = groupCalendar(t, st, "Rooms")
	grantRole(t, st, w.group, "editor", "usr_alice")
	return w
}

// token seeds an app password of scope for userID and returns its ID and token.
func seedToken(t *testing.T, st store.Store, userID, scope string) (string, string) {
	t.Helper()
	id, token, hash, err := apppass.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AppPasswords().Create(context.Background(), store.Seed, &store.AppPassword{ID: id, UserID: userID, Label: "agent", Hash: hash, Scope: scope}, 0); err != nil {
		t.Fatal(err)
	}
	return id, token
}

// rawMCP posts one tools/list request to /mcp with the given headers.
func rawMCP(t *testing.T, srv http.Handler, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

type bearer struct {
	token string
	base  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}

// agent connects the SDK client to srv with token, as OpenClaw and Hermes Agent do.
func agent(t *testing.T, srv http.Handler, token string) *mcp.ClientSession {
	t.Helper()
	hs := httptest.NewServer(srv)
	t.Cleanup(hs.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "test-agent", Version: "1"}, nil)
	cs, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: hs.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{token, http.DefaultTransport}}}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// callTool calls name and decodes its structured result into out; it returns the tool error
// text, "" on success.
func callTool(t *testing.T, cs *mcp.ClientSession, name string, args any, out any) string {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error %v", name, err)
	}
	if res.IsError {
		var text []string
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				text = append(text, tc.Text)
			}
		}
		return cmpOr(strings.Join(text, " "), "tool error")
	}
	if out != nil {
		data, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("%s: decode %s: %v", name, data, err)
		}
	}
	return ""
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

type mcpEvent struct {
	CalendarID           string `json:"calendar_id"`
	UID                  string `json:"uid"`
	RecurrenceID         string `json:"recurrence_id"`
	ETag                 string `json:"etag"`
	Title                string `json:"title"`
	Location             string `json:"location"`
	Description          string `json:"description"`
	DescriptionTruncated bool   `json:"description_truncated"`
	Start                string `json:"start"`
	Recurring            bool   `json:"recurring"`
	Override             bool   `json:"override"`
	Editable             bool   `json:"editable"`
}

type written struct {
	CalendarID string `json:"calendar_id"`
	UID        string `json:"uid"`
	ETag       string `json:"etag"`
}

func listEvents(t *testing.T, cs *mcp.ClientSession, args map[string]any) []mcpEvent {
	t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	args["start"], args["end"] = "2026-10-01T00:00:00Z", "2026-11-01T00:00:00Z"
	var out struct {
		Events []mcpEvent `json:"events"`
	}
	if msg := callTool(t, cs, "list_events", args, &out); msg != "" {
		t.Fatalf("list_events: %s", msg)
	}
	return out.Events
}

func findEvent(events []mcpEvent, uid string) *mcpEvent {
	for i := range events {
		if events[i].UID == uid {
			return &events[i]
		}
	}
	return nil
}

func createArgs(cal, title string) map[string]any {
	return map[string]any{"calendar_id": cal, "title": title, "start": "2026-10-20T14:00:00Z", "end": "2026-10-20T15:00:00Z", "zone": "UTC"}
}

func TestMCPAuthentication(t *testing.T) {
	w := newMCPWorld(t)
	_, read := seedToken(t, w.st, "usr_alice", store.ScopeMCPRead)
	_, dav := seedToken(t, w.st, "usr_alice", store.ScopeDAV)
	loginAs(t, w.srv, w.st, "root", "admin")
	_, adminTok := seedToken(t, w.st, "usr_root", store.ScopeMCPRead)

	if got := rawMCP(t, w.srv, map[string]string{"Authorization": "Bearer " + read}).Code; got != http.StatusOK {
		t.Fatalf("valid MCP token: got %d, want 200", got)
	}
	none := rawMCP(t, w.srv, nil)
	if none.Code != http.StatusUnauthorized || !strings.HasPrefix(none.Header().Get("WWW-Authenticate"), "Bearer") {
		t.Errorf("no credentials: got %d %q, want 401 with a Bearer challenge", none.Code, none.Header().Get("WWW-Authenticate"))
	}
	for name, c := range map[string]struct {
		header map[string]string
		want   int
	}{
		"browser origin with a valid token": {map[string]string{"Authorization": "Bearer " + read, "Origin": "https://evil.example"}, 403},
		"same-site origin too":              {map[string]string{"Authorization": "Bearer " + read, "Origin": "http://localhost:8080"}, 403},
		"dav token":                         {map[string]string{"Authorization": "Bearer " + dav}, 401},
		"token over Basic":                  {map[string]string{"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:"+read))}, 401},
		"wrong secret":                      {map[string]string{"Authorization": "Bearer " + wrongSecret(read)}, 401},
		"administrator":                     {map[string]string{"Authorization": "Bearer " + adminTok}, 403},
	} {
		if got := rawMCP(t, w.srv, c.header).Code; got != c.want {
			t.Errorf("%s: got %d, want %d", name, got, c.want)
		}
	}
	// The preflight gets no CORS grant and no 200 short-circuit.
	req := httptest.NewRequest("OPTIONS", "/mcp", nil)
	req.Header.Set("Origin", "http://localhost:8080")
	rec := httptest.NewRecorder()
	w.srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("preflight: got %d, ACAO %q; want 403 and no grant", rec.Code, rec.Header().Get("Access-Control-Allow-Origin"))
	}

	// An MCP token never logs in over CalDAV.
	dreq := httptest.NewRequest("PROPFIND", "/dav/usr_alice/calendars/", nil)
	dreq.SetBasicAuth("alice", read)
	drec := httptest.NewRecorder()
	w.srv.ServeHTTP(drec, dreq)
	if drec.Code != http.StatusUnauthorized {
		t.Errorf("MCP token over CalDAV: got %d, want 401", drec.Code)
	}
}

func TestMCPDeactivatedUserIsRefused(t *testing.T) {
	w := newMCPWorld(t)
	_, tok := seedToken(t, w.st, "usr_alice", store.ScopeMCPWriteAny)
	u, err := w.st.Users().GetUserByID(context.Background(), "usr_alice")
	if err != nil {
		t.Fatal(err)
	}
	u.Status = "inactive"
	if err := w.st.Users().UpdateUser(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	if got := rawMCP(t, w.srv, map[string]string{"Authorization": "Bearer " + tok}).Code; got != http.StatusUnauthorized {
		t.Errorf("deactivated: got %d, want 401", got)
	}
}

func TestMCPEndToEnd(t *testing.T) {
	w := newMCPWorld(t)
	_, tok := seedToken(t, w.st, "usr_alice", store.ScopeMCPWriteOwn)
	cs := agent(t, w.srv, tok)

	if cs.InitializeResult().Instructions == "" {
		t.Error("initialize carries no instructions")
	}
	if got := cs.InitializeResult().ServerInfo; got.Name != "kycalendar" || got.Version != appversion.String() {
		t.Errorf("server info %+v, want kycalendar %s", got, appversion.String())
	}
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	if want := []string{"create_event", "list_calendars", "list_events", "update_event"}; !slices.Equal(names, want) {
		t.Fatalf("tools %v, want %v (and nothing that deletes)", names, want)
	}

	var cals struct {
		Calendars []struct{ ID string } `json:"calendars"`
	}
	if msg := callTool(t, cs, "list_calendars", map[string]any{}, &cals); msg != "" {
		t.Fatal(msg)
	}
	if len(cals.Calendars) != 1 || cals.Calendars[0].ID != w.personal {
		t.Fatalf("calendars %+v, want only the personal one", cals.Calendars)
	}

	var made written
	if msg := callTool(t, cs, "create_event", createArgs(w.personal, "Dentist"), &made); msg != "" {
		t.Fatal(msg)
	}
	ev := findEvent(listEvents(t, cs, nil), made.UID)
	if ev == nil || ev.Title != "Dentist" || !ev.Editable || ev.ETag != made.ETag {
		t.Fatalf("created event %+v", ev)
	}

	update := map[string]any{"calendar_id": w.personal, "uid": made.UID, "etag": made.ETag, "title": "Dentist (moved)",
		"start": "2026-10-21T09:00:00Z", "end": "2026-10-21T10:00:00Z", "zone": "UTC"}
	var upd written
	if msg := callTool(t, cs, "update_event", update, &upd); msg != "" {
		t.Fatal(msg)
	}
	if msg := callTool(t, cs, "update_event", update, nil); !strings.Contains(msg, "changed elsewhere") {
		t.Errorf("stale etag: got %q, want refused", msg)
	}
	update["etag"] = "*"
	if msg := callTool(t, cs, "update_event", update, nil); msg == "" {
		t.Error(`etag "*" accepted`)
	}
	update["etag"], update["title"] = upd.ETag, "  "
	if msg := callTool(t, cs, "update_event", update, nil); !strings.Contains(msg, "blank") {
		t.Errorf("blank title: got %q, want refused", msg)
	}
	if msg := callTool(t, cs, "create_event", createArgs(w.personal, ""), nil); !strings.Contains(msg, "blank") {
		t.Errorf("blank title on create: got %q, want refused", msg)
	}
	ev = findEvent(listEvents(t, cs, nil), made.UID)
	if ev == nil || ev.Title != "Dentist (moved)" || ev.Start != "2026-10-21T09:00:00Z" {
		t.Fatalf("after update %+v", ev)
	}
}

// Events list by start whatever order they were stored in, a series' occurrences interleaved
// with other events, all-day events first on their day and equal starts by UID; the web list
// keeps the same order.
func TestEventsListInChronologicalOrder(t *testing.T) {
	w := newMCPWorld(t)
	_, tok := seedToken(t, w.st, "usr_alice", store.ScopeMCPWriteAny)
	cs := agent(t, w.srv, tok)
	at := func(title, start, end string) map[string]any {
		args := createArgs(w.personal, title)
		args["start"], args["end"] = start, end
		return args
	}
	weekly := at("Standup", "2026-10-05T09:00:00Z", "2026-10-05T09:15:00Z")
	weekly["repeat"] = map[string]any{"freq": "weekly"}
	allDay := at("Holiday", "2026-10-12", "2026-10-13")
	allDay["all_day"] = true
	uids := map[string]string{}
	for _, args := range []map[string]any{
		at("Late", "2026-10-25T15:00:00Z", "2026-10-25T16:00:00Z"),
		at("Late too", "2026-10-25T15:00:00Z", "2026-10-25T15:30:00Z"),
		weekly,
		at("Midnight", "2026-10-12T00:00:00Z", "2026-10-12T00:30:00Z"),
		allDay,
		at("Early", "2026-10-02T08:00:00Z", "2026-10-02T09:00:00Z"),
	} {
		var made written
		if msg := callTool(t, cs, "create_event", args, &made); msg != "" {
			t.Fatalf("create %v: %s", args["title"], msg)
		}
		uids[args["title"].(string)] = made.UID
	}
	tie := []string{"Late", "Late too"}
	if uids["Late"] > uids["Late too"] {
		tie = []string{"Late too", "Late"}
	}
	want := slices.Concat([]string{"Early", "Standup", "Holiday", "Midnight", "Standup", "Standup"}, tie, []string{"Standup"})

	var got []string
	for _, e := range listEvents(t, cs, nil) {
		got = append(got, e.Title)
	}
	if !slices.Equal(got, want) {
		t.Errorf("list_events order %v, want %v", got, want)
	}

	rec := doJSON(t, w.srv, "GET", "/api/events?start=2026-10-01T00:00:00Z&end=2026-11-01T00:00:00Z", w.alice, nil)
	var web []mcpEvent
	if err := json.Unmarshal(rec.Body.Bytes(), &web); rec.Code != http.StatusOK || err != nil {
		t.Fatalf("GET /api/events: %d %s", rec.Code, rec.Body)
	}
	got = got[:0]
	for _, e := range web {
		got = append(got, e.Title)
	}
	if !slices.Equal(got, want) {
		t.Errorf("GET /api/events order %v, want %v", got, want)
	}

	// In New York, Midnight UTC is the evening before, so it moves ahead of the holiday.
	var ny struct {
		Events []mcpEvent `json:"events"`
	}
	if msg := callTool(t, cs, "list_events", map[string]any{"start": "2026-10-11T00:00:00-04:00", "end": "2026-10-13T00:00:00-04:00", "tz": "America/New_York"}, &ny); msg != "" {
		t.Fatal(msg)
	}
	got = got[:0]
	for _, e := range ny.Events {
		got = append(got, e.Title)
	}
	if want := []string{"Midnight", "Holiday", "Standup"}; !slices.Equal(got, want) {
		t.Errorf("list_events in New York %v, want %v", got, want)
	}
}

func TestMCPReadOnlyToken(t *testing.T) {
	w := newMCPWorld(t)
	_, tok := seedToken(t, w.st, "usr_alice", store.ScopeMCPRead)
	cs := agent(t, w.srv, tok)
	if msg := callTool(t, cs, "create_event", createArgs(w.personal, "x"), nil); msg != api.MCPReadOnlyForTest {
		t.Errorf("create with a read token: got %q", msg)
	}
	uid := webEvent(t, w, "Web event")
	ev := findEvent(listEvents(t, cs, nil), uid)
	if ev == nil || ev.Editable {
		t.Fatalf("read token sees %+v, want it listed and not editable", ev)
	}
	args := map[string]any{"calendar_id": w.personal, "uid": uid, "etag": ev.ETag, "title": "Hijacked"}
	if msg := callTool(t, cs, "update_event", args, nil); msg != api.MCPReadOnlyForTest {
		t.Errorf("update with a read token: got %q", msg)
	}
}

// webEvent creates an event in alice's personal calendar through the web API; it returns its UID.
func webEvent(t *testing.T, w *mcpWorld, title string) string {
	t.Helper()
	rec := doJSON(t, w.srv, "POST", "/api/calendars/"+w.personal+"/events", w.alice,
		map[string]any{"title": title, "start": "2026-10-22T09:00:00Z", "end": "2026-10-22T10:00:00Z", "zone": "UTC"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("web create: %d %s", rec.Code, rec.Body)
	}
	var out written
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out.UID
}

func TestMCPWriteOwnOnlyChangesItsOwnEvents(t *testing.T) {
	w := newMCPWorld(t)
	id, tok := seedToken(t, w.st, "usr_alice", store.ScopeMCPWriteOwn)
	_, other := seedToken(t, w.st, "usr_alice", store.ScopeMCPWriteAny)
	cs := agent(t, w.srv, tok)

	webUID := webEvent(t, w, "Web event")
	var theirs written
	if msg := callTool(t, agent(t, w.srv, other), "create_event", createArgs(w.personal, "Other agent"), &theirs); msg != "" {
		t.Fatal(msg)
	}
	var mine written
	if msg := callTool(t, cs, "create_event", createArgs(w.personal, "Mine"), &mine); msg != "" {
		t.Fatal(msg)
	}
	// The user edits the agent's event on their phone: it stays the agent's.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/events/"+w.personal+"/"+mine.UID, bytes.NewReader([]byte(`{"title":"Mine, edited","start":"2026-10-20T14:00:00Z","end":"2026-10-20T15:00:00Z","zone":"UTC"}`)))
	req.Header.Set("If-Match", `"`+mine.ETag+`"`)
	addSession(req, w.alice)
	w.srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("web edit: %d %s", rec.Code, rec.Body)
	}

	events := listEvents(t, cs, nil)
	for uid, want := range map[string]bool{webUID: false, theirs.UID: false, mine.UID: true} {
		ev := findEvent(events, uid)
		if ev == nil || ev.Editable != want {
			t.Fatalf("event %s: %+v, want editable %v", uid, ev, want)
		}
	}
	tryUpdate := func(uid string) string {
		ev := findEvent(listEvents(t, cs, nil), uid)
		return callTool(t, cs, "update_event", map[string]any{"calendar_id": w.personal, "uid": uid, "etag": ev.ETag, "title": "Changed by agent"}, nil)
	}
	for _, uid := range []string{webUID, theirs.UID} {
		if msg := tryUpdate(uid); msg != api.MCPNotOwnForTest {
			t.Errorf("update of %s: got %q, want %q", uid, msg, api.MCPNotOwnForTest)
		}
	}
	if msg := tryUpdate(mine.UID); msg != "" {
		t.Errorf("update of its own event after a web edit: %s", msg)
	}

	// The setting widens and narrows it again.
	setScope := func(scope string) {
		if rec := doJSON(t, w.srv, "PATCH", "/api/app-passwords/"+id, w.alice, map[string]string{"scope": scope}); rec.Code != http.StatusOK {
			t.Fatalf("PATCH %s: %d %s", scope, rec.Code, rec.Body)
		}
	}
	setScope(store.ScopeMCPWriteAny)
	if msg := tryUpdate(webUID); msg != "" {
		t.Errorf("write-any update: %s", msg)
	}
	setScope(store.ScopeMCPWriteOwn)
	if msg := tryUpdate(webUID); msg != api.MCPNotOwnForTest {
		t.Errorf("after narrowing: got %q", msg)
	}
}

func addSession(req *http.Request, cookie *http.Cookie) {
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: "test-csrf"})
	req.Header.Set(auth.HeaderCSRF, "test-csrf")
}

func TestMCPNeverReachesGroupCalendars(t *testing.T) {
	w := newMCPWorld(t)
	_, tok := seedToken(t, w.st, "usr_alice", store.ScopeMCPWriteAny)
	cs := agent(t, w.srv, tok)
	// alice is an editor of the group calendar, and it has an event.
	if rec := doJSON(t, w.srv, "POST", "/api/calendars/"+w.group.ID+"/events", w.alice,
		map[string]any{"title": "Room booking", "start": "2026-10-22T09:00:00Z", "end": "2026-10-22T10:00:00Z", "zone": "UTC"}); rec.Code != http.StatusCreated {
		t.Fatalf("group event: %d", rec.Code)
	}
	for _, ev := range listEvents(t, cs, nil) {
		if ev.CalendarID == w.group.ID {
			t.Fatalf("group event listed: %+v", ev)
		}
	}
	if msg := callTool(t, cs, "list_events", map[string]any{"start": "2026-10-01T00:00:00Z", "end": "2026-11-01T00:00:00Z", "calendar_ids": []string{w.group.ID}}, nil); msg != "No such calendar" {
		t.Errorf("list group calendar: got %q", msg)
	}
	if msg := callTool(t, cs, "create_event", createArgs(w.group.ID, "Booked"), nil); msg != "No such calendar" {
		t.Errorf("create in group calendar: got %q", msg)
	}
	if msg := callTool(t, cs, "update_event", map[string]any{"calendar_id": w.group.ID, "uid": "x", "etag": "e", "title": "t"}, nil); msg != "No such calendar" {
		t.Errorf("update in group calendar: got %q", msg)
	}
}

// A title-only change keeps the rule and every one-off change of a series.
func TestMCPTextEditKeepsTheSeries(t *testing.T) {
	w := newMCPWorld(t)
	_, tok := seedToken(t, w.st, "usr_alice", store.ScopeMCPWriteOwn)
	cs := agent(t, w.srv, tok)
	args := createArgs(w.personal, "Standup")
	args["start"], args["end"] = "2026-10-05T09:00:00Z", "2026-10-05T09:15:00Z"
	args["repeat"] = map[string]any{"freq": "weekly"}
	var made written
	if msg := callTool(t, cs, "create_event", args, &made); msg != "" {
		t.Fatal(msg)
	}
	instances := func() []mcpEvent {
		var out []mcpEvent
		for _, ev := range listEvents(t, cs, nil) {
			if ev.UID == made.UID {
				out = append(out, ev)
			}
		}
		return out
	}
	series := instances()
	if len(series) != 4 {
		t.Fatalf("weekly in October: %d instances", len(series))
	}
	// Move one occurrence.
	second := series[1]
	var moved written
	if msg := callTool(t, cs, "update_event", map[string]any{"calendar_id": w.personal, "uid": made.UID, "etag": second.ETag, "scope": "this",
		"recurrence_id": second.RecurrenceID, "start": "2026-10-12T11:00:00Z", "end": "2026-10-12T11:15:00Z", "zone": "UTC"}, &moved); msg != "" {
		t.Fatal(msg)
	}
	if msg := callTool(t, cs, "update_event", map[string]any{"calendar_id": w.personal, "uid": made.UID, "etag": moved.ETag, "scope": "this"}, nil); !strings.Contains(msg, "recurrence_id") {
		t.Errorf("scope this without recurrence_id: got %q", msg)
	}
	// Rename the series without times or repeat.
	if msg := callTool(t, cs, "update_event", map[string]any{"calendar_id": w.personal, "uid": made.UID, "etag": moved.ETag, "title": "Daily sync"}, nil); msg != "" {
		t.Fatal(msg)
	}
	after := instances()
	if len(after) != 4 {
		t.Fatalf("after renaming: %d instances, want the rule kept", len(after))
	}
	overrides := 0
	for _, ev := range after {
		if ev.Override {
			overrides++
			if ev.Start != "2026-10-12T11:00:00Z" {
				t.Errorf("override moved back: %+v", ev)
			}
		} else if ev.Title != "Daily sync" {
			t.Errorf("occurrence not renamed: %+v", ev)
		}
	}
	if overrides != 1 {
		t.Errorf("%d overrides after renaming, want 1", overrides)
	}
}

func TestMCPTruncatesLongDescriptions(t *testing.T) {
	w := newMCPWorld(t)
	_, tok := seedToken(t, w.st, "usr_alice", store.ScopeMCPWriteOwn)
	cs := agent(t, w.srv, tok)
	args := createArgs(w.personal, "Long")
	args["description"] = strings.Repeat("é", 3000) // 6000 bytes, two per rune
	var made written
	if msg := callTool(t, cs, "create_event", args, &made); msg != "" {
		t.Fatal(msg)
	}
	ev := findEvent(listEvents(t, cs, nil), made.UID)
	if ev == nil || !ev.DescriptionTruncated || len(ev.Description) > 2048 || !strings.HasPrefix(strings.Repeat("é", 3000), ev.Description) {
		t.Fatalf("description %d bytes, truncated %v", len(ev.Description), ev != nil && ev.DescriptionTruncated)
	}
}

func TestMCPIdleTokensAreRevoked(t *testing.T) {
	w := newMCPWorld(t)
	ctx := context.Background()
	apps := w.st.AppPasswords()
	day := 24 * time.Hour

	recentID, recent := seedToken(t, w.st, "usr_alice", store.ScopeMCPRead)
	idleID, idle := seedToken(t, w.st, "usr_alice", store.ScopeMCPRead)
	if err := apps.TouchLastUsed(ctx, recentID, time.Now().Add(-89*day)); err != nil {
		t.Fatal(err)
	}
	if err := apps.TouchLastUsed(ctx, idleID, time.Now().Add(-91*day)); err != nil {
		t.Fatal(err)
	}
	if got := rawMCP(t, w.srv, map[string]string{"Authorization": "Bearer " + recent}).Code; got != http.StatusOK {
		t.Errorf("89 days idle: got %d, want 200", got)
	}
	// Use resets the clock.
	if p, err := apps.Get(ctx, recentID); err != nil || p.LastUsedAt == nil || time.Since(*p.LastUsedAt) > time.Minute {
		t.Errorf("last used not reset: %+v %v", p, err)
	}
	// Refused and gone with no sweep run.
	if got := rawMCP(t, w.srv, map[string]string{"Authorization": "Bearer " + idle}).Code; got != http.StatusUnauthorized {
		t.Errorf("91 days idle: got %d, want 401", got)
	}
	if _, err := apps.Get(ctx, idleID); err != store.ErrNotFound {
		t.Errorf("idle token still stored: %v", err)
	}

	// The sweep: never-used tokens count from creation; dav tokens are never swept.
	neverID, _ := seedToken(t, w.st, "usr_alice", store.ScopeMCPWriteOwn)
	davID, _ := seedToken(t, w.st, "usr_alice", store.ScopeDAV)
	if err := api.ExpireIdleMCPTokens(ctx, w.st, time.Now().Add(89*day)); err != nil {
		t.Fatal(err)
	}
	if _, err := apps.Get(ctx, neverID); err != nil {
		t.Errorf("never-used token swept before 90 days: %v", err)
	}
	if err := api.ExpireIdleMCPTokens(ctx, w.st, time.Now().Add(91*day)); err != nil {
		t.Fatal(err)
	}
	if _, err := apps.Get(ctx, neverID); err != store.ErrNotFound {
		t.Errorf("never-used token kept past 90 days: %v", err)
	}
	if _, err := apps.Get(ctx, davID); err != nil {
		t.Errorf("dav token swept: %v", err)
	}
	records, _, err := w.st.Audit().ListAuditRecords(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	expired := 0
	for _, r := range records {
		if r.Action == "app_password.expired" {
			expired++
		}
	}
	// The idle token at sign-in, then the never-used one and the recent one (idle by day 91).
	if expired != 3 {
		t.Errorf("%d app_password.expired records, want 3", expired)
	}
}

func TestAppPasswordScopes(t *testing.T) {
	w := newMCPWorld(t)
	for name, body := range map[string]map[string]string{
		"no scope":      {"label": "x"},
		"unknown scope": {"label": "x", "scope": "admin"},
	} {
		if rec := doJSON(t, w.srv, "POST", "/api/app-passwords", w.alice, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", name, rec.Code)
		}
	}
	rec := doJSON(t, w.srv, "POST", "/api/app-passwords", w.alice, map[string]string{"label": "Hermes", "scope": store.ScopeMCPRead})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var made struct {
		ID          string     `json:"id"`
		Scope       string     `json:"scope"`
		RevokeAfter *time.Time `json:"revoke_after"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &made)
	if made.Scope != store.ScopeMCPRead || made.RevokeAfter == nil || made.RevokeAfter.Before(time.Now().Add(89*24*time.Hour)) {
		t.Errorf("created %+v", made)
	}
	davRec := doJSON(t, w.srv, "POST", "/api/app-passwords", w.alice, map[string]string{"label": "Phone", "scope": store.ScopeDAV})
	var phone struct {
		ID          string     `json:"id"`
		RevokeAfter *time.Time `json:"revoke_after"`
	}
	_ = json.Unmarshal(davRec.Body.Bytes(), &phone)
	if phone.RevokeAfter != nil {
		t.Error("a dav token has an idle deadline")
	}

	if rec := doJSON(t, w.srv, "PATCH", "/api/app-passwords/"+phone.ID, w.alice, map[string]string{"scope": store.ScopeMCPWriteAny}); rec.Code != http.StatusBadRequest {
		t.Errorf("dav to MCP: got %d, want 400", rec.Code)
	}
	if rec := doJSON(t, w.srv, "PATCH", "/api/app-passwords/"+made.ID, w.alice, map[string]string{"scope": store.ScopeDAV}); rec.Code != http.StatusBadRequest {
		t.Errorf("MCP to dav: got %d, want 400", rec.Code)
	}
	bob := loginAs(t, w.srv, w.st, "bob", "user")
	if rec := doJSON(t, w.srv, "PATCH", "/api/app-passwords/"+made.ID, bob, map[string]string{"scope": store.ScopeMCPWriteAny}); rec.Code != http.StatusNotFound {
		t.Errorf("another user's token: got %d, want 404", rec.Code)
	}
	if rec := doJSON(t, w.srv, "PATCH", "/api/app-passwords/"+made.ID, w.alice, map[string]string{"scope": store.ScopeMCPWriteAny}); rec.Code != http.StatusOK {
		t.Fatalf("widen: %d %s", rec.Code, rec.Body)
	}
	if p, err := w.st.AppPasswords().Get(context.Background(), made.ID); err != nil || p.Scope != store.ScopeMCPWriteAny {
		t.Errorf("stored scope %+v %v", p, err)
	}
	records, _, err := w.st.Audit().ListAuditRecords(context.Background(), 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(records, func(r *store.AuditRecord) bool {
		return r.Action == "app_password.scope_change" && r.Details == "from=mcp:read to=mcp:write-any"
	}) {
		t.Error("scope change not audited")
	}
}

func TestAgentSkill(t *testing.T) {
	w := newMCPWorld(t)
	rec := httptest.NewRecorder()
	w.srv.ServeHTTP(rec, httptest.NewRequest("GET", "/agent/skills/kycalendar/SKILL.md", nil))
	body, _ := io.ReadAll(rec.Body)
	if rec.Code != http.StatusOK || !bytes.Equal(body, agentskill.For(appversion.String())) || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/markdown") {
		t.Fatalf("served %d %q, want the embedded skill as markdown", rec.Code, rec.Header().Get("Content-Type"))
	}
	if n := bytes.Count(agentskill.KyCalendar, []byte(agentskill.SourceVersion)); n != 1 {
		t.Fatalf("committed skill has %d %q lines, want 1: the served copy would not carry the build's version", n, agentskill.SourceVersion)
	}
	if got := string(agentskill.For("1.2.3")); !strings.Contains(got, "\nversion: \"1.2.3\"\n") || strings.Contains(got, agentskill.SourceVersion) {
		t.Errorf("For(1.2.3) does not carry 1.2.3 as its version")
	}
	skill := string(body)

	front, _, ok := strings.Cut(strings.TrimPrefix(skill, "---\n"), "\n---\n")
	if !ok || !strings.HasPrefix(skill, "---\n") {
		t.Fatal("no frontmatter")
	}
	var keys []string
	for _, line := range strings.Split(front, "\n") {
		key, _, _ := strings.Cut(line, ":")
		keys = append(keys, key)
	}
	if want := []string{"name", "description", "version"}; !slices.Equal(keys, want) {
		t.Errorf("frontmatter keys %v, want exactly %v", keys, want)
	}
	if !strings.Contains(front, `version: "`+appversion.String()+`"`) {
		t.Errorf("served skill version is not the app version %s", appversion.String())
	}

	_, tok := seedToken(t, w.st, "usr_alice", store.ScopeMCPRead)
	tools, err := agent(t, w.srv, tok).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var registered []string
	for _, tool := range tools.Tools {
		registered = append(registered, tool.Name)
		if !strings.Contains(skill, "`"+tool.Name+"`") {
			t.Errorf("skill does not name tool %s", tool.Name)
		}
	}
	// Every tool-like name the skill mentions (a verb, an underscore, a noun) must exist.
	for _, m := range regexp.MustCompile("`((?:list|get|create|update|delete|move|cancel|find|search)_[a-z_]+)`").FindAllStringSubmatch(skill, -1) {
		if !slices.Contains(registered, m[1]) {
			t.Errorf("skill names %s, which is not a tool", m[1])
		}
	}
	for _, msg := range []string{api.MCPReadOnlyForTest, api.MCPNotOwnForTest, api.MCPSubscribedForTest, api.MCPMeetingForTest, "No such calendar", "No such event", "The event changed elsewhere; reload it", "Calendar storage is full"} {
		if !strings.Contains(skill, msg) {
			t.Errorf("skill error table lacks %q", msg)
		}
	}
}

// wrongSecret changes the token's last character, so it never equals the original.
func wrongSecret(tok string) string {
	last := byte('a')
	if tok[len(tok)-1] == 'a' {
		last = 'b'
	}
	return tok[:len(tok)-1] + string(last)
}
