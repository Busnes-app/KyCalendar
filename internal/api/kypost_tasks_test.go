package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Busnes-app/ky-primitives/syncauth"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/kypost"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// fakeKyPostTasks records the task states KyCalendar reports.
type fakeKyPostTasks struct {
	mu     sync.Mutex
	states []kypost.TaskStateRequest
}

func (f *fakeKyPostTasks) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	h := syncauth.FromRequest(r)
	if _, err := syncauth.Verify(kypostSecret, h, body, syncauth.Options{}); err != nil || h.EventType != kypost.EventTaskState || r.URL.Path != kypost.TaskStatePath {
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}
	var req kypost.TaskStateRequest
	_ = json.Unmarshal(body, &req)
	f.mu.Lock()
	f.states = append(f.states, req)
	f.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (f *fakeKyPostTasks) take() []kypost.TaskStateRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.states
	f.states = nil
	return out
}

// flagWorld: KyPost linked by environment (the link, and so flags, exist only with scheduling), alice an SSO
// user with a session.
func flagWorld(t *testing.T) (*api.Server, store.Store, *fakeKyPostTasks, string, *http.Cookie) {
	t.Helper()
	t.Setenv("KYCALENDAR_SCHEDULING", "true")
	t.Setenv("KYCALENDAR_KYPOST_URL", "https://kypost.example")
	secretFile := filepath.Join(t.TempDir(), "kypost.secret")
	if err := os.WriteFile(secretFile, append(kypostSecret, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KYCALENDAR_KYPOST_SECRET_FILE", secretFile)
	srv, st, _ := setupTestServer(t)
	fake := &fakeKyPostTasks{}
	kp := httptest.NewTLSServer(fake)
	t.Cleanup(kp.Close)
	link := api.KyPostLinkForTest(srv)
	link.HTTP = kp.Client()
	link.Set(&kypost.Peer{URL: kp.URL, Secret: kypostSecret, Source: kypost.SourceEnvironment})
	ssoUser(t, st, "alice")
	u, err := st.Users().GetUserByID(t.Context(), "usr_alice")
	if err != nil {
		t.Fatal(err)
	}
	return srv, st, fake, kp.URL, sessionFor(t, st, u)
}

func flag(ref, base string, flagged bool, due string) kypost.TaskFlagRequest {
	return kypost.TaskFlagRequest{User: kypost.User{Sub: "sub-alice", Issuer: "https://id.example"}, Ref: ref,
		Subject: "Quarterly report\r\nBcc: evil@example.net", URL: base + "/#/m/" + ref, Due: due, Flagged: flagged}
}

func TestKyPostFlagMakesATask(t *testing.T) {
	srv, st, fake, base, alice := flagWorld(t)
	post := func(id string, v any) *httptest.ResponseRecorder {
		return signedPost(t, srv, "/api/tasks/flag", kypost.EventTaskFlag, id, v)
	}
	// Control characters in the subject: refused, nothing made.
	if w := post("e0", flag("m1", base, true, "2026-11-20")); w.Code != http.StatusBadRequest {
		t.Fatalf("subject with CRLF: %d", w.Code)
	}
	ok := flag("m1", base, true, "2026-11-20")
	ok.Subject = "Quarterly report"
	if w := post("e1", ok); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"created"`) {
		t.Fatalf("flag: %d %s", w.Code, w.Body)
	}
	if w := post("e1", ok); !strings.Contains(w.Body.String(), "duplicate") {
		t.Fatalf("replay: %d %s", w.Code, w.Body)
	}
	got := listTasks(t, srv, alice, "source=kypost")
	if len(got) != 1 || got[0].Title != "Quarterly report" || got[0].Due != "2026-11-20" {
		t.Fatalf("flagged tasks %+v", got)
	}
	// The task carries the link and the reference, never the Message-ID or the body.
	o, _ := st.Calendars().GetObjectByUID(t.Context(), got[0].CalendarID, got[0].UID)
	if s := string(o.Data); !strings.Contains(s, "X-KY-REF:m1") || !strings.Contains(s, "X-KY-SOURCE:kypost") || !strings.Contains(s, "URL:"+base) {
		t.Fatalf("object %s", s)
	}
	// Flag again with another due date: updated, not duplicated.
	ok.Due = "2026-11-23"
	if w := post("e2", ok); !strings.Contains(w.Body.String(), `"updated"`) {
		t.Fatalf("reflag: %s", w.Body)
	}
	// Unflag in KyPost completes it, and KyCalendar does not echo it back.
	ok.Flagged = false
	if w := post("e3", ok); !strings.Contains(w.Body.String(), `"completed"`) {
		t.Fatalf("unflag: %s", w.Body)
	}
	if s := fake.take(); len(s) != 0 {
		t.Fatalf("echoed KyPost's own change: %+v", s)
	}
	if w := post("e4", ok); !strings.Contains(w.Body.String(), `"unchanged"`) {
		t.Fatalf("second unflag: %s", w.Body)
	}
	// Reopening in KyCalendar sets the flag again; completing clears it; deleting clears it.
	done := listTasks(t, srv, alice, "status=completed")
	if w := taskReq(t, srv, "PATCH", "/api/tasks/"+done[0].CalendarID+"/"+done[0].UID, done[0].ETag, alice, map[string]any{"status": "NEEDS-ACTION"}); w.Code != http.StatusOK {
		t.Fatalf("reopen %d", w.Code)
	}
	if s := fake.take(); len(s) != 1 || s[0].Ref != "m1" || s[0].Done || s[0].Sub != "sub-alice" {
		t.Fatalf("reopen state %+v", s)
	}
	open := listTasks(t, srv, alice, "source=kypost")
	if w := taskReq(t, srv, "DELETE", "/api/tasks/"+open[0].CalendarID+"/"+open[0].UID, open[0].ETag, alice, ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete %d", w.Code)
	}
	if s := fake.take(); len(s) != 1 || !s[0].Done {
		t.Fatalf("delete state %+v", s)
	}
}

func TestKyPostFlagRefusals(t *testing.T) {
	srv, st, _, base, alice := flagWorld(t)
	post := func(id string, v any) *httptest.ResponseRecorder {
		return signedPost(t, srv, "/api/tasks/flag", kypost.EventTaskFlag, id, v)
	}
	good := flag("m2", base, true, "")
	good.Subject = "Hello"
	// Wrong event type (a reflected state message) and a bad signature.
	if w := signedPost(t, srv, "/api/tasks/flag", kypost.EventTaskState, "x1", good); w.Code != http.StatusUnauthorized {
		t.Errorf("wrong event type: %d", w.Code)
	}
	r := httptest.NewRequest("POST", "/api/tasks/flag", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("unsigned: %d", w.Code)
	}
	// Unknown user, a local user and an administrator are all no_user.
	for name, who := range map[string]kypost.User{"unknown": {Sub: "nobody", Issuer: "https://id.example"}, "wrong issuer": {Sub: "sub-alice", Issuer: "https://evil.example"}} {
		b := good
		b.User = who
		if w := post("u-"+name, b); w.Code != http.StatusNotFound {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	if err := st.Users().CreateUser(t.Context(), &store.User{ID: "usr_root", Username: "root", Role: "admin", Status: "active", SSOProvider: "kysignon", SSOSubject: "sub-root", SSOIssuer: "kyidentity https://id.example"}); err != nil {
		t.Fatal(err)
	}
	adm := good
	adm.User = kypost.User{Sub: "sub-root", Issuer: "https://id.example"}
	if w := post("u-admin", adm); w.Code != http.StatusNotFound {
		t.Errorf("admin: %d", w.Code)
	}
	// Bad input: a link elsewhere, a bad reference, a bad date, oversize body.
	for name, mut := range map[string]func(*kypost.TaskFlagRequest){
		"other host": func(b *kypost.TaskFlagRequest) { b.URL = "https://evil.example/#/m/m2" },
		"http link":  func(b *kypost.TaskFlagRequest) { b.URL = strings.Replace(b.URL, "https:", "http:", 1) },
		"bad ref":    func(b *kypost.TaskFlagRequest) { b.Ref = "<script>" },
		"bad due":    func(b *kypost.TaskFlagRequest) { b.Due = "tomorrow" },
	} {
		b := good
		mut(&b)
		if w := post("b-"+name, b); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	big := good
	big.Subject = strings.Repeat("x", 3<<20)
	if w := post("b-big", big); w.Code == http.StatusOK {
		t.Errorf("oversize accepted")
	}
	// The user's switch, on KyCalendar's side, defaults on and turns the route off.
	var prefs struct {
		KyPostFlags  bool `json:"kypost_flags"`
		KyPostLinked bool `json:"kypost_linked"`
	}
	_ = json.Unmarshal(taskReq(t, srv, "GET", "/api/tasks/prefs", "", alice, "").Body.Bytes(), &prefs)
	if !prefs.KyPostFlags || !prefs.KyPostLinked {
		t.Fatalf("prefs %+v", prefs)
	}
	if w := taskReq(t, srv, "PUT", "/api/tasks/prefs", "", alice, map[string]any{"kypost_flags": false}); w.Code != http.StatusOK {
		t.Fatalf("switch off %d", w.Code)
	}
	if w := post("s-off", good); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "tasks_off") {
		t.Errorf("switch off: %d %s", w.Code, w.Body)
	}
	_ = taskReq(t, srv, "PUT", "/api/tasks/prefs", "", alice, map[string]any{"kypost_flags": true})
	// Rate limit: 120 an hour.
	var last int
	for i := range 121 {
		b := good
		b.Ref = "r" + strings.Repeat("a", i%5+1)
		last = post("rate-"+string(rune('a'+i%26))+strings.Repeat("z", i/26), b).Code
	}
	if last != http.StatusTooManyRequests {
		t.Errorf("121st flag: %d", last)
	}
	// Unpaired: the route is absent.
	api.KyPostLinkForTest(srv).Set(nil)
	if w := post("unpaired", good); w.Code != http.StatusNotFound {
		t.Errorf("unpaired: %d", w.Code)
	}
}
