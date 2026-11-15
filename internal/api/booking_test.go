package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/auth"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/kypost"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// bookingWorld is a server with scheduling on and KyPost linked (sends recorded by fake), booking
// pages allowed, and bob's booking type: 60 minutes, any hour of any day, no notice.
type bookingWorld struct {
	srv   *api.Server
	st    store.Store
	fake  *fakeKyPost
	bob   *http.Cookie
	cal   string
	typ   string
	slug  string
	admin *http.Cookie
}

func newBookingWorld(t *testing.T) *bookingWorld {
	t.Helper()
	t.Setenv("KYCALENDAR_SCHEDULING", "true")
	t.Setenv("KYCALENDAR_KYPOST_URL", "https://kypost.example")
	secretFile := filepath.Join(t.TempDir(), "kypost.secret")
	if err := os.WriteFile(secretFile, append(kypostSecret, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KYCALENDAR_KYPOST_SECRET_FILE", secretFile)
	srv, st, _ := setupTestServer(t)
	fake := &fakeKyPost{}
	kp := httptest.NewTLSServer(fake)
	t.Cleanup(kp.Close)
	api.SetKyPostClientForTest(srv, &kypost.Client{BaseURL: kp.URL, Secret: kypostSecret, HTTP: kp.Client()})
	ssoUser(t, st, "bob")
	ctx := context.Background()
	bob, err := st.Users().GetUserByID(ctx, "usr_bob")
	if err != nil {
		t.Fatal(err)
	}
	w := &bookingWorld{srv: srv, st: st, fake: fake, bob: sessionFor(t, st, bob), admin: loginAs(t, srv, st, "root", "admin")}
	if err := davbackend.EnsureDefault(ctx, st, "usr_bob", 0); err != nil {
		t.Fatal(err)
	}
	cals, _ := st.Calendars().ListCalendarsByOwner(ctx, "user", "usr_bob")
	w.cal = cals[0].ID
	if r := call(t, srv, "PUT", "/api/admin/booking", `{"allowed":true}`, w.admin); r.Code != 200 {
		t.Fatalf("allow booking pages: %d %s", r.Code, r.Body.String())
	}
	r := call(t, srv, "POST", "/api/booking-types", w.typeBody(true), w.bob)
	if r.Code != 201 {
		t.Fatalf("create type: %d %s", r.Code, r.Body.String())
	}
	var v struct{ ID, Slug, URL string }
	_ = json.Unmarshal(r.Body.Bytes(), &v)
	w.typ, w.slug = v.ID, v.Slug
	if len(v.Slug) != 32 || !strings.HasSuffix(v.URL, "/book/"+v.Slug) {
		t.Fatalf("slug %q url %q", v.Slug, v.URL)
	}
	return w
}

func (w *bookingWorld) typeBody(enabled bool) string {
	return fmt.Sprintf(`{"enabled":%t,"title":"Intro call","duration_min":60,"zone":"UTC","notice_min":0,"horizon_days":3,
"windows":[{"day":0,"start":"00:00","end":"24:00"},{"day":1,"start":"00:00","end":"24:00"},{"day":2,"start":"00:00","end":"24:00"},
{"day":3,"start":"00:00","end":"24:00"},{"day":4,"start":"00:00","end":"24:00"},{"day":5,"start":"00:00","end":"24:00"},{"day":6,"start":"00:00","end":"24:00"}],
"calendar_id":%q}`, enabled, w.cal)
}

// visitor is a browser with no account: its cookies and an address of its own.
type visitor struct {
	ip      string
	cookies []*http.Cookie
	csrf    string
}

var nextIP = 10

func newVisitor() *visitor {
	nextIP++
	return &visitor{ip: fmt.Sprintf("198.51.100.%d:4000", nextIP)}
}

func (v *visitor) do(t *testing.T, srv http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = v.ip
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, c := range v.cookies {
		req.AddCookie(c)
	}
	if v.csrf != "" {
		req.Header.Set(auth.HeaderCSRF, v.csrf)
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	for _, c := range w.Result().Cookies() {
		v.cookies = slices.DeleteFunc(slices.Clone(v.cookies), func(o *http.Cookie) bool { return o.Name == c.Name })
		v.cookies = append(v.cookies, c)
		if c.Name == auth.CSRFCookieName {
			v.csrf = c.Value
		}
	}
	return w
}

type pageView struct {
	Host, Title string
	Slots       []struct{ Start, End string }
}

func (v *visitor) page(t *testing.T, w *bookingWorld, slug string) pageView {
	t.Helper()
	r := v.do(t, w.srv, "GET", "/api/book/"+slug, "")
	if r.Code != 200 {
		t.Fatalf("GET page: %d %s", r.Code, r.Body.String())
	}
	var p pageView
	_ = json.Unmarshal(r.Body.Bytes(), &p)
	return p
}

func holdBody(start, email string) string {
	return fmt.Sprintf(`{"start":%q,"name":"Carol Visitor","email":%q,"note":"About the order, line 1\nline 2"}`, start, email)
}

var confirmLink = regexp.MustCompile(`/book/c#([0-9a-f-]{36})\.([0-9a-f]{64})`)
var manageLink = regexp.MustCompile(`/book/m#([0-9a-f-]{36})\.([0-9a-f]{64})`)

func unfold(ics string) string { return strings.ReplaceAll(ics, "\r\n ", "") }

func linkIn(t *testing.T, re *regexp.Regexp, ics string) (string, string) {
	t.Helper()
	m := re.FindStringSubmatch(unfold(ics))
	if m == nil {
		t.Fatalf("no link in\n%s", ics)
	}
	return m[1], m[2]
}

func linkJSON(id, token string) string { return fmt.Sprintf(`{"id":%q,"token":%q}`, id, token) }

// hold books start as carol and returns the confirm link mailed to her.
func (w *bookingWorld) hold(t *testing.T, v *visitor, start string) (string, string) {
	t.Helper()
	if r := v.do(t, w.srv, "POST", "/api/book/"+w.slug+"/hold", holdBody(start, "carol@example.net")); r.Code != 202 {
		t.Fatalf("hold: %d %s", r.Code, r.Body.String())
	}
	sent := w.fake.take()
	if len(sent) != 1 {
		t.Fatalf("hold mailed %d messages", len(sent))
	}
	return linkIn(t, confirmLink, sent[0].ICS)
}

func TestBookingFlowHoldConfirmInviteCancel(t *testing.T) {
	w := newBookingWorld(t)
	v := newVisitor()
	p := v.page(t, w, w.slug)
	if p.Host != "bob" || p.Title != "Intro call" || len(p.Slots) == 0 {
		t.Fatalf("page %+v", p)
	}
	start := p.Slots[1].Start
	// The hold is mailed from bob through KyPost; it carries the confirm link, nothing carol wrote.
	if r := v.do(t, w.srv, "POST", "/api/book/"+w.slug+"/hold", holdBody(start, "Carol@Example.net")); r.Code != 202 {
		t.Fatalf("hold: %d %s", r.Code, r.Body.String())
	}
	sent := w.fake.take()
	if len(sent) != 1 || sent[0].From != "bob@example.com" || sent[0].Sub != "sub-bob" || sent[0].Method != "REQUEST" ||
		len(sent[0].Recipients) != 1 || sent[0].Recipients[0] != "carol@example.net" {
		t.Fatalf("hold mail %+v", sent)
	}
	ics := unfold(sent[0].ICS)
	if strings.Contains(ics, "Carol") || strings.Contains(ics, "order") || !strings.Contains(ics, "STATUS:TENTATIVE") {
		t.Fatalf("hold mail carries visitor text or is not tentative:\n%s", ics)
	}
	id, token := linkIn(t, confirmLink, ics)
	// The held slot is gone from the page for everyone else.
	for _, s := range newVisitor().page(t, w, w.slug).Slots {
		if s.Start == start {
			t.Fatal("a held slot is still offered")
		}
	}
	// A scanner's GET of the link changes nothing (the fragment never reaches the server); the
	// page shows the hold and confirms only on a POST.
	if r := v.do(t, w.srv, "POST", "/api/book/confirm/view", linkJSON(id, token)); r.Code != 200 || !strings.Contains(r.Body.String(), `"status":"held"`) {
		t.Fatalf("confirm view: %d %s", r.Code, r.Body.String())
	}
	if r := v.do(t, w.srv, "POST", "/api/book/confirm", linkJSON(id, strings.Repeat("0", 64))); r.Code != 404 {
		t.Fatalf("wrong token: %d", r.Code)
	}
	if r := v.do(t, w.srv, "POST", "/api/book/confirm", linkJSON(id, token)); r.Code != 200 {
		t.Fatalf("confirm: %d %s", r.Code, r.Body.String())
	}
	if r := v.do(t, w.srv, "POST", "/api/book/confirm", linkJSON(id, token)); r.Code != 409 || codeOf(t, r.Body.Bytes()) != "already_confirmed" {
		t.Fatalf("second confirm: %d %s", r.Code, r.Body.String())
	}
	// bob's calendar holds an ordinary event, and the scheduler mails carol a normal invitation
	// with her name and note and the manage link.
	w.srv.DeliverScheduled(context.Background())
	sent = w.fake.take()
	if len(sent) != 1 || sent[0].Method != "REQUEST" || sent[0].Recipients[0] != "carol@example.net" {
		t.Fatalf("invitation %+v", sent)
	}
	ics = unfold(sent[0].ICS)
	if !strings.Contains(ics, `CN=Carol Visitor`) || !strings.Contains(ics, `About the order\, line 1\nline 2`) || !strings.Contains(ics, "SEQUENCE:1") {
		t.Fatalf("invitation:\n%s", ics)
	}
	mid, mtoken := linkIn(t, manageLink, ics)
	objs, _ := w.st.Calendars().ListObjects(context.Background(), w.cal)
	if len(objs) != 1 {
		t.Fatalf("bob's calendar holds %d objects", len(objs))
	}
	// The confirm token cannot manage the booking.
	if r := v.do(t, w.srv, "POST", "/api/book/manage", linkJSON(id, token)); r.Code != 404 {
		t.Fatalf("confirm token on manage: %d", r.Code)
	}
	// Reschedule to another free slot: the event moves and carol gets the update.
	r := v.do(t, w.srv, "POST", "/api/book/manage", linkJSON(mid, mtoken))
	var m struct {
		Status string
		Slots  []struct{ Start string }
	}
	_ = json.Unmarshal(r.Body.Bytes(), &m)
	if r.Code != 200 || m.Status != "confirmed" || len(m.Slots) == 0 {
		t.Fatalf("manage: %d %s", r.Code, r.Body.String())
	}
	to := m.Slots[len(m.Slots)-1].Start
	if r := v.do(t, w.srv, "POST", "/api/book/manage/reschedule", fmt.Sprintf(`{"id":%q,"token":%q,"start":%q}`, mid, mtoken, "2020-01-01T00:00:00Z")); r.Code != 409 {
		t.Fatalf("reschedule into the past: %d %s", r.Code, r.Body.String())
	}
	if r := v.do(t, w.srv, "POST", "/api/book/manage/reschedule", fmt.Sprintf(`{"id":%q,"token":%q,"start":%q}`, mid, mtoken, to)); r.Code != 200 {
		t.Fatalf("reschedule: %d %s", r.Code, r.Body.String())
	}
	w.srv.DeliverScheduled(context.Background())
	if sent = w.fake.take(); len(sent) != 1 || sent[0].Method != "REQUEST" {
		t.Fatalf("update %+v", sent)
	}
	// Cancel: the event goes and carol gets a CANCEL; the link then shows nothing.
	if r := v.do(t, w.srv, "POST", "/api/book/manage/cancel", linkJSON(mid, mtoken)); r.Code != 200 {
		t.Fatalf("cancel: %d %s", r.Code, r.Body.String())
	}
	w.srv.DeliverScheduled(context.Background())
	if sent = w.fake.take(); len(sent) != 1 || sent[0].Method != "CANCEL" {
		t.Fatalf("cancel mail %+v", sent)
	}
	if objs, _ = w.st.Calendars().ListObjects(context.Background(), w.cal); len(objs) != 0 {
		t.Fatal("the cancelled booking's event stayed")
	}
	if r := v.do(t, w.srv, "POST", "/api/book/manage", linkJSON(mid, mtoken)); r.Code != 404 {
		t.Fatalf("manage after cancel: %d", r.Code)
	}
}

func TestBookingMemberCancelShowsCancelled(t *testing.T) {
	w := newBookingWorld(t)
	v := newVisitor()
	id, token := w.hold(t, v, v.page(t, w, w.slug).Slots[0].Start)
	if r := v.do(t, w.srv, "POST", "/api/book/confirm", linkJSON(id, token)); r.Code != 200 {
		t.Fatalf("confirm: %d", r.Code)
	}
	w.srv.DeliverScheduled(context.Background())
	mid, mtoken := linkIn(t, manageLink, w.fake.take()[0].ICS)
	objs, _ := w.st.Calendars().ListObjects(context.Background(), w.cal)
	uid := strings.TrimSuffix(objs[0].Name, ".ics")
	req := httptest.NewRequest("DELETE", "/api/events/"+w.cal+"/"+uid+"?scope=all", nil)
	req.Header.Set("If-Match", `"`+objs[0].ETag+`"`)
	req.AddCookie(w.bob)
	addCSRF(req, w.bob)
	rec := httptest.NewRecorder()
	w.srv.ServeHTTP(rec, req)
	if rec.Code != 204 {
		t.Fatalf("member cancel %d %s", rec.Code, rec.Body.String())
	}
	if r := v.do(t, w.srv, "POST", "/api/book/manage", linkJSON(mid, mtoken)); r.Code != 200 || !strings.Contains(r.Body.String(), `"status":"cancelled"`) {
		t.Fatalf("manage after the member cancelled: %d %s", r.Code, r.Body.String())
	}
}

// Two visitors confirming at once, and two holding the same slot at once: exactly one wins.
func TestBookingRaces(t *testing.T) {
	w := newBookingWorld(t)
	v := newVisitor()
	slots := v.page(t, w, w.slug).Slots
	id, token := w.hold(t, v, slots[0].Start)
	codes := make(chan int, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			x := &visitor{ip: fmt.Sprintf("203.0.113.%d:1", i+1), cookies: v.cookies, csrf: v.csrf}
			codes <- x.do(t, w.srv, "POST", "/api/book/confirm", linkJSON(id, token)).Code
		}()
	}
	wg.Wait()
	close(codes)
	won := 0
	for c := range codes {
		if c == 200 {
			won++
		} else if c != 409 {
			t.Errorf("a racing confirm answered %d", c)
		}
	}
	if objs, _ := w.st.Calendars().ListObjects(context.Background(), w.cal); won != 1 || len(objs) != 1 {
		t.Fatalf("%d confirms won, %d events", won, len(objs))
	}

	start := slots[2].Start
	holds := make(chan int, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			x := newVisitorAt(fmt.Sprintf("192.0.2.%d:1", i+1))
			x.cookies, x.csrf = v.cookies, v.csrf
			holds <- x.do(t, w.srv, "POST", "/api/book/"+w.slug+"/hold", holdBody(start, fmt.Sprintf("racer%d@example.net", i))).Code
		}()
	}
	wg.Wait()
	close(holds)
	won = 0
	for c := range holds {
		if c == 202 {
			won++
		} else if c != 409 {
			t.Errorf("a racing hold answered %d", c)
		}
	}
	if won != 1 {
		t.Fatalf("%d holds of one slot won", won)
	}
}

func newVisitorAt(ip string) *visitor { return &visitor{ip: ip} }

// A confirmation re-checks free/busy: an event bob added after the hold takes the slot.
func TestBookingConfirmRechecksFreeBusy(t *testing.T) {
	w := newBookingWorld(t)
	v := newVisitor()
	slot := v.page(t, w, w.slug).Slots[0]
	id, token := w.hold(t, v, slot.Start)
	s, _ := time.Parse(time.RFC3339, slot.Start)
	body := fmt.Sprintf(`{"title":"Dentist","start":%q,"end":%q,"zone":"UTC"}`, s.Add(30*time.Minute).Format(time.RFC3339), s.Add(90*time.Minute).Format(time.RFC3339))
	if r := call(t, w.srv, "POST", "/api/calendars/"+w.cal+"/events", body, w.bob); r.Code != 201 {
		t.Fatalf("bob's event: %d %s", r.Code, r.Body.String())
	}
	if r := v.do(t, w.srv, "POST", "/api/book/confirm", linkJSON(id, token)); r.Code != 409 || codeOf(t, r.Body.Bytes()) != "slot_unavailable" {
		t.Fatalf("confirm over a new event: %d %s", r.Code, r.Body.String())
	}
}

func TestBookingHoldExpires(t *testing.T) {
	w := newBookingWorld(t)
	v := newVisitor()
	slot := v.page(t, w, w.slug).Slots[0]
	s, _ := time.Parse(time.RFC3339, slot.Start)
	token := crypto.RandomHex(32)
	now := time.Now()
	b := &store.Booking{ID: "11111111-1111-1111-1111-111111111111", TypeID: w.typ, UserID: "usr_bob", Email: "carol@example.net", EmailHash: "h",
		Name: "Carol", ConfirmHash: crypto.SHA256Hex([]byte(token)), UID: "expired-uid", Start: s.Unix(), End: s.Add(time.Hour).Unix(), ExpiresAt: now.Add(-time.Second).Unix()}
	if err := w.st.Bookings().Hold(context.Background(), b, now.Add(-time.Minute).Unix(), 0, 1<<40, 2, func([][]byte, []*store.Booking) error { return nil }); err != nil {
		t.Fatal(err)
	}
	// The expired hold no longer blocks the slot and cannot be confirmed.
	if got := v.page(t, w, w.slug).Slots[0].Start; got != slot.Start {
		t.Fatalf("an expired hold still blocks %s (first slot %s)", slot.Start, got)
	}
	if r := v.do(t, w.srv, "POST", "/api/book/confirm/view", linkJSON(b.ID, token)); !strings.Contains(r.Body.String(), `"status":"expired"`) {
		t.Fatalf("view of an expired hold: %d %s", r.Code, r.Body.String())
	}
	if r := v.do(t, w.srv, "POST", "/api/book/confirm", linkJSON(b.ID, token)); r.Code != 410 {
		t.Fatalf("confirm of an expired hold: %d %s", r.Code, r.Body.String())
	}
	w.srv.PruneBookings(context.Background())
	if _, err := w.st.Bookings().Get(context.Background(), b.ID); err != store.ErrNotFound {
		t.Fatalf("prune kept the expired hold: %v", err)
	}
}

func TestBookingSlugRotation(t *testing.T) {
	w := newBookingWorld(t)
	r := call(t, w.srv, "POST", "/api/booking-types/"+w.typ+"/rotate", "", w.bob)
	var v struct{ Slug string }
	_ = json.Unmarshal(r.Body.Bytes(), &v)
	if r.Code != 200 || v.Slug == w.slug || len(v.Slug) != 32 {
		t.Fatalf("rotate: %d %s", r.Code, r.Body.String())
	}
	x := newVisitor()
	if r := x.do(t, w.srv, "GET", "/api/book/"+w.slug, ""); r.Code != 404 {
		t.Fatalf("old slug after rotation: %d", r.Code)
	}
	x.page(t, w, v.Slug)
	// Turning the page off hides it the same way.
	if r := call(t, w.srv, "PUT", "/api/booking-types/"+w.typ, w.typeBody(false), w.bob); r.Code != 200 {
		t.Fatalf("turn off: %d %s", r.Code, r.Body.String())
	}
	if r := x.do(t, w.srv, "GET", "/api/book/"+v.Slug, ""); r.Code != 404 {
		t.Fatalf("a page turned off: %d", r.Code)
	}
}

func TestBookingSwitchOffHidesEverything(t *testing.T) {
	w := newBookingWorld(t)
	v := newVisitor()
	id, token := w.hold(t, v, v.page(t, w, w.slug).Slots[0].Start)
	if r := call(t, w.srv, "PUT", "/api/admin/booking", `{"allowed":false}`, w.admin); r.Code != 200 || !audited(t, w.st, "admin.booking_pages") {
		t.Fatalf("switch off: %d", r.Code)
	}
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/api/book/" + w.slug, ""},
		{"POST", "/api/book/" + w.slug + "/hold", holdBody("2030-01-01T00:00:00Z", "x@example.net")},
		{"POST", "/api/book/confirm", linkJSON(id, token)},
		{"POST", "/api/book/manage", linkJSON(id, token)},
	} {
		if r := v.do(t, w.srv, tc.method, tc.path, tc.body); r.Code != 404 {
			t.Errorf("%s %s with the switch off: %d", tc.method, tc.path, r.Code)
		}
	}
	r := call(t, w.srv, "GET", "/api/booking-types", "", w.bob)
	if !strings.Contains(r.Body.String(), `"available":false`) || !strings.Contains(r.Body.String(), `"reason":"off"`) {
		t.Fatalf("member list with the switch off: %s", r.Body.String())
	}
	if r := call(t, w.srv, "PUT", "/api/booking-types/"+w.typ, w.typeBody(true), w.bob); r.Code != 409 {
		t.Fatalf("enabling a type with the switch off: %d", r.Code)
	}
}

