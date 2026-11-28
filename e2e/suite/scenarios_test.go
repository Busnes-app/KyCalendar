package suite

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	calX     = "https://cal-x.e2e.test"
	calY     = "https://cal-y.e2e.test"
	messages = "https://messages.e2e.test"
	post     = "https://post.e2e.test"
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

// TestScenarios runs S1-S3, S6, and S7 of the e2e design, with KyNotes' org isolation: orgs A and B in cell e2e-1, C in e2e-2,
// made through KyIdentity's platform API only (E5), then each app's checks.
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
	// KyMessages polls every five minutes and KyPost every minute; restarting both runs their
	// startup full org sync immediately so S1 can proceed without waiting for a poll tick.
	s.restartServices("kymessages", "kypost", "kynotes")
	waitUntil(t, 30*time.Second, "KyPost cached orgs A, B, C", func() bool {
		q := fmt.Sprintf("SELECT count(*) FROM kypost_tenancy.orgs WHERE id IN ('%s','%s','%s') AND status = 'active'",
			orgs["A"].id, orgs["B"].id, orgs["C"].id)
		return s.sql("kypost", q) == "3"
	})
	waitUntil(t, 30*time.Second, "KyNotes mirrored orgs A, B, C enabled", func() bool {
		q := fmt.Sprintf("SELECT count(*) FROM orgs WHERE id IN ('%s','%s','%s') AND status = 'active' AND enabled = 1",
			orgs["A"].id, orgs["B"].id, orgs["C"].id)
		return s.sql("kynotes", q) == "3"
	})
	// KyPost creates accounts in multi mode only from signed directory webhooks, never from OIDC
	// tokens. Trigger a full resync now that KyPost holds orgs A, B, and C.
	s.staff.must(200, "POST", issuer+"/api/admin/systems/"+s.kypostSystemID+"/resync", nil, nil)
	waitUntil(t, 45*time.Second, "KyPost provisioned a1, a2, b1, c1 via directory webhook", func() bool {
		q := fmt.Sprintf("SELECT count(*) FROM kypost_tenancy.user_orgs WHERE org_id IN ('%s','%s','%s')",
			orgs["A"].id, orgs["B"].id, orgs["C"].id)
		return s.sql("kypost", q) == "4"
	})

	t.Run("S1 two orgs", func(t *testing.T) {
		for _, c := range []struct {
			app string
			who []string
		}{
			{calX, []string{"a1", "a2", "b1"}}, {calY, []string{"c1"}},
			{messages, []string{"a1", "a2", "b1", "c1"}},
			{post, []string{"a1", "a2", "b1", "c1"}},
			{notes, []string{"a1", "a2", "b1"}}, // KyNotes serves cell e2e-1 only
		} {
			for _, name := range c.who {
				if _, r := people[name].signInToApp(t, c.app); !r.signedIn {
					t.Errorf("%s at %s (org %s): not signed in: %+v", name, c.app, people[name].org, r)
				}
			}
		}
		notRun(t, "KyCalendar OIDC callbacks on a different replica: X uses cookie affinity for its process-local nonce key")
		// KyNotes reports each org it serves to KyIdentity (POST /api/suite/storage); KyIdentity
		// stores it in org_storage, and its answer is the grant KyNotes admits writes under.
		waitUntil(t, 90*time.Second, "KyNotes' storage reports for A and B reach KyIdentity", func() bool {
			return s.notesReported(orgs["A"].id) != "" && s.notesReported(orgs["B"].id) != ""
		})
		if r := s.notesReported(orgs["C"].id); r != "" {
			t.Errorf("KyNotes (cell e2e-1) reported usage %s for org C (e2e-2)", r)
		}
		notRun(t, "the other apps' first storage report reaching KyIdentity: checked for KyNotes only")
		notRun(t, "KyPost native mailbox allocation: requires CoreDNS TXT/MX verification for the suite mail domain")
	})

	t.Run("S2 cells", func(t *testing.T) {
		// Refused as an org the deployment does not serve: KyCalendar's org gate answer.
		for _, c := range []struct{ app, who string }{{calX, "c1"}, {calY, "a1"}, {calY, "b1"}, {notes, "c1"}} {
			_, r := people[c.who].signInToApp(t, c.app)
			if r.signedIn || r.status != http.StatusForbidden || !strings.Contains(r.body, `"org_not_active"`) {
				t.Errorf("%s at %s: want 403 org_not_active, got %+v", c.who, c.app, r)
			}
		}
		notRun(t, "KyPost cell isolation and booking page of a slug made on Y: KyPost runs as one deployment serving both cells (e2e-1,e2e-2) with native mail off")
		notRun(t, "backup and attachment keys under cells/<cell>/: only KyNotes uses the run's S3, under orgs/<org>/ (no cell prefix yet), and KyRecovery is not in the run")
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
		meeting(t, a1b, people, start.Add(2*time.Hour))
		notRun(t, "KyPost's outbound send log: native mail/SMTP outbound requires CoreDNS")
	})

	// KyNotes' data for S6 and S7: a1's and b1's notebook, page, attachment, links and device.
	var aNotes, bNotes notesData
	t.Run("KyNotes cross-org isolation on every credential", func(t *testing.T) {
		a1n, b1n := people["a1"].signedIn(t, notes), people["b1"].signedIn(t, notes)
		waitUntil(t, 90*time.Second, "KyNotes holds pool grants for A and B", func() bool {
			return s.sql("kynotes", fmt.Sprintf("SELECT count(*) FROM org_pool WHERE org_id IN ('%s','%s')", orgs["A"].id, orgs["B"].id)) == "2"
		})
		aNotes, bNotes = s.notesWrite(t, a1n, orgs["A"].id, people["a1"].userID), s.notesWrite(t, b1n, orgs["B"].id, people["b1"].userID)
		if s.s3Files("kynotes", "orgs/"+orgs["B"].id+"/") == 0 {
			t.Errorf("no KyNotes object under orgs/%s/ in S3", orgs["B"].id)
		}
		// Each credential works for its own org.
		if got := s.notesAnswers(b1n, bNotes); got != [4]int{200, 200, 200, 200} {
			t.Errorf("b1's KyNotes credentials (session, device, sealed link, page link): %v", got)
		}
		a1n.must(200, "GET", notes+"/api/v1/objects/"+aNotes.oid, nil, notesHdr)
		// And none reaches the other org's notebook: session, device, or the org's links.
		for _, c := range []struct {
			who  string
			code int
		}{
			{"b1 session", func() int { c, _ := b1n.do("GET", notes+"/api/v1/objects/"+aNotes.oid, nil, notesHdr); return c }()},
			{"b1 session changes", func() int {
				c, _ := b1n.do("GET", notes+"/api/v1/containers/"+aNotes.cid+"/changes", nil, notesHdr)
				return c
			}()},
			{"b1 device", s.notesDevice(bNotes, "/api/v1/objects/"+aNotes.oid)},
			{"a1 session", func() int { c, _ := a1n.do("GET", notes+"/api/v1/objects/"+bNotes.oid, nil, notesHdr); return c }()},
		} {
			if c.code == http.StatusOK || c.code/100 == 5 {
				t.Errorf("%s read the other org's KyNotes page: %d", c.who, c.code)
			}
		}
		var listed []struct{ ID string }
		var wrap struct{ Containers []struct{ ID string } }
		raw := b1n.must(200, "GET", notes+"/api/v1/containers", nil, notesHdr)
		if json.Unmarshal(raw, &listed) != nil {
			json.Unmarshal(raw, &wrap)
			listed = wrap.Containers
		}
		for _, c := range listed {
			if c.ID == aNotes.cid {
				t.Errorf("b1's KyNotes notebook list holds org A's notebook %s", aNotes.cid)
			}
		}
		// The pool usage KyNotes reports to KyIdentity grows with B's writes.
		waitUntil(t, 90*time.Second, "KyNotes reports org B's new bytes to KyIdentity", func() bool {
			n, _ := strconv.ParseInt(s.notesReported(orgs["B"].id), 10, 64)
			return n > 0
		})
	})

	t.Run("S6 org suspension and resume", func(t *testing.T) {
		a1Cal, a1Msg, a1Post := people["a1"].signedIn(t, calX), people["a1"].signedIn(t, messages), people["a1"].signedIn(t, post)
		b1Old := people["b1"].browser
		b1Cal, b1Msg, b1Post := people["b1"].signedIn(t, calX), people["b1"].signedIn(t, messages), people["b1"].signedIn(t, post)
		b1Me := b1Cal.me(calX)
		b1Notes := people["b1"].signedIn(t, notes)
		a1Notes := people["a1"].signedIn(t, notes)
		var state string // the last state a wait saw, for a timeout
		defer func() {
			if t.Failed() {
				t.Logf("last state: %s", state)
			}
		}()
		var b1DAV struct {
			Password string `json:"password"`
		}
		json.Unmarshal(b1Cal.must(201, "POST", calX+"/api/app-passwords", map[string]string{"label": "s6-dav", "scope": "dav"}, nil), &b1DAV)
		if code := davPropfind(s.web, calX+"/dav/"+b1Me.ID+"/", b1Me.Username, b1DAV.Password); code != http.StatusMultiStatus {
			t.Fatalf("b1 DAV before suspend: want 207, got %d", code)
		}

		// Suspend org B through the platform API.
		orgs["B"].revision++
		billing.must(200, "POST", "/orgs/"+orgs["B"].id+"/suspend", map[string]any{"reason": "payment", "revision": orgs["B"].revision})
		// KyMessages polls every five minutes; restart it for an immediate sync while letting
		// KyCalendar and KyPost pick up the suspension on their live 1-minute pollers.
		s.restartServices("kymessages")
		waitUntil(t, 90*time.Second, "KyCalendar X and KyPost observe org B suspended", func() bool {
			calDown := davPropfind(s.web, calX+"/dav/"+b1Me.ID+"/", b1Me.Username, b1DAV.Password) == http.StatusForbidden
			code, body := b1Post.do("GET", post+"/api/auth/me", nil, nil)
			postDown := code != http.StatusOK || strings.Contains(string(body), `"authenticated":false`)
			got := s.notesAnswers(b1Notes, bNotes)
			state = fmt.Sprintf("kycal_x down %t, kypost down %t, kynotes %v", calDown, postDown, got)
			return calDown && postDown && got == [4]int{401, 401, 404, 404}
		})

		// Every credential of org B is refused across all four apps.
		if code, _ := b1Old.do("GET", issuer+"/api/auth/me", nil, nil); code == http.StatusOK {
			t.Errorf("b1 old KyIdentity session stayed active while org B suspended: %d", code)
		}
		freshB1 := s.browser()
		freshB1.must(200, "GET", issuer+"/api/auth/csrf", nil, nil)
		if code, body := freshB1.do("POST", issuer+"/api/auth/login", map[string]string{"username": people["b1"].email, "password": people["b1"].password}, nil); code == http.StatusOK {
			t.Errorf("b1 signed into KyIdentity while org B suspended: %d %s", code, body)
		}
		if code, _ := b1Cal.do("GET", calX+"/api/calendars", nil, nil); code != http.StatusUnauthorized && code != http.StatusForbidden {
			t.Errorf("b1 old KyCalendar session while org B suspended: want 401/403, got %d", code)
		}
		if code, body := b1Msg.do("GET", messages+"/api/v1/rooms", nil, nil); code != http.StatusUnauthorized && code != http.StatusForbidden {
			t.Errorf("b1 old KyMessages session while org B suspended: want 401/403, got %d %s", code, body)
		}
		if code, body := b1Post.do("GET", post+"/api/auth/me", nil, nil); code == http.StatusOK && !strings.Contains(string(body), `"authenticated":false`) {
			t.Errorf("b1 old KyPost session while org B suspended: %d %s", code, body)
		}

		// Org A stays working across every app.
		people["a1"].browser.must(200, "GET", issuer+"/api/auth/me", nil, nil)
		a1Cal.must(200, "GET", calX+"/api/calendars", nil, nil)
		a1Msg.must(200, "GET", messages+"/api/auth/me", nil, nil)
		if code, body := a1Post.do("GET", post+"/api/auth/me", nil, nil); code != http.StatusOK || !strings.Contains(string(body), `"authenticated":true`) {
			t.Errorf("a1 KyPost session affected by org B suspension: %d %s", code, body)
		}
		if got := s.notesAnswers(a1Notes, aNotes); got != [4]int{200, 200, 200, 200} {
			t.Errorf("a1's KyNotes credentials during org B's suspension: %v", got)
		}

		// Resume org B through the platform API.
		orgs["B"].revision++
		billing.must(200, "POST", "/orgs/"+orgs["B"].id+"/resume", map[string]any{"revision": orgs["B"].revision})
		s.restartServices("kymessages")
		waitUntil(t, 90*time.Second, "KyCalendar X and KyPost observe org B resumed", func() bool {
			calUp := davPropfind(s.web, calX+"/dav/"+b1Me.ID+"/", b1Me.Username, b1DAV.Password) == http.StatusMultiStatus
			postUp := s.sql("kypost", fmt.Sprintf("SELECT status FROM kypost_tenancy.orgs WHERE id = '%s'", orgs["B"].id)) == "active"
			notesStatus := s.sql("kynotes", fmt.Sprintf("SELECT status FROM orgs WHERE id = '%s'", orgs["B"].id))
			state = fmt.Sprintf("kycal_x up %t, kypost up %t, kynotes %s", calUp, postUp, notesStatus)
			return calUp && postUp && notesStatus == "active"
		})
		// KyNotes: the links serve again with no re-linking; the session and the device paired
		// from it ended with the suspension (an SSO-derived device must be linked again).
		if got := s.notesAnswers(b1Notes, bNotes); got != [4]int{401, 401, 200, 200} {
			t.Errorf("b1's KyNotes credentials after resume (session, device, sealed link, page link): %v, want [401 401 200 200]", got)
		}

		// Old revoked browser sessions stay dead after resume; DAV app passwords work again.
		if code, _ := b1Cal.do("GET", calX+"/api/calendars", nil, nil); code != http.StatusUnauthorized {
			t.Errorf("b1 old KyCalendar session came back after resume: %d", code)
		}
		if code, body := b1Post.do("GET", post+"/api/auth/me", nil, nil); code == http.StatusOK && !strings.Contains(string(body), `"authenticated":false`) {
			t.Errorf("b1 old KyPost session came back after resume: %d %s", code, body)
		}
		// Fresh sign-in succeeds across all four apps.
		people["b1"].browser = s.browser()
		people["b1"].browser.signIn(people["b1"].email, people["b1"].password)
		for _, app := range []string{calX, messages, post, notes} {
			if _, r := people["b1"].signInToApp(t, app); !r.signedIn {
				t.Errorf("b1 fresh sign-in at %s after resume failed: %+v", app, r)
			}
		}
		notRun(t, "KyCalendar booking page 404 on suspension: booking pages require KyCalendar-KyPost mail pairing")
		notRun(t, "KyPost IMAP/SMTP app passwords on suspension: native mail is disabled without CoreDNS")
	})

	t.Run("S7 org deletion and purge", func(t *testing.T) {
		orgs["B"].revision++
		sched := billing.must(200, "POST", "/orgs/"+orgs["B"].id+"/schedule-deletion", map[string]any{"grace_days": 7, "revision": orgs["B"].revision})
		if sched["status"] != "deletion_scheduled" || sched["delete_after"] == nil {
			t.Fatalf("schedule-deletion org B: %v", sched)
		}
		// Documented E2E shortcut (spec S7): advance delete_after into the past on KyIdentity's DB
		// instead of waiting seven real days before calling DELETE /platform/v1/orgs/{id}.
		s.sql("kyidentity", fmt.Sprintf("UPDATE orgs SET delete_after = extract(epoch from now())::bigint - 60 WHERE id = '%s'", orgs["B"].id))
		del := billing.must(200, "DELETE", "/orgs/"+orgs["B"].id, nil)
		if del["status"] != "deleted" {
			t.Fatalf("DELETE org B: %v", del)
		}
		if got := billing.must(200, "GET", "/orgs/"+orgs["B"].id, nil); got["status"] != "deleted" {
			t.Errorf("GET deleted org B tombstone: %v", got)
		}
		if n := s.sql("kyidentity", fmt.Sprintf("SELECT count(*) FROM users WHERE org_id = '%s'", orgs["B"].id)); n != "0" {
			t.Errorf("kyidentity still holds %s users for deleted org B", n)
		}

		// KyMessages polls every five minutes; restart it so SyncTenancy and PurgeDeletedOrgs run.
		// KyCalendar (orgs + org-purge) and KyPost (StartOrgPoller) purge on their 1-minute ticks.
		s.restartServices("kymessages")
		var purge string // the last state seen, for a timeout
		defer func() {
			if t.Failed() {
				t.Logf("purge state: %s", purge)
			}
		}()
		waitUntil(t, 150*time.Second, "KyCalendar X, KyMessages, KyPost and KyNotes purge org B", func() bool {
			calPurged := s.sql("kycal_x", fmt.Sprintf("SELECT status || ':' || (purged_at IS NOT NULL)::text FROM orgs WHERE id = '%s'", orgs["B"].id)) == "deleted:true"
			postPurged := s.sql("kypost", fmt.Sprintf(
				"SELECT o.status || ':' || EXISTS(SELECT 1 FROM kypost_tenancy.org_purges p WHERE p.org_id = o.id AND p.done_at IS NOT NULL)::text FROM kypost_tenancy.orgs o WHERE o.id = '%s'",
				orgs["B"].id)) == "deleted:true"
			msgPurged := s.sql("kymessages", fmt.Sprintf("SELECT status FROM tenants WHERE id = '%s'", orgs["B"].id)) == "deleted" &&
				s.sql("kymessages", fmt.Sprintf("SELECT count(*) FROM users WHERE org_id = '%s'", orgs["B"].id)) == "0"
			notesStatus, notesRows, notesFiles := s.sql("kynotes", fmt.Sprintf("SELECT status FROM orgs WHERE id = '%s'", orgs["B"].id)),
				s.notesOrgRows(orgs["B"].id), s.s3Files("kynotes", "orgs/"+orgs["B"].id+"/")
			purge = fmt.Sprintf("kycal_x %t, kypost %t, kymessages %t, kynotes status %s rows %v objects %d",
				calPurged, postPurged, msgPurged, notesStatus, notesRows, notesFiles)
			// KyNotes keeps erased_subjects (a restore re-erases from it), and org_usage until
			// its late-PUT window has passed (checked below).
			delete(notesRows, "erased_subjects")
			delete(notesRows, "org_usage")
			return calPurged && postPurged && msgPurged && notesStatus == "deleted" && len(notesRows) == 0 && notesFiles == 0
		})
		// KyNotes marks the purge done, deleting org_usage and B's prefix once more, only after a
		// purge run that began 20 minutes (blobstore.LatePutWindow) after it fenced B's writers.
		// Documented shortcut, like delete_after above: move the fence back past that window.
		s.sql("kynotes", fmt.Sprintf("UPDATE server_settings SET value = (value::bigint - 1260)::text WHERE key = 'purge_fence:%s'", orgs["B"].id))
		waitUntil(t, 90*time.Second, "KyNotes marks org B purged", func() bool {
			return s.sql("kynotes", fmt.Sprintf("SELECT purged_at <> '' FROM orgs WHERE id = '%s' AND status = 'deleted'", orgs["B"].id)) == "t"
		})
		// No org_id column names B but erased_subjects, which holds b1's subject only; no
		// object is left under B's prefix; the tombstone stays; B's credentials are refused.
		left := s.notesOrgRows(orgs["B"].id)
		if left["erased_subjects"] != "1" || s.sql("kynotes", fmt.Sprintf("SELECT subject FROM erased_subjects WHERE org_id = '%s'", orgs["B"].id)) != people["b1"].userID {
			t.Errorf("kynotes erased_subjects for org B: want b1's subject only, got %v", left)
		}
		delete(left, "erased_subjects")
		if len(left) != 0 {
			t.Errorf("kynotes rows still naming deleted org B: %v", left)
		}
		if n := s.s3Files("kynotes", "orgs/"+orgs["B"].id+"/"); n != 0 {
			t.Errorf("%d KyNotes objects left under deleted org B's prefix", n)
		}
		if r := s.notesReported(orgs["B"].id); r != "" && r != "0" {
			t.Errorf("KyIdentity still holds %s bytes reported by KyNotes for deleted org B, want none or 0", r)
		}
		if n := s.sql("kynotes", fmt.Sprintf("SELECT count(*) FROM users WHERE id = '%s'", bNotes.user)); n != "0" {
			t.Errorf("kynotes still holds b1's account")
		}
		if got := s.notesAnswers(s.browser(), bNotes); got != [4]int{401, 401, 404, 404} {
			t.Errorf("deleted org B's KyNotes credentials: %v, want [401 401 404 404]", got)
		}
		if a1n, r := people["a1"].signInToApp(t, notes); !r.signedIn {
			t.Errorf("a1 at KyNotes after org B deletion: %+v", r)
		} else {
			a1n.must(200, "GET", notes+"/api/v1/objects/"+aNotes.oid, nil, notesHdr)
		}
		if s.s3Files("kynotes", "orgs/"+orgs["A"].id+"/") == 0 {
			t.Errorf("org A's KyNotes objects went with org B's purge")
		}

		if n := s.sql("kycal_x", fmt.Sprintf("SELECT count(*) FROM users WHERE org_id = '%s'", orgs["B"].id)); n != "0" {
			t.Errorf("kycal_x still holds %s users for deleted org B", n)
		}
		if n := s.sql("kypost", fmt.Sprintf("SELECT count(*) FROM kypost_tenancy.user_orgs WHERE org_id = '%s'", orgs["B"].id)); n != "0" {
			t.Errorf("kypost still holds %s user_orgs for deleted org B", n)
		}
		// KyPost intentionally keeps subject_orgs after purge: opaque IDs stop a purged subject joining another org.
		if n := s.sql("kypost", fmt.Sprintf("SELECT count(*) FROM kypost_tenancy.subject_orgs WHERE org_id = '%s'", orgs["B"].id)); n != "1" {
			t.Errorf("kypost subject_orgs for deleted org B = %s, want 1 (kept as opaque subject tombstone)", n)
		}

		// Orgs A and C stay untouched across every app.
		for _, c := range []struct{ who, app string }{
			{"a1", calX}, {"a1", messages}, {"a1", post},
			{"c1", calY}, {"c1", messages}, {"c1", post},
		} {
			if _, r := people[c.who].signInToApp(t, c.app); !r.signedIn {
				t.Errorf("%s at %s broken after org B deletion: %+v", c.who, c.app, r)
			}
		}
		notRun(t, "KyMessages MarkTenantPurged and S3 attachment prefix deletion: S3/SeaweedFS is not in the run")
		notRun(t, "KyPost address_tombstones for deleted native mailboxes: native mail is disabled without CoreDNS")
		notRun(t, "KyRecovery backup deletion: KyRecovery is not in the run")
	})
}

