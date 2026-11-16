package api

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

	"github.com/Busnes-app/ky-primitives/suitedir"
	"github.com/Busnes-app/ky-primitives/syncauth"

	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/kymessages"
	"github.com/Busnes-app/kycalendar/internal/store"
)

type presenceRig struct {
	s     *Server
	ann   *store.User
	cal   *store.Calendar
	clock time.Time
	mu    sync.Mutex
	got   []presenceCall
	reply func(path string) (int, string)
}

type presenceCall struct {
	Path string
	Body []byte
}

// newPresenceRig is KyCalendar paired with a KyMessages that checks each signature and event
// type and records the signed bodies; ann is a member with a default calendar.
func newPresenceRig(t *testing.T) *presenceRig {
	t.Helper()
	s, _ := davInternalServer(t)
	p := newSuiteIdP(t).provider()
	s.signin.Store(p)
	r := &presenceRig{s: s, clock: time.Now().UTC().Truncate(time.Minute)}
	events := map[string]string{"calendar": kymessages.EventBusy, "me": kymessages.EventMe, "lookup": kymessages.EventLookup}
	km := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		h := syncauth.FromRequest(req)
		want := events[strings.TrimPrefix(req.URL.Path, kymessages.PresencePath)]
		if _, err := syncauth.Verify(meetSecret, h, b, syncauth.Options{}); err != nil || want == "" || h.EventType != want {
			t.Errorf("%s event %q: %v", req.URL.Path, h.EventType, err)
			http.Error(w, `{"code":"bad_signature"}`, http.StatusUnauthorized)
			return
		}
		r.mu.Lock()
		r.got = append(r.got, presenceCall{req.URL.Path, b})
		reply := r.reply
		r.mu.Unlock()
		status, body := 200, `{"outcome":"applied"}`
		if reply != nil {
			status, body = reply(req.URL.Path)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(km.Close)
	s.kymessages.HTTP = km.Client()
	s.kymessages.Set(&kymessages.Peer{URL: km.URL, Secret: meetSecret, Source: kymessages.SourceEnvironment})
	ctx := context.Background()
	r.ann = ssoUser("usr_ann", "sub-ann", p)
	r.ann.Email = "ann@example.com"
	if err := s.store.Users().CreateUser(ctx, r.ann); err != nil {
		t.Fatal(err)
	}
	if err := davbackend.EnsureDefault(ctx, s.store, r.ann.ID, 0); err != nil {
		t.Fatal(err)
	}
	cals, _ := s.store.Calendars().ListCalendarsByOwner(ctx, "user", r.ann.ID)
	r.cal = cals[0]
	old, oldResolve := meetingNow, resolvePeople
	meetingNow = func() time.Time { return r.clock }
	resolvePeople = func(_ context.Context, _ *suitedir.Client, _ string, emails []string) ([]person, error) {
		return orgPeople(emails), nil
	}
	t.Cleanup(func() { meetingNow, resolvePeople = old, oldResolve })
	return r
}

func (r *presenceRig) calls() []presenceCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]presenceCall(nil), r.got...)
}

// put stores one VEVENT from from to to (offsets from the clock) with extra lines.
func (r *presenceRig) put(t *testing.T, uid string, from, to time.Duration, extra ...string) {
	t.Helper()
	f := func(d time.Duration) string { return r.clock.Add(d).Format("20060102T150405Z") }
	ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:" + uid + "\r\nDTSTAMP:20261001T000000Z\r\nDTSTART:" + f(from) +
		"\r\nDTEND:" + f(to) + "\r\nSUMMARY:Board: layoffs\r\nLOCATION:Room 7\r\n" + strings.Join(extra, "\r\n") + "\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	ics = strings.ReplaceAll(ics, "\r\n\r\n", "\r\n")
	o := &store.CalendarObject{CalendarID: r.cal.ID, Name: uid + ".ics", Component: "VEVENT", UID: uid, Data: []byte(ics), FirstStart: r.clock.Add(from).Unix()}
	if _, err := r.s.store.Calendars().PutObject(context.Background(), o, "", false, store.OwnerLimits{}); err != nil {
		t.Fatal(err)
	}
}