func TestBookingNeedsKyPost(t *testing.T) {
	t.Setenv("KYCALENDAR_SCHEDULING", "true")
	srv, st, _ := setupTestServer(t)
	admin := loginAs(t, srv, st, "root", "admin")
	r := call(t, srv, "PUT", "/api/admin/booking", `{"allowed":true}`, admin)
	if r.Code != 409 || codeOf(t, r.Body.Bytes()) != "kypost_unpaired" {
		t.Fatalf("allowing booking pages without KyPost: %d %s", r.Code, r.Body.String())
	}
	if r := call(t, srv, "GET", "/api/admin/booking", "", admin); !strings.Contains(r.Body.String(), `"prerequisite":"kypost_unpaired"`) {
		t.Fatalf("admin view: %s", r.Body.String())
	}
	// Even a stored switch does nothing while KyPost is unpaired.
	if err := st.Settings().SetSetting(context.Background(), "booking_pages_allowed", "true"); err != nil {
		t.Fatal(err)
	}
	if r := call(t, srv, "GET", "/api/admin/booking", "", admin); !strings.Contains(r.Body.String(), `"available":false`) {
		t.Fatalf("admin view: %s", r.Body.String())
	}
	if r := newVisitor().do(t, srv, "GET", "/api/book/"+strings.Repeat("a", 32), ""); r.Code != 404 {
		t.Fatalf("public page without KyPost: %d", r.Code)
	}
}

