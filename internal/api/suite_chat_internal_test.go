package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/Busnes-app/ky-primitives/suitedir"
	"github.com/Busnes-app/ky-primitives/syncauth"

	"github.com/Busnes-app/kycalendar/internal/kymessages"
	"github.com/Busnes-app/kycalendar/internal/store"
)

type chatRig struct {
	s     *Server
	ann   *store.User
	mu    sync.Mutex
	asked []kymessages.ChatRequest
	reply string // body KyMessages answers; status 200 unless it carries a code
	code  int
}

// newChatRig is KyCalendar paired with a KyMessages console that checks every signature and
// event type; the organisation is every @example.com address (orgPeople).
func newChatRig(t *testing.T) *chatRig {
	t.Helper()
	s, _ := davInternalServer(t)
	p := newSuiteIdP(t).provider()
	s.signin.Store(p)
	r := &chatRig{s: s, ann: ssoUser("usr_ann", "sub-ann", p), reply: `{"url":"https://chat.example/#/user/@bob:example"}`, code: 200}
	r.ann.Email = "ann@example.com"
	km := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		h := syncauth.FromRequest(req)
		if _, err := syncauth.Verify(meetSecret, h, b, syncauth.Options{}); err != nil || h.EventType != kymessages.EventChat || req.Method != http.MethodPost || req.URL.Path != kymessages.ChatPath {
			t.Errorf("%s %s event %q: %v", req.Method, req.URL.Path, h.EventType, err)
			http.Error(w, `{"code":"bad_signature"}`, http.StatusUnauthorized)
			return
		}
		var c kymessages.ChatRequest
		_ = json.Unmarshal(b, &c)
		r.mu.Lock()
		r.asked = append(r.asked, c)
		code, reply := r.code, r.reply
		r.mu.Unlock()
		w.WriteHeader(code)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(km.Close)
	s.kymessages.HTTP = km.Client()
	s.kymessages.Set(&kymessages.Peer{URL: km.URL, Secret: meetSecret, Source: kymessages.SourceEnvironment})
	old := resolvePeople
	resolvePeople = func(_ context.Context, _ *suitedir.Client, sub string, emails []string) ([]person, error) {
		if sub != "sub-ann" {
			t.Errorf("resolved as %q", sub)
		}
		return orgPeople(emails), nil
	}
	t.Cleanup(func() { resolvePeople = old })
	return r
}

func (r *chatRig) chat(u *store.User, body string) (int, map[string]string) {
	w := httptest.NewRecorder()
	r.s.handleSuiteChat(w, withSessionUser(httptest.NewRequest(http.MethodPost, "/api/suite/chat", strings.NewReader(body)), u, nil))
	var out map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func (r *chatRig) calls() []kymessages.ChatRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]kymessages.ChatRequest(nil), r.asked...)
}

func TestChatAsksKyMessagesForTheTargetsLink(t *testing.T) {
	r := newChatRig(t)
	code, out := r.chat(r.ann, `{"email":"Bob@example.com"}`)
	if code != 200 || out["url"] != "https://chat.example/#/user/@bob:example" {
		t.Fatalf("%d %v", code, out)
	}
	c := r.calls()
	if len(c) != 1 || c[0].Sub != "sub-ann" || c[0].Issuer != issuerURL(r.ann) || !strings.HasPrefix(c[0].Issuer, "https://") || c[0].TargetSub != "sub-Bob" {
		t.Fatalf("asked %+v", c)
	}
	recs, _, _ := r.s.store.Audit().ListAuditRecords(context.Background(), 0, 10)
	if len(recs) != 1 || recs[0].Action != "chat.open" || recs[0].UserID != r.ann.ID || strings.Contains(recs[0].Details+recs[0].Resource, "bob") {
		t.Fatalf("audit %+v", recs)
	}
}