// meeting is S3's meeting link (contract B, KyMessages #58): a1 asks KyCalendar X for a video
// meeting with a2; KyCalendar's meeting worker PUTs it to KyMessages under the explicit pair
// secret, and the room holds exactly the organizer and the attendee. b1, in another org, cannot
// open it.
func meeting(t *testing.T, a1b *browser, people map[string]*person, at time.Time) {
	t.Helper()
	var cals []struct {
		ID   string `json:"id"`
		Role string `json:"role"`
	}
	json.Unmarshal(a1b.must(200, "GET", calX+"/api/calendars", nil, nil), &cals)
	cal := ""
	for _, c := range cals {
		if c.Role == "owner" {
			cal = c.ID
			break
		}
	}
	if cal == "" {
		t.Fatalf("a1 owns no calendar at X: %+v", cals)
	}
	a2x := people["a2"].signedIn(t, calX).me(calX)
	var made struct {
		UID string `json:"uid"`
	}
	json.Unmarshal(a1b.must(201, "POST", calX+"/api/calendars/"+cal+"/events", map[string]any{"title": "S3 meeting",
		"start": at.Format(time.RFC3339), "end": at.Add(time.Hour).Format(time.RFC3339), "zone": "UTC",
		"attendees": []string{a2x.Email}, "meeting": true}, nil), &made)
	// KyCalendar's meetings job runs every 5 s; the link appears once KyMessages answered.
	q := url.Values{"start": {at.Add(-time.Hour).Format(time.RFC3339)}, "end": {at.Add(2 * time.Hour).Format(time.RFC3339)}, "calendar": {cal}}
	link := ""
	for deadline := time.Now().Add(time.Minute); link == "" && time.Now().Before(deadline); time.Sleep(2 * time.Second) {
		var events []struct {
			UID        string `json:"uid"`
			MeetingURL string `json:"meeting_url"`
		}
		json.Unmarshal(a1b.must(200, "GET", calX+"/api/events?"+q.Encode(), nil, nil), &events)
		for _, e := range events {
			if e.UID == made.UID {
				link = e.MeetingURL
			}
		}
	}
	room, ok := strings.CutPrefix(link, messages+"/chat/")
	if !ok || room == "" || strings.Contains(room, "/") {
		t.Fatalf("the S3 meeting got no KyMessages room link: %q", link)
	}
	a1m, a2m := people["a1"].signedIn(t, messages), people["a2"].signedIn(t, messages)
	want := map[string]bool{a1m.me(messages).ID: true, a2m.me(messages).ID: true}
	var got struct {
		Members []struct {
			UserID string `json:"user_id"`
		} `json:"members"`
	}
	json.Unmarshal(a2m.must(200, "GET", messages+"/api/v1/rooms/"+room, nil, nil), &got)
	ids := map[string]bool{}
	for _, m := range got.Members {
		ids[m.UserID] = true
	}
	if !maps.Equal(ids, want) {
		t.Errorf("meeting room %s members %v, want a1 and a2 %v", room, ids, want)
	}
	t.Logf("meeting room %s: %d members, a1 and a2", room, len(got.Members))
	if code, _ := people["b1"].signedIn(t, messages).do("GET", messages+"/api/v1/rooms/"+room, nil, nil); code == http.StatusOK {
		t.Errorf("b1 (org B) opened org A's meeting room %s", room)
	}
}