func TestBookingPublicFormsRefuseForgedAndBadInput(t *testing.T) {
	w := newBookingWorld(t)
	v := newVisitor()
	slots := v.page(t, w, w.slug).Slots
	path := "/api/book/" + w.slug + "/hold"
	// No CSRF token, a token from another browser, and a cross-site request are all refused.
	bare := &visitor{ip: "198.51.100.200:1"}
	if r := bare.do(t, w.srv, "POST", path, holdBody(slots[0].Start, "a@example.net")); r.Code != 403 {
		t.Fatalf("no CSRF token: %d", r.Code)
	}
	other := newVisitor()
	other.page(t, w, w.slug)
	forged := &visitor{ip: "198.51.100.201:1", cookies: v.cookies, csrf: other.csrf}
	if r := forged.do(t, w.srv, "POST", path, holdBody(slots[0].Start, "a@example.net")); r.Code != 403 {
		t.Fatalf("another browser's token: %d", r.Code)
	}
	req := httptest.NewRequest("POST", path, strings.NewReader(holdBody(slots[0].Start, "a@example.net")))
	for _, c := range v.cookies {
		req.AddCookie(c)
	}
	req.Header.Set(auth.HeaderCSRF, v.csrf)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	w.srv.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("cross-site hold: %d", rec.Code)
	}
	if len(w.fake.take()) != 0 {
		t.Fatal("a refused form sent mail")
	}

	s0, _ := time.Parse(time.RFC3339, slots[0].Start)
	for name, body := range map[string]string{
		"off-grid":     holdBody(s0.Add(7*time.Minute).Format(time.RFC3339), "a@example.net"),
		"past horizon": holdBody(s0.AddDate(0, 0, 10).Format(time.RFC3339), "a@example.net"),
	} {
		if r := newVisitorWith(v).do(t, w.srv, "POST", path, body); r.Code != 409 || codeOf(t, r.Body.Bytes()) != "slot_unavailable" {
			t.Errorf("%s: %d %s", name, r.Code, r.Body.String())
		}
	}
	for name, body := range map[string]string{
		"long note":      fmt.Sprintf(`{"start":%q,"name":"A","email":"a@example.net","note":%q}`, slots[0].Start, strings.Repeat("x", 1001)),
		"bad email":      fmt.Sprintf(`{"start":%q,"name":"A","email":"not-an-address"}`, slots[0].Start),
		"own email":      fmt.Sprintf(`{"start":%q,"name":"A","email":"bob@example.com"}`, slots[0].Start),
		"quoted name":    fmt.Sprintf(`{"start":%q,"name":"A \"B\"","email":"a@example.net"}`, slots[0].Start),
		"header in name": fmt.Sprintf(`{"start":%q,"name":"A\r\nBcc: x","email":"a@example.net"}`, slots[0].Start),
		"unknown field":  fmt.Sprintf(`{"start":%q,"name":"A","email":"a@example.net","admin":true}`, slots[0].Start),
		"oversize":       `{"note":"` + strings.Repeat("x", 9000) + `"}`,
	} {
		if r := newVisitorWith(v).do(t, w.srv, "POST", path, body); r.Code != 400 {
			t.Errorf("%s: %d %s", name, r.Code, r.Body.String())
		}
	}
	if len(w.fake.take()) != 0 {
		t.Fatal("refused input sent mail")
	}
	// A guessed slug and a guessed booking ID answer exactly like a missing page.
	if r := v.do(t, w.srv, "GET", "/api/book/"+strings.Repeat("f", 32), ""); r.Code != 404 {
		t.Fatalf("unknown slug: %d", r.Code)
	}
	if r := v.do(t, w.srv, "GET", "/api/book/short", ""); r.Code != 404 {
		t.Fatalf("malformed slug: %d", r.Code)
	}
}

