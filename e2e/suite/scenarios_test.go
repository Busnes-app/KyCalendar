//go:build kye2e

package suite

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

const (
	calX     = "https://cal-x.e2e.test"
	calY     = "https://cal-y.e2e.test"
	messages = "https://messages.e2e.test"
	// suiteMail is the suite mail domain mailbox addresses live on.
	suiteMail = "mail.e2e.test"
)

type org struct {
	name, cell, id string
	revision       int64
}

type person struct {
	name, org, email, password, userID string
	holder                             bool
	browser                            *browser
}

func (p *person) mailbox() string { return p.name + "@" + suiteMail }

// TestScenarios runs S1-S3 of the e2e design: orgs A and B in cell e2e-1, C in e2e-2, made
// through KyIdentity's platform API only (E5), then each app's checks.
func TestScenarios(t *testing.T) {
	s := start(t)
	sink := mailSink{t, "http://" + hostPort(t, s.compose, "mail", "8025")}
	// Platform mail and the suite mail domain: the administrator's HTTP settings.
	s.staff.admin(s.adminPass, 200, "PUT", issuer+"/api/admin/mail", map[string]any{
		"host": "smtp.e2e.test", "port": 1025, "security": "starttls", "from": "id@e2e.test"})
	s.staff.admin(s.adminPass, 200, "PUT", issuer+"/api/admin/suite-settings/email-domains",
		map[string]any{"domains": []string{suiteMail}})
	base := "http://" + hostPort(t, s.compose, "kyidentity", "5868")
	billing := platform{t, base, s.platformToken("e2e-billing", "orgs:read,orgs:write,orgs:delete")}
	portal := platform{t, base, s.platformToken("e2e-portal", "members:read,members:write")}

	orgs := map[string]*org{"A": {name: "A", cell: "e2e-1"}, "B": {name: "B", cell: "e2e-1"}, "C": {name: "C", cell: "e2e-2"}}
	people := map[string]*person{
		"a1": {name: "a1", org: "A", holder: true}, "a2": {name: "a2", org: "A"},
		"b1": {name: "b1", org: "B", holder: true}, "c1": {name: "c1", org: "C", holder: true},
	}
	for _, p := range people {
		p.email, p.password = p.name+"@people.e2e.test", password()
	}

	// S1, provisioning: holders with the orgs, then a2, each with a mailbox address.
	for _, key := range []string{"A", "B", "C"} {
		o, h := orgs[key], people[strings.ToLower(key)+"1"]
		ans := billing.must(201, "POST", "/orgs", map[string]any{"name": "Org " + key, "home_region": o.cell, "plan": "e2e",
			"max_seats": 5, "storage_bytes": 10 << 30, "storage_per_seat_bytes": 0, "holder_email": h.email, "holder_name": h.name,
			"holder_mailbox_address": h.mailbox(), "billing_ref": "e2e-" + key + "-" + random(4), "revision": 1})
		o.id, o.revision, h.userID = str(t, ans, "id"), 1, str(t, ans, "account_holder_user_id")
		if ans["home_region"] != o.cell || ans["status"] != "active" {
			t.Fatalf("org %s: %v", key, ans)
		}
	}
	a2 := people["a2"]
	ans := portal.must(201, "POST", "/orgs/"+orgs["A"].id+"/members", map[string]any{"email": a2.email, "name": a2.name, "mailbox_address": a2.mailbox()})
	a2.userID = str(t, ans, "user_id")

	// Each person activates from the mailed link and signs in with a password alone (E8).
	for _, p := range people {
		b := s.browser()
		b.must(200, "GET", issuer+"/api/auth/csrf", nil, nil)
		b.must(200, "POST", issuer+"/api/auth/activate", map[string]string{"token": sink.activation(p.email), "password": p.password}, nil)
		b.signIn(p.email, p.password)
		p.browser = b
	}
	for _, key := range []string{"A", "B", "C"} {
		members := portal.must(200, "GET", "/orgs/"+orgs[key].id+"/members", nil)
		if n := len(members["members"].([]any)); n != map[string]int{"A": 2, "B": 1, "C": 1}[key] {
			t.Errorf("org %s has %d members: %v", key, n, members)
		}
	}

	// Before any app is enabled for them, KyIdentity lets no member into an app.
	if _, r := people["a1"].signInToApp(t, calX); r.signedIn || r.idpError != "access_denied" {
		t.Errorf("a1 at X before app enablement: %+v", r)
	}

	enableApps(billing, orgs)
	// KyMessages polls every five minutes and has no poll at sign-in; its start is a full sync.
	if out, err := s.compose("restart", "kymessages"); err != nil {
		t.Fatalf("restart kymessages: %v\n%s", err, out)
	}
	if out, err := s.compose("up", "-d", "--wait", "kymessages"); err != nil {
		t.Fatalf("kymessages: %v\n%s", err, out)
	}

	t.Run("S1 two orgs", func(t *testing.T) {
		for _, c := range []struct {
			app  string
			who  []string
			want bool
		}{
			{calX, []string{"a1", "a2", "b1"}, true}, {calY, []string{"c1"}, true},
			{messages, []string{"a1", "a2", "b1", "c1"}, true},
		} {
			for _, name := range c.who {
				if _, r := people[name].signInToApp(t, c.app); !r.signedIn {
					t.Errorf("%s at %s (org %s): not signed in: %+v", name, c.app, people[name].org, r)
				}
			}
		}
		notRun(t, "KyCalendar OIDC callbacks on a different replica: X uses cookie affinity for its process-local nonce key")
		notRun(t, "each app's first storage report reaching KyIdentity: no API shows an org's reported usage")
	})

	t.Run("S2 cells", func(t *testing.T) {
		// Refused as an org the deployment does not serve: KyCalendar's org gate answer.
		for _, c := range []struct{ app, who string }{{calX, "c1"}, {calY, "a1"}, {calY, "b1"}} {
			_, r := people[c.who].signInToApp(t, c.app)
			if r.signedIn || r.status != http.StatusForbidden || !strings.Contains(r.body, `"org_not_active"`) {
				t.Errorf("%s at %s: want 403 org_not_active, got %+v", c.who, c.app, r)
			}
		}
		notRun(t, "KyPost inbound naming another cell's member and the booking page of a slug made on Y (KyPost is not in the run)")
		notRun(t, "backup and attachment keys under cells/<cell>/ (S3 and KyRecovery are not in the run)")
	})

	t.Run("S3 invitation within one org, delivered directly", func(t *testing.T) {
		a1b, a2b := people["a1"].signedIn(t, calX), people["a2"].signedIn(t, calX)
		a1, a2 := a1b.me(calX), a2b.me(calX)
		var pw struct {
			Password string `json:"password"`
		}
		json.Unmarshal(a1b.must(201, "POST", calX+"/api/app-passwords", map[string]string{"label": "e2e", "scope": "dav"}, nil), &pw)
		start := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Hour)
		ics := strings.Join([]string{"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//kye2e//EN", "BEGIN:VEVENT",
			"UID:s3-" + random(8) + "@e2e.test", "DTSTAMP:" + time.Now().UTC().Format("20060102T150405Z"),
			"DTSTART:" + start.Format("20060102T150405Z"), "DTEND:" + start.Add(time.Hour).Format("20060102T150405Z"),
			"SUMMARY:S3", "ORGANIZER:mailto:" + a1.Email, "ATTENDEE;PARTSTAT=NEEDS-ACTION;RSVP=TRUE:mailto:" + a2.Email,
			"END:VEVENT", "END:VCALENDAR", ""}, "\r\n")
		req, _ := http.NewRequest("PUT", calX+"/dav/"+a1.ID+"/calendars/default/s3.ics", strings.NewReader(ics))
		req.SetBasicAuth(a1.Username, pw.Password)
		req.Header.Set("Content-Type", "text/calendar; charset=utf-8")
		res, err := (&http.Client{Transport: s.web}).Do(req)
		if err != nil || res.StatusCode != http.StatusCreated {
			t.Fatalf("CalDAV PUT: %v %v", err, res)
		}
		res.Body.Close()
		// a2's calendar holds it without mail: a1's free/busy lookup of a2 shows the hour busy.
		q := url.Values{"users": {a2.Email}, "start": {start.Format(time.RFC3339)}, "end": {start.Add(time.Hour).Format(time.RFC3339)}}
		busy := false
		for deadline := time.Now().Add(time.Minute); !busy && time.Now().Before(deadline); time.Sleep(2 * time.Second) {
			var fb struct {
				People []struct {
					Busy []any `json:"busy"`
				} `json:"people"`
			}
			json.Unmarshal(a1b.must(200, "GET", calX+"/api/freebusy?"+q.Encode(), nil, nil), &fb)
			busy = len(fb.People) == 1 && len(fb.People[0].Busy) > 0
		}
		if !busy {
			t.Error("a2's free/busy does not show a1's invitation")
		}
		if n := sink.count(a2.Email) + sink.count(people["a2"].mailbox()); n != 1 { // the activation mail only
			t.Errorf("%d mails reached a2, want only the activation mail", n)
		}
		notRun(t, "the KyMessages meeting link: KyCalendar sends the organizer as `sub` but KyMessages reads `organizer_sub`, and no KyMessages peer link is set")
		notRun(t, "KyPost's send log (KyPost is not in the run)")
	})
}