func TestChatRefusals(t *testing.T) {
	r := newChatRig(t)
	for _, c := range []struct {
		name, body string
		status     int
		code       string
	}{
		{"no body", `{}`, 400, "bad_body"},
		{"not an address", `{"email":"bob"}`, 400, "bad_body"},
		{"oversize", `{"email":"` + strings.Repeat("a", 2000) + `@example.com"}`, 400, "bad_body"},
		{"outside the organisation", `{"email":"eve@elsewhere.test"}`, 404, "no_target"},
		{"the caller", `{"email":"ann@example.com"}`, 404, "no_target"},
	} {
		if status, out := r.chat(r.ann, c.body); status != c.status || out["code"] != c.code {
			t.Errorf("%s: %d %v", c.name, status, out)
		}
	}
	if n := len(r.calls()); n != 0 {
		t.Fatalf("%d refusals reached KyMessages", n)
	}
	// What KyMessages refuses or answers badly never reaches the browser as a link.
	for _, c := range []struct {
		code      int
		reply     string
		status    int
		wantCode  string
		wantNoURL bool
	}{
		{404, `{"code":"no_target"}`, 404, "no_target", true},
		{404, `{"code":"no_user"}`, 404, "no_user", true},
		{404, `{"code":"kycalendar_off"}`, 503, "unavailable", true},
		{503, `{"code":"unavailable"}`, 503, "unavailable", true},
		{200, `{"url":"https://evil.example/login"}`, 502, "refused", true},
		{200, `{"url":"javascript:alert(1)"}`, 502, "refused", true},
	} {
		r.mu.Lock()
		r.code, r.reply = c.code, c.reply
		r.mu.Unlock()
		if status, out := r.chat(r.ann, `{"email":"bob@example.com"}`); status != c.status || out["code"] != c.wantCode || out["url"] != "" {
			t.Errorf("%d %s: %d %v", c.code, c.reply, status, out)
		}
	}
}

func TestChatNeedsKyMessagesAndAKyIdentityAccount(t *testing.T) {
	r := newChatRig(t)
	local := &store.User{ID: "usr_loc", Username: "loc", Role: "user", Status: "active", SSOProvider: "local", Email: "loc@example.com"}
	admin := ssoUser("usr_adm", "sub-adm", r.s.signin.Load())
	admin.Role = "admin"
	for _, u := range []*store.User{local, admin} {
		if status, out := r.chat(u, `{"email":"bob@example.com"}`); status != 404 || out["code"] != "chat_off" {
			t.Errorf("%s: %d %v", u.ID, status, out)
		}
	}
	r.s.kymessages.Set(nil) // unpaired
	if status, out := r.chat(r.ann, `{"email":"bob@example.com"}`); status != 404 || out["code"] != "chat_off" {
		t.Fatalf("unpaired: %d %v", status, out)
	}
	if n := len(r.calls()); n != 0 {
		t.Fatalf("%d calls reached KyMessages", n)
	}
}

func TestChatIsRateLimitedPerUser(t *testing.T) {
	r := newChatRig(t)
	limited := 0
	for i := 0; i < 25; i++ {
		if status, _ := r.chat(r.ann, `{"email":"bob@example.com"}`); status == http.StatusTooManyRequests {
			limited++
		}
	}
	if limited != 5 || len(r.calls()) != 20 {
		t.Fatalf("limited %d, calls %d", limited, len(r.calls()))
	}
	bob := ssoUser("usr_bob", "sub-ann", r.s.signin.Load()) // another account keeps its own budget
	if status, _ := r.chat(bob, `{"email":"cy@example.com"}`); status != 200 {
		t.Fatalf("another user limited: %d", status)
	}
}

func peopleByEmail(t *testing.T, s *Server, u *store.User, query string) []person {
	t.Helper()
	w := httptest.NewRecorder()
	s.handleSuitePeople(w, withSessionUser(httptest.NewRequest(http.MethodGet, "/api/suite/people?"+query, nil), u, nil))
	var body struct{ People []person }
	if json.Unmarshal(w.Body.Bytes(), &body) != nil || w.Code != 200 || body.People == nil {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	return body.People
}

func TestSuitePeopleResolvesAddresses(t *testing.T) {
	r := newChatRig(t)
	n := 0
	inner := resolvePeople
	resolvePeople = func(ctx context.Context, d *suitedir.Client, sub string, emails []string) ([]person, error) {
		n++
		return inner(ctx, d, sub, emails)
	}
	q := url.Values{"email": {"bob@example.com", "eve@elsewhere.test"}}.Encode()
	if got := peopleByEmail(t, r.s, r.ann, q); len(got) != 1 || got[0].Email != "bob@example.com" {
		t.Fatalf("%v", got)
	}
	peopleByEmail(t, r.s, r.ann, q) // cached
	many := url.Values{}
	for i := 0; i < 101; i++ {
		many.Add("email", "p"+strings.Repeat("x", i%3)+"@example.com")
	}
	local := &store.User{ID: "usr_loc", Username: "loc", Role: "user", Status: "active", SSOProvider: "local"}
	for _, c := range []struct {
		u *store.User
		q string
	}{{r.ann, many.Encode()}, {r.ann, "email=bob"}, {r.ann, "email="}, {r.ann, q + "&q=bo"}, {local, q}} {
		if got := peopleByEmail(t, r.s, c.u, c.q); len(got) != 0 {
			t.Errorf("%s %.40s: %v", c.u.ID, c.q, got)
		}
	}
	if n != 1 {
		t.Fatalf("KyIdentity asked %d times", n)
	}
}