// newVisitorWith is a fresh address in v's browser, so per-IP limits do not mix tests.
func newVisitorWith(v *visitor) *visitor {
	x := newVisitor()
	x.cookies, x.csrf = v.cookies, v.csrf
	return x
}

func TestBookingLimits(t *testing.T) {
	w := newBookingWorld(t)
	v := newVisitor()
	slots := v.page(t, w, w.slug).Slots
	path := "/api/book/" + w.slug + "/hold"
	// Two open holds per address, from any IP.
	for i := 0; i < 2; i++ {
		if r := newVisitorWith(v).do(t, w.srv, "POST", path, holdBody(slots[i].Start, "same@example.net")); r.Code != 202 {
			t.Fatalf("hold %d: %d %s", i, r.Code, r.Body.String())
		}
	}
	if r := newVisitorWith(v).do(t, w.srv, "POST", path, holdBody(slots[2].Start, "SAME@example.net")); r.Code != 409 || codeOf(t, r.Body.Bytes()) != "too_many_holds" {
		t.Fatalf("third hold for one address: %d %s", r.Code, r.Body.String())
	}
	// Five hold requests per IP per 15 minutes.
	one := newVisitorWith(v)
	for i := 0; i < 5; i++ {
		one.do(t, w.srv, "POST", path, holdBody(slots[3+i].Start, fmt.Sprintf("ip%d@example.net", i)))
	}
	if r := one.do(t, w.srv, "POST", path, holdBody(slots[9].Start, "ip9@example.net")); r.Code != 429 {
		t.Fatalf("sixth hold from one IP: %d", r.Code)
	}
	// Twenty per booking type per hour, whatever the IPs.
	codes := map[int]int{}
	for i := 0; i < 20; i++ {
		codes[newVisitorWith(v).do(t, w.srv, "POST", path, holdBody(slots[10+i].Start, fmt.Sprintf("t%d@example.net", i))).Code]++
	}
	if codes[429] == 0 {
		t.Fatalf("no per-type limit after 28 holds: %v", codes)
	}
}