// enableApps enables both apps for every org through the platform API.
func enableApps(billing platform, orgs map[string]*org) {
	for _, o := range orgs {
		billing.must(http.StatusOK, "PATCH", "/orgs/"+o.id, map[string]any{"revision": o.revision + 1, "apps": map[string]any{
			"kycalendar": map[string]any{"enabled": true, "quotas": map[string]any{}},
			"kymessages": map[string]any{"enabled": true, "quotas": map[string]any{}}}})
		o.revision++
	}
}

// notRun records a check this run cannot make, so a reader sees it was not proven.
func notRun(t *testing.T, what string) { t.Log("NOT RUN: " + what) }

func str(t *testing.T, m map[string]any, k string) string {
	t.Helper()
	v, _ := m[k].(string)
	if v == "" {
		t.Fatalf("no %q in %v", k, m)
	}
	return v
}

// platformToken mints a platform API token with KyIdentity's own CLI, inside its container.
// The token is never logged.
func (s *suite) platformToken(name, scopes string) string {
	s.t.Helper()
	out, err := s.compose("exec", "-T", "kyidentity", "kyidentity", "platform-token", "create", "--name", name, "--scopes", scopes, "--days", "1")
	if err != nil {
		s.t.Fatalf("platform-token create %s: %v", name, err)
	}
	for _, line := range strings.Split(out, "\n") {
		var tok struct {
			Token string `json:"token"`
		}
		if json.Unmarshal([]byte(line), &tok) == nil && strings.HasPrefix(tok.Token, "kyp_") {
			return tok.Token
		}
	}
	s.t.Fatalf("platform-token create %s: no token printed", name)
	return ""
}