func TestPresencePushSendsAcceptedBusyTimesOnly(t *testing.T) {
	r := newPresenceRig(t)
	org, att := "ORGANIZER:mailto:ann@example.com", "ATTENDEE;PARTSTAT=ACCEPTED:mailto:bob@example.com"
	r.put(t, "own-meeting", time.Hour, 2*time.Hour, org, att)
	r.put(t, "accepted", 3*time.Hour, 4*time.Hour, "ORGANIZER:mailto:bob@example.com", "ATTENDEE;PARTSTAT=ACCEPTED:mailto:ann@example.com")
	r.put(t, "declined", 5*time.Hour, 6*time.Hour, "ORGANIZER:mailto:bob@example.com", "ATTENDEE;PARTSTAT=DECLINED:mailto:ann@example.com")
	r.put(t, "unanswered", 5*time.Hour, 6*time.Hour, "ORGANIZER:mailto:bob@example.com", "ATTENDEE:mailto:ann@example.com")
	r.put(t, "free", 7*time.Hour, 8*time.Hour, "TRANSP:TRANSPARENT")
	r.put(t, "tentative", 7*time.Hour, 8*time.Hour, "STATUS:TENTATIVE")
	r.put(t, "cancelled", 7*time.Hour, 8*time.Hour, org, att, "STATUS:CANCELLED")
	r.put(t, "ooo", 9*time.Hour, 10*time.Hour, "X-KYCALENDAR-OUT-OF-OFFICE:TRUE")
	r.put(t, "later", 72*time.Hour, 73*time.Hour)

	r.s.SyncPresence(context.Background())
	calls := r.calls()
	if len(calls) != 1 || calls[0].Path != kymessages.PresencePath+"calendar" {
		t.Fatalf("calls %+v", calls)
	}
	body := calls[0].Body
	for _, leak := range []string{"Board", "layoffs", "Room", "bob", "own-meeting", "accepted", "ann@"} {
		if bytes.Contains(body, []byte(leak)) {
			t.Errorf("the signed body carries %q: %s", leak, body)
		}
	}
	var got struct {
		Sub, Issuer string
		Revision    int64
		Busy        []kymessages.Block
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&got); err != nil || got.Sub != "sub-ann" || got.Issuer == "" || got.Revision != r.clock.UnixMilli() {
		t.Fatalf("body %s: %v", body, err)
	}
	f := func(d time.Duration) string { return r.clock.Add(d).Format(time.RFC3339) }
	want := []kymessages.Block{{Start: f(time.Hour), End: f(2 * time.Hour)}, {Start: f(3 * time.Hour), End: f(4 * time.Hour)}}
	if fmt.Sprint(got.Busy) != fmt.Sprint(want) {
		t.Fatalf("busy %v, want %v", got.Busy, want)
	}

	// Done until something changes or the refresh comes round.
	r.s.SyncPresence(context.Background())
	if n := len(r.calls()); n != 1 {
		t.Fatalf("pushed again without a change: %d", n)
	}
	r.put(t, "new", 11*time.Hour, 12*time.Hour)
	r.clock = time.Now().Add(time.Second) // the write stamps the real clock
	r.s.SyncPresence(context.Background())
	if n := len(r.calls()); n != 2 {
		t.Fatalf("a change was not pushed: %d", n)
	}
	r.clock = r.clock.Add(store.PresenceRefresh + time.Minute)
	r.s.SyncPresence(context.Background())
	if n := len(r.calls()); n != 3 {
		t.Fatalf("no refresh: %d", n)
	}
}

func TestPresencePushRetriesAndStopsWhenUnpaired(t *testing.T) {
	r := newPresenceRig(t)
	r.reply = func(string) (int, string) { return 503, `{"code":"unavailable"}` }
	r.s.SyncPresence(context.Background())
	r.s.SyncPresence(context.Background()) // backing off
	if n := len(r.calls()); n != 1 {
		t.Fatalf("calls during backoff: %d", n)
	}
	r.reply = nil
	r.clock = r.clock.Add(2 * time.Hour)
	r.s.SyncPresence(context.Background())
	if n := len(r.calls()); n != 2 {
		t.Fatalf("no retry: %d", n)
	}
	r.s.kymessages.Set(nil)
	r.put(t, "x", time.Hour, 2*time.Hour)
	r.clock = time.Now().Add(time.Hour)
	r.s.SyncPresence(context.Background())
	if n := len(r.calls()); n != 2 {
		t.Fatalf("pushed while unpaired: %d", n)
	}
}

func (r *presenceRig) do(t *testing.T, u *store.User, method, path, body string, h http.HandlerFunc) (int, string) {
	t.Helper()
	w := httptest.NewRecorder()
	h(w, withSessionUser(httptest.NewRequest(method, path, strings.NewReader(body)), u, nil))
	return w.Code, w.Body.String()
}