// KyPost refusing the hold mail (the member turned sending off) drops the hold and says so.
func TestBookingHoldDroppedWhenMailIsRefused(t *testing.T) {
	w := newBookingWorld(t)
	refusing := httptest.NewTLSServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.WriteHeader(http.StatusConflict)
		_, _ = rw.Write([]byte(`{"code":"sending_off"}`))
	}))
	defer refusing.Close()
	api.SetKyPostClientForTest(w.srv, &kypost.Client{BaseURL: refusing.URL, Secret: kypostSecret, HTTP: refusing.Client()})
	v := newVisitor()
	start := v.page(t, w, w.slug).Slots[0].Start
	if r := v.do(t, w.srv, "POST", "/api/book/"+w.slug+"/hold", holdBody(start, "carol@example.net")); r.Code != 503 || codeOf(t, r.Body.Bytes()) != "mail_failed" {
		t.Fatalf("hold with mail refused: %d %s", r.Code, r.Body.String())
	}
	if got := newVisitor().page(t, w, w.slug).Slots[0].Start; got != start {
		t.Fatal("a hold whose mail failed still blocks the slot")
	}
}

func TestBookingTypeValidationAndOwnership(t *testing.T) {
	w := newBookingWorld(t)
	base := map[string]any{}
	_ = json.Unmarshal([]byte(w.typeBody(false)), &base)
	for name, change := range map[string]func(map[string]any){
		"zero length":     func(m map[string]any) { m["duration_min"] = 0 },
		"long horizon":    func(m map[string]any) { m["horizon_days"] = 61 },
		"bad zone":        func(m map[string]any) { m["zone"] = "Mars/Olympus" },
		"no windows":      func(m map[string]any) { m["windows"] = []any{} },
		"other calendar":  func(m map[string]any) { m["calendar_id"] = "cal_spare_owner" },
		"control title":   func(m map[string]any) { m["title"] = "a\x00b" },
		"negative buffer": func(m map[string]any) { m["buffer_before_min"] = -1 },
	} {
		m := map[string]any{}
		for k, v := range base {
			m[k] = v
		}
		change(m)
		b, _ := json.Marshal(m)
		if r := call(t, w.srv, "POST", "/api/booking-types", string(b), w.bob); r.Code != 400 {
			t.Errorf("%s: %d %s", name, r.Code, r.Body.String())
		}
	}
	// Another member sees none of bob's types and cannot touch them.
	ssoUser(t, w.st, "eve")
	eveUser, _ := w.st.Users().GetUserByID(context.Background(), "usr_eve")
	eve := sessionFor(t, w.st, eveUser)
	if r := call(t, w.srv, "GET", "/api/booking-types", "", eve); strings.Contains(r.Body.String(), w.typ) {
		t.Fatal("eve lists bob's type")
	}
	for _, tc := range []struct{ method, path, body string }{
		{"PUT", "/api/booking-types/" + w.typ, w.typeBody(true)},
		{"POST", "/api/booking-types/" + w.typ + "/rotate", ""},
		{"DELETE", "/api/booking-types/" + w.typ, ""},
	} {
		if r := call(t, w.srv, tc.method, tc.path, tc.body, eve); r.Code != 404 {
			t.Errorf("eve %s %s: %d", tc.method, tc.path, r.Code)
		}
	}
	// A member who does not sign in through KyIdentity cannot turn a page on.
	local := loginAs(t, w.srv, w.st, "lou", "user")
	if r := call(t, w.srv, "GET", "/api/booking-types", "", local); !strings.Contains(r.Body.String(), `"reason":"no_address"`) {
		t.Fatalf("local member: %s", r.Body.String())
	}
}