// signIn is the result of one OIDC sign-in at an app.
type signIn struct {
	signedIn bool
	idpError string // the error KyIdentity sent back to the app, if any
	status   int    // the app's answer on its callback
	body     string
}

// signInToApp uses the IdP session established during activation and password sign-in.
func (p *person) signInToApp(t *testing.T, app string) (*browser, signIn) {
	b := *p.browser
	b.t = t
	return &b, b.appSignIn(app, p)
}

func (b *browser) appSignIn(app string, p *person) signIn {
	b.t.Helper()
	start := map[string]string{calX: "/api/sso/kysignon/login", calY: "/api/sso/kysignon/login", messages: "/api/sso/kyidentity/login"}[app]
	next, r := app+start, signIn{}
	for hop := 0; hop < 12; hop++ {
		res, body := b.send("GET", next, nil, nil)
		if res.StatusCode/100 != 3 {
			r.status, r.body = res.StatusCode, string(body)
			break
		}
		loc, err := res.Request.URL.Parse(res.Header.Get("Location"))
		if err != nil {
			b.t.Fatal(err)
		}
		switch {
		case loc.Host == "id.e2e.test" && loc.Path == "/login":
			in := loc.Query().Get("interaction")
			b.must(200, "GET", issuer+"/api/auth/csrf", nil, nil)
			b.must(200, "POST", issuer+"/api/auth/login", map[string]string{"interaction": in, "username": p.email, "password": p.password}, nil)
			next = issuer + "/oauth/authorize?interaction=" + url.QueryEscape(in)
			continue
		case loc.Host != "id.e2e.test" && loc.Query().Get("error") != "":
			r.idpError = loc.Query().Get("error")
		case loc.Host != "id.e2e.test" && loc.Path == "/":
			r.status = res.StatusCode
			hop = 12 // the app's callback sent the browser home
			continue
		}
		next = loc.String()
	}
	code, me := b.do("GET", app+"/api/auth/me", nil, nil)
	r.signedIn = code == 200 && !strings.Contains(string(me), `"authenticated":false`)
	return r
}