func TestPresenceLookupIsBatchedAndCached(t *testing.T) {
	r := newPresenceRig(t)
	until := r.clock.Add(time.Hour).Format(time.RFC3339)
	r.reply = func(string) (int, string) {
		return 200, `{"people":[{"sub":"sub-bob","state":"in_a_meeting","until":"` + until + `"},{"sub":"sub-cy","state":"unknown"}]}`
	}
	code, body := r.do(t, r.ann, http.MethodPost, "/api/presence", `{"emails":["bob@example.com","cy@example.com","eve@outside.test"]}`, r.s.handlePresenceLookup)
	if code != 200 || body != `{"people":[{"email":"bob@example.com","state":"in_a_meeting","until":"`+until+`"},{"email":"cy@example.com","state":"unknown"}]}`+"\n" {
		t.Fatalf("%d %s", code, body)
	}
	var asked struct{ Subs []string }
	_ = json.Unmarshal(r.calls()[0].Body, &asked)
	if fmt.Sprint(asked.Subs) != "[sub-bob sub-cy]" { // never the outsider
		t.Fatalf("asked %v", asked.Subs)
	}
	r.do(t, r.ann, http.MethodPost, "/api/presence", `{"emails":["bob@example.com"]}`, r.s.handlePresenceLookup)
	if n := len(r.calls()); n != 1 {
		t.Fatalf("not cached: %d calls", n)
	}
	r.s.presence.mu.Lock()
	for k, e := range r.s.presence.m {
		e.at = e.at.Add(-presenceCacheTTL)
		r.s.presence.m[k] = e
	}
	r.s.presence.mu.Unlock()
	r.do(t, r.ann, http.MethodPost, "/api/presence", `{"emails":["bob@example.com"]}`, r.s.handlePresenceLookup)
	if n := len(r.calls()); n != 2 {
		t.Fatalf("kept past a minute: %d calls", n)
	}

	// The cache is per caller: KyMessages refuses dee (no KyMessages account), and ann's
	// cached answer for bob never reaches her.
	dee := ssoUser("usr_dee", "sub-dee", r.s.signin.Load())
	dee.Email = "dee@example.com"
	if err := r.s.store.Users().CreateUser(context.Background(), dee); err != nil {
		t.Fatal(err)
	}
	r.do(t, r.ann, http.MethodPost, "/api/presence", `{"emails":["bob@example.com"]}`, r.s.handlePresenceLookup)
	calls := len(r.calls())
	r.reply = func(string) (int, string) { return 404, `{"error":"x","code":"no_user"}` }
	if code, body := r.do(t, dee, http.MethodPost, "/api/presence", `{"emails":["bob@example.com"]}`, r.s.handlePresenceLookup); code != 404 || !strings.Contains(body, "no_user") {
		t.Fatalf("another caller's cache served dee: %d %s", code, body)
	}
	if n := len(r.calls()); n != calls+1 {
		t.Fatalf("dee was not asked about: %d calls", n)
	}

	many := make([]string, 51)
	for i := range many {
		many[i] = fmt.Sprintf(`"p%d@example.com"`, i)
	}
	if code, body := r.do(t, r.ann, http.MethodPost, "/api/presence", `{"emails":[`+strings.Join(many, ",")+`]}`, r.s.handlePresenceLookup); code != 400 || !strings.Contains(body, "too_many") {
		t.Fatalf("over the cap: %d %s", code, body)
	}
	outsider := &store.User{ID: "usr_local", Role: "user", Status: "active"}
	if code, _ := r.do(t, outsider, http.MethodPost, "/api/presence", `{"emails":["bob@example.com"]}`, r.s.handlePresenceLookup); code != 409 {
		t.Fatalf("a member without KyIdentity: %d", code)
	}
}

func TestPresenceSettingsGoToKyMessages(t *testing.T) {
	r := newPresenceRig(t)
	r.reply = func(string) (int, string) { return 200, `{"share":false,"manual":null}` }
	if code, body := r.do(t, r.ann, http.MethodPut, "/api/presence/me", `{"share":false}`, r.s.handlePresenceMe); code != 200 || !strings.Contains(body, `"share":false`) {
		t.Fatalf("%d %s", code, body)
	}
	if b := string(r.calls()[0].Body); b != `{"issuer":"`+issuerURL(r.ann)+`","share":false,"sub":"sub-ann"}` {
		t.Fatalf("sent %s", b)
	}
	for _, bad := range []string{`{"manual":{"state":"busy"}}`, `{"manual":{"state":"in_a_meeting","until":"` + time.Now().Add(time.Hour).UTC().Format(time.RFC3339) + `"}}`,
		`{"manual":{"state":"busy","until":"` + time.Now().Add(8*24*time.Hour).UTC().Format(time.RFC3339) + `"}}`, `{"note":"x"}`} {
		if code, _ := r.do(t, r.ann, http.MethodPut, "/api/presence/me", bad, r.s.handlePresenceMe); code != 400 {
			t.Errorf("%s: %d", bad, code)
		}
	}
	r.reply = func(string) (int, string) { return 409, `{"error":"x","code":"sharing_off"}` }
	until := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	if code, body := r.do(t, r.ann, http.MethodPut, "/api/presence/me", `{"manual":{"state":"away","until":"`+until+`"}}`, r.s.handlePresenceMe); code != 409 || !strings.Contains(body, "sharing_off") {
		t.Fatalf("sharing off: %d %s", code, body)
	}
}
