package api_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/feed"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// secret stands for the private part of a published link; it must never leave the server.
const secret = "Zx9secretPublishToken"

type subCalendar struct {
	ID           string `json:"id"`
	Role         string `json:"role"`
	DAVPath      string `json:"dav_path"`
	Subscription *struct {
		Host        string  `json:"host"`
		RefreshedAt *string `json:"refreshed_at"`
		Error       string  `json:"error"`
	} `json:"subscription"`
}

// feedServer serves the M365 fixture at /cal.ics and 404 elsewhere, and makes srv's feed
// client trust it and reach its loopback address.
func feedServer(t *testing.T, srv *api.Server) *httptest.Server {
	t.Helper()
	body, err := os.ReadFile("../feed/testdata/m365.ics")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cal.ics" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(ts.Close)
	pool := x509.NewCertPool()
	pool.AddCert(ts.Certificate())
	api.SetFeedClientForTest(srv, feed.NewClient(func(netip.Addr) error { return nil }, pool))
	return ts
}

// captureLog collects the server log for the rest of the test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &buf
}

func TestSubscriptionLifecycle(t *testing.T) {
	logs := captureLog(t)
	w := newMCPWorld(t)
	ts := feedServer(t, w.srv)
	link := strings.Replace(ts.URL, "https://", "webcal://", 1) + "/cal.ics?token=" + secret

	rec := doJSON(t, w.srv, "POST", "/api/subscriptions", w.alice, map[string]string{"url": link, "name": "Work", "color": "#3366cc"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("subscribe: %d %s", rec.Code, rec.Body.String())
	}
	var cal subCalendar
	if err := json.Unmarshal(rec.Body.Bytes(), &cal); err != nil || cal.Subscription == nil || cal.Subscription.Host != "127.0.0.1" || cal.Role != "owner" {
		t.Fatalf("created %s", rec.Body.String())
	}
	list := call(t, w.srv, "GET", "/api/calendars", "", w.alice)
	if !strings.Contains(list.Body.String(), `"subscription"`) {
		t.Fatalf("list lacks the subscription: %s", list.Body.String())
	}

	// The events are there and read-only.
	evs := eventsIn(t, w.srv, w.alice, cal.ID)
	if len(evs) == 0 {
		t.Fatal("no events mirrored")
	}
	var standup eventJSON
	for _, e := range evs {
		if e.Editable {
			t.Errorf("%s is editable", e.Title)
		}
		if e.Title == "Weekly standup" {
			standup = e
		}
	}
	if standup.UID == "" {
		t.Fatalf("no standup among %+v", evs)
	}

	// Every write path refuses: web, CalDAV and the agent.
	if r := call(t, w.srv, "POST", "/api/calendars/"+cal.ID+"/events", `{"title":"x","start":"2026-10-20T10:00:00Z","end":"2026-10-20T11:00:00Z"}`, w.alice); r.Code != http.StatusForbidden {
		t.Errorf("web create: %d", r.Code)
	}
	path := "/api/events/" + cal.ID + "/" + standup.UID
	if r := withIfMatch(t, w.srv, "PUT", path+"?scope=all", `{"title":"hijacked","start":"2026-10-19T09:00:00+02:00","end":"2026-10-19T09:15:00+02:00"}`, standup.ETag, w.alice); r.Code != http.StatusForbidden {
		t.Errorf("web update: %d %s", r.Code, r.Body.String())
	}
	if r := withIfMatch(t, w.srv, "DELETE", path, "", standup.ETag, w.alice); r.Code != http.StatusForbidden {
		t.Errorf("web delete: %d", r.Code)
	}

	dav := httptest.NewServer(w.srv)
	defer dav.Close()
	_, davTok := seedToken(t, w.st, "usr_alice", store.ScopeDAV)
	objs, err := w.st.Calendars().ListObjects(context.Background(), cal.ID)
	if err != nil || len(objs) == 0 {
		t.Fatal(err)
	}
	if r := rawDAV(t, dav, "PUT", cal.DAVPath+"new.ics", "alice", davTok, evA, map[string]string{"Content-Type": "text/calendar"}); r.StatusCode != http.StatusForbidden {
		t.Errorf("CalDAV PUT: %d %s", r.StatusCode, readAll(r))
	}
	if r := rawDAV(t, dav, "DELETE", cal.DAVPath+objs[0].Name, "alice", davTok, "", nil); r.StatusCode != http.StatusForbidden {
		t.Errorf("CalDAV DELETE: %d", r.StatusCode)
	}
	privs := `<?xml version="1.0"?><d:propfind xmlns:d="DAV:"><d:prop><d:current-user-privilege-set/></d:prop></d:propfind>`
	r := rawDAV(t, dav, "PROPFIND", cal.DAVPath, "alice", davTok, privs, map[string]string{"Depth": "0", "Content-Type": "application/xml"})
	if body := readAll(r); r.StatusCode != http.StatusMultiStatus || !strings.Contains(body, "read") || strings.Contains(body, "write") {
		t.Errorf("CalDAV privileges: %d %s", r.StatusCode, body)
	}

	_, tok := seedToken(t, w.st, "usr_alice", store.ScopeMCPWriteAny)
	cs := agent(t, w.srv, tok)
	var cals struct {
		Calendars []struct {
			ID       string `json:"id"`
			ReadOnly bool   `json:"read_only"`
		} `json:"calendars"`
	}
	callTool(t, cs, "list_calendars", map[string]any{}, &cals)
	readOnly := map[string]bool{}
	for _, c := range cals.Calendars {
		readOnly[c.ID] = c.ReadOnly
	}
	if !readOnly[cal.ID] || readOnly[w.personal] {
		t.Errorf("read_only %v: want only %s", readOnly, cal.ID)
	}
	var listed struct {
		Events []eventJSON `json:"events"`
	}
	callTool(t, cs, "list_events", map[string]any{"start": "2026-10-01T00:00:00Z", "end": "2026-11-01T00:00:00Z", "calendar_ids": []string{cal.ID}}, &listed)
	for _, e := range listed.Events {
		if e.Editable {
			t.Errorf("agent sees %s as editable", e.Title)
		}
	}
	if msg := callTool(t, cs, "create_event", map[string]any{"calendar_id": cal.ID, "title": "x", "start": "2026-10-20T10:00:00Z", "end": "2026-10-20T11:00:00Z"}, nil); msg != api.MCPSubscribedForTest {
		t.Errorf("agent create: %q", msg)
	}
	if msg := callTool(t, cs, "update_event", map[string]any{"calendar_id": cal.ID, "uid": standup.UID, "etag": standup.ETag, "title": "x"}, nil); msg != api.MCPSubscribedForTest {
		t.Errorf("agent update: %q", msg)
	}

	// Refresh now is spaced a minute apart; it was just fetched.
	if r := call(t, w.srv, "POST", "/api/calendars/"+cal.ID+"/refresh", "", w.alice); r.Code != http.StatusTooManyRequests {
		t.Errorf("refresh right after subscribing: %d", r.Code)
	}
	// A new link must load before it replaces the old one.
	if r := doJSON(t, w.srv, "PATCH", "/api/subscriptions/"+cal.ID, w.alice, map[string]string{"url": ts.URL + "/unpublished.ics"}); r.Code != http.StatusUnprocessableEntity || !strings.Contains(r.Body.String(), "no longer works") {
		t.Errorf("patch to a dead link: %d %s", r.Code, r.Body.String())
	}
	if after := eventsIn(t, w.srv, w.alice, cal.ID); len(after) != len(evs) {
		t.Errorf("a refused link changed the calendar: %d -> %d events", len(evs), len(after))
	}
	if r := doJSON(t, w.srv, "PATCH", "/api/subscriptions/"+cal.ID, w.alice, map[string]string{"url": ts.URL + "/cal.ics?token=new" + secret}); r.Code != http.StatusOK {
		t.Errorf("patch: %d %s", r.Code, r.Body.String())
	}
	// Subscription routes answer 404 for a calendar that is not a subscription.
	if r := call(t, w.srv, "POST", "/api/calendars/"+w.personal+"/refresh", "", w.alice); r.Code != http.StatusNotFound {
		t.Errorf("refresh on a plain calendar: %d", r.Code)
	}

	// The owner may still rename and delete it.
	if r := call(t, w.srv, "PATCH", "/api/calendars/"+cal.ID, `{"name":"Contoso"}`, w.alice); r.Code != http.StatusOK {
		t.Errorf("rename: %d", r.Code)
	}

	// The link's secret is in no response, audit row or log line.
	audit, _, err := w.st.Audit().ListAuditRecords(context.Background(), 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	auditJSON, _ := json.Marshal(audit)
	for where, text := range map[string]string{"create": rec.Body.String(), "list": call(t, w.srv, "GET", "/api/calendars", "", w.alice).Body.String(), "audit": string(auditJSON), "log": logs.String()} {
		if strings.Contains(text, secret) {
			t.Errorf("the link's secret leaked into the %s", where)
		}
	}
	if !strings.Contains(string(auditJSON), "subscription.create") || !strings.Contains(string(auditJSON), "subscription.url_change") {
		t.Errorf("audit lacks the subscription actions: %s", auditJSON)
	}

	if r := call(t, w.srv, "DELETE", "/api/calendars/"+cal.ID, "", w.alice); r.Code != http.StatusNoContent {
		t.Errorf("delete: %d", r.Code)
	}
	if _, err := w.st.Calendars().GetSubscription(context.Background(), cal.ID); err == nil {
		t.Error("the subscription outlived its calendar")
	}
}

func TestSubscriptionRefusesLocalAddresses(t *testing.T) {
	w := newMCPWorld(t)
	ts := feedServer(t, w.srv)
	// The production policy: the loopback listener is refused at dial time.
	api.SetFeedClientForTest(w.srv, feed.NewClient(feed.RefusePrivate, nil))
	r := doJSON(t, w.srv, "POST", "/api/subscriptions", w.alice, map[string]string{"url": ts.URL + "/cal.ics", "name": "Work"})
	if r.Code != http.StatusUnprocessableEntity || !strings.Contains(r.Body.String(), "private or local") {
		t.Fatalf("loopback link: %d %s", r.Code, r.Body.String())
	}
	for _, bad := range []string{"http://example.com/cal.ics", "file:///etc/passwd", "not a url"} {
		if r := doJSON(t, w.srv, "POST", "/api/subscriptions", w.alice, map[string]string{"url": bad, "name": "Work"}); r.Code != http.StatusBadRequest {
			t.Errorf("%q: %d", bad, r.Code)
		}
	}
	cals, _ := w.st.Calendars().ListCalendarsByOwner(context.Background(), "user", "usr_alice")
	if len(cals) != 1 {
		t.Fatalf("refused links left %d calendars", len(cals))
	}
	// Each attempt is a fetch by the server, so attempts are limited per user.
	var last int
	for range 12 {
		last = doJSON(t, w.srv, "POST", "/api/subscriptions", w.alice, map[string]string{"url": ts.URL + "/cal.ics", "name": "Work"}).Code
	}
	if last != http.StatusTooManyRequests {
		t.Errorf("attempt 13: %d, want 429", last)
	}
}