// signedIn is a browser p has signed in to app, or the test stops.
func (p *person) signedIn(t *testing.T, app string) *browser {
	t.Helper()
	b, result := p.signInToApp(t, app)
	if !result.signedIn {
		t.Fatalf("%s at %s: %+v", p.name, app, result)
	}
	return b
}

type me struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email"`
}

// me reads KyCalendar's /api/auth/me for this browser's user.
func (b *browser) me(app string) me {
	b.t.Helper()
	var ans struct {
		User me `json:"user"`
	}
	if err := json.Unmarshal(b.must(200, "GET", app+"/api/auth/me", nil, nil), &ans); err != nil || ans.User.ID == "" {
		b.t.Fatalf("me at %s: %v", app, err)
	}
	return ans.User
}

// platform calls the platform provisioning API with one token, as billing or the portal does.
type platform struct {
	t     *testing.T
	base  string
	token string
}

// call sends one platform request; every write carries a fresh Idempotency-Key.
func (p platform) call(method, path string, body any) (int, map[string]any) {
	p.t.Helper()
	var r io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		r = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, p.base+"/platform/v1"+path, r)
	if err != nil {
		p.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/json")
	if method != "GET" {
		req.Header.Set("Idempotency-Key", random(16))
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		p.t.Fatalf("platform %s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	var out map[string]any
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err := json.Unmarshal(raw, &out); err != nil {
		p.t.Fatalf("platform %s %s: %d %s", method, path, res.StatusCode, raw)
	}
	return res.StatusCode, out
}

func (p platform) must(want int, method, path string, body any) map[string]any {
	p.t.Helper()
	code, out := p.call(method, path, body)
	if code != want {
		p.t.Fatalf("platform %s %s: %d, want %d: %v", method, path, code, want, out)
	}
	return out
}

// mailSink reads the mail sink through Mailpit's API.
type mailSink struct {
	t    *testing.T
	base string
}

var activateLink = regexp.MustCompile(`/activate\?token=([A-Za-z0-9_-]+)`)

// activation waits for the activation mail to `to` and returns its token.
func (m mailSink) activation(to string) string {
	m.t.Helper()
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(time.Second) {
		var list struct {
			Messages []struct {
				ID string `json:"ID"`
			} `json:"messages"`
		}
		m.get("/api/v1/search?query="+url.QueryEscape(fmt.Sprintf("to:%q subject:%q", to, "Activate your account")), &list)
		if len(list.Messages) > 0 {
			var msg struct{ Text string }
			m.get("/api/v1/message/"+list.Messages[0].ID, &msg)
			if tok := activateLink.FindStringSubmatch(msg.Text); tok != nil {
				return tok[1]
			}
			m.t.Fatalf("activation mail to %s has no link", to)
		}
	}
	m.t.Fatalf("no activation mail reached %s", to)
	return ""
}

// count is how many mails reached `to`.
func (m mailSink) count(to string) int {
	m.t.Helper()
	var list struct {
		Total int `json:"messages_count"`
	}
	m.get("/api/v1/search?query="+url.QueryEscape(fmt.Sprintf("to:%q", to)), &list)
	return list.Total
}

func (m mailSink) get(path string, v any) {
	m.t.Helper()
	res, err := http.Get(m.base + path)
	if err != nil {
		m.t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || json.NewDecoder(res.Body).Decode(v) != nil {
		m.t.Fatalf("mail sink GET %s: %d", path, res.StatusCode)
	}
}

// password is a random member password KyIdentity's policy accepts (12+ bytes).
func password() string { return "Pw-" + random(12) }