// enableApps enables every app for every org through the platform API.
func enableApps(billing platform, orgs map[string]*org) {
	for _, o := range orgs {
		billing.must(http.StatusOK, "PATCH", "/orgs/"+o.id, map[string]any{"revision": o.revision + 1, "apps": map[string]any{
			"kycalendar": map[string]any{"enabled": true, "quotas": map[string]any{}},
			"kymessages": map[string]any{"enabled": true, "quotas": map[string]any{}},
			"kypost":     map[string]any{"enabled": true, "quotas": map[string]any{}},
			"kynotes":    map[string]any{"enabled": true, "quotas": map[string]any{}}}})
		o.revision++
	}
}

// restartServices restarts compose services and waits for them to pass healthchecks again.
func (s *suite) restartServices(services ...string) {
	s.t.Helper()
	if out, err := s.compose(append([]string{"restart"}, services...)...); err != nil {
		s.t.Fatalf("restart %v: %v\n%s", services, err, out)
	}
	if out, err := s.compose(append([]string{"up", "-d", "--wait", "--wait-timeout", "120"}, services...)...); err != nil {
		s.t.Fatalf("wait %v: %v\n%s", services, err, out)
	}
}

// waitUntil polls fn until it returns true or timeout elapses.
func waitUntil(t *testing.T, timeout time.Duration, desc string, fn func() bool) {
	t.Helper()
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(2 * time.Second) {
		if fn() {
			return
		}
	}
	if !fn() {
		t.Fatalf("timed out after %s waiting for: %s", timeout, desc)
	}
}

// davPropfind issues a Depth:0 CalDAV PROPFIND with HTTP Basic auth and returns its status code.
func davPropfind(tr http.RoundTripper, target, user, pass string) int {
	req, _ := http.NewRequest("PROPFIND", target, strings.NewReader(`<propfind xmlns="DAV:"><prop><resourcetype/></prop></propfind>`))
	req.SetBasicAuth(user, pass)
	req.Header.Set("Depth", "0")
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	res, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		return 0
	}
	res.Body.Close()
	return res.StatusCode
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
	start := map[string]string{
		calX:     "/api/sso/kysignon/login",
		calY:     "/api/sso/kysignon/login",
		messages: "/api/sso/kyidentity/login",
		post:     "/api/auth/oidc/login",
		notes:    "/api/v1/auth/oidc/login",
	}[app]
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
		case loc.Host != "id.e2e.test" && (loc.Path == "/" || loc.Path == "/read" || loc.Path == "/settings"):
			r.status = res.StatusCode
			hop = 12 // the app's callback sent the browser home
			continue
		}
		next = loc.String()
	}
	mePath := "/api/auth/me"
	if app == notes {
		mePath = "/api/v1/auth/session"
	}
	code, me := b.do("GET", app+mePath, nil, nil)
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
