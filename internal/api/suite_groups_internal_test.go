package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// suiteGroupsIdP is KyIdentity's GET /api/suite/groups: it answers status with body.
type suiteGroupsIdP struct {
	*httptest.Server
	mu     sync.Mutex
	status int
	body   string
}

func newSuiteGroupsIdP(t *testing.T) *suiteGroupsIdP {
	idp := &suiteGroupsIdP{status: http.StatusOK, body: `{"groups":[]}`}
	idp.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id, secret, ok := r.BasicAuth(); r.URL.Path != "/api/suite/groups" || !ok || id != "kycalendar" || secret != "suite-secret" {
			http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
			return
		}
		idp.mu.Lock()
		defer idp.mu.Unlock()
		w.WriteHeader(idp.status)
		fmt.Fprint(w, idp.body)
	}))
	t.Cleanup(idp.Close)
	return idp
}

func (idp *suiteGroupsIdP) answer(status int, body string) {
	idp.mu.Lock()
	defer idp.mu.Unlock()
	idp.status, idp.body = status, body
}

func (idp *suiteGroupsIdP) provider() *sso.Provider {
	return sso.NewProvider(sso.KindKyIdentity, "single sign-on", idp.URL, "kycalendar", "suite-secret", idp.Client())
}

// suiteState is every group calendar with its archived flag and its suite group's members.
func suiteState(t *testing.T, s *Server) string {
	t.Helper()
	ctx := context.Background()
	cals, err := s.store.Calendars().ListCalendarsByKind(ctx, "group")
	if err != nil {
		t.Fatal(err)
	}
	out := ""
	for _, c := range cals {
		gs, err := s.store.Calendars().ListGrants(ctx, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		var grants []string // sorted here: backends collate names differently
		for _, gr := range gs {
			g, err := s.store.Org(store.DefaultOrg).GetGroup(ctx, gr.GroupID)
			if err != nil {
				t.Fatal(err)
			}
			slices.Sort(g.Members)
			grants = append(grants, fmt.Sprintf(" %s:%s:%v", g.DisplayName, gr.Role, g.Members))
		}
		slices.Sort(grants)
		out += fmt.Sprintf("%s %s archived=%t%s\n", c.ID, c.Name, c.Archived, strings.Join(grants, ""))
	}
	return out
}

func createEvent(t *testing.T, s *Server, u *store.User, calID string) int {
	t.Helper()
	start := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	data, _ := json.Marshal(map[string]any{"title": "Stand-up", "start": start.Format(time.RFC3339), "end": start.Add(time.Hour).Format(time.RFC3339), "zone": "UTC"})
	req := httptest.NewRequest(http.MethodPost, "/api/calendars/x/events", bytes.NewReader(data))
	req.SetPathValue("id", calID)
	w := httptest.NewRecorder()
	s.handleCreateEvent(w, withSessionUser(req, u, nil))
	return w.Code
}

func TestSyncSuiteGroups(t *testing.T) {
	ctx := context.Background()
	s, _ := davInternalServer(t)
	idp := newSuiteGroupsIdP(t)
	p := idp.provider()
	s.signin.Store(p)
	ann, adm := ssoUser("usr_ann", "sub-ann", p), ssoUser("usr_adm", "sub-adm", p)
	adm.Role = "admin"
	for _, u := range []*store.User{ann, adm} {
		if err := s.store.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	active := `{"groups":[{"id":"kg1","name":"Sales","slug":"sales","state":"active","members":["sub-adm","sub-ann","sub-nobody"]}]}`
	idp.answer(http.StatusOK, active)
	s.SyncSuiteGroups(ctx)
	cals, err := s.store.Calendars().ListCalendarsByKind(ctx, "group")
	if err != nil || len(cals) != 1 {
		t.Fatalf("calendars %v %v", cals, err)
	}
	cal := cals[0]
	want := cal.ID + " Sales archived=false suite-group:kg1:editor:[usr_ann]\n"
	if got := suiteState(t, s); got != want {
		t.Fatalf("state\n%s want\n%s", got, want)
	}
	if code := createEvent(t, s, ann, cal.ID); code != http.StatusCreated {
		t.Fatalf("member create: %d", code)
	}
	// Running again, KyIdentity failing (500, 401) or a group missing from the list changes
	// nothing.
	s.SyncSuiteGroups(ctx)
	for _, a := range []struct {
		status int
		body   string
	}{{http.StatusInternalServerError, "down"}, {http.StatusUnauthorized, `{"error":"invalid_client"}`}, {http.StatusOK, `{"groups":[]}`}, {http.StatusOK, `{}`}} {
		idp.answer(a.status, a.body)
		s.SyncSuiteGroups(ctx)
		if got := suiteState(t, s); got != want {
			t.Fatalf("after %d %s:\n%s want\n%s", a.status, a.body, got, want)
		}
	}
	// Archived: no members, read-only for anyone an administrator still grants it to, and
	// marked in the admin list. The removal takes one pass.
	watchers := &store.Group{ID: "grp_w", DisplayName: "Watchers"}
	if err := s.store.Groups().CreateGroup(ctx, watchers); err != nil {
		t.Fatal(err)
	}
	if err := s.store.Org(store.DefaultOrg).AddGroupMember(ctx, watchers.ID, ann.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.store.Org(store.DefaultOrg).SetGrant(ctx, store.CalendarGrant{CalendarID: cal.ID, GroupID: watchers.ID, Role: "editor"}); err != nil {
		t.Fatal(err)
	}
	idp.answer(http.StatusOK, `{"groups":[{"id":"kg1","name":"Sales","slug":"sales","state":"archived","members":[]}]}`)
	began := time.Now()
	s.SyncSuiteGroups(ctx)
	t.Logf("archive pass took %v", time.Since(began))
	g, err := s.store.Groups().GetGroupByName(ctx, store.SuiteGroupDisplayName("kg1"))
	if err != nil || len(g.Members) != 0 {
		t.Fatalf("archived members %v %v", g, err)
	}
	if code := createEvent(t, s, ann, cal.ID); code != http.StatusForbidden {
		t.Fatalf("archived create: %d", code)
	}
	w := httptest.NewRecorder()
	s.handleListGroupCalendars(w, withSessionUser(httptest.NewRequest(http.MethodGet, "/api/admin/calendars", nil), adm, nil))
	var list []groupCalendarView
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list) != 1 || !list[0].Archived {
		t.Fatalf("admin list %s %v", w.Body, err)
	}
	// Re-enabled: the same calendar, writable again.
	idp.answer(http.StatusOK, active)
	s.SyncSuiteGroups(ctx)
	if got := suiteState(t, s); got != cal.ID+" Sales archived=false Watchers:editor:[usr_ann] suite-group:kg1:editor:[usr_ann]\n" {
		t.Fatalf("re-enabled\n%s", got)
	}
	if code := createEvent(t, s, ann, cal.ID); code != http.StatusCreated {
		t.Fatalf("re-enabled create: %d", code)
	}
}

// Another OIDC provider, or none, never asks for suite groups.
func TestSyncSuiteGroupsOnlyForKyIdentity(t *testing.T) {
	s, _ := davInternalServer(t)
	idp := newSuiteGroupsIdP(t)
	idp.answer(http.StatusOK, `{"groups":[{"id":"kg1","name":"Sales","slug":"sales","state":"active","members":[]}]}`)
	s.SyncSuiteGroups(context.Background())
	s.signin.Store(sso.NewProvider(sso.KindOIDC, "other", idp.URL, "kycalendar", "suite-secret", idp.Client()))
	s.SyncSuiteGroups(context.Background())
	if got := suiteState(t, s); got != "" {
		t.Fatalf("state %s", got)
	}
}

// Build step 4g (tenancy spec D9, L11): in multi-tenant mode each serving org's suite groups are
// listed with its org_id and reconciled into that org alone. A member listed in another org's
// group never joins it, a suspended org is not asked, and one org's failed list skips only that
// org.
func TestSyncSuiteGroupsPerOrg(t *testing.T) {
	ctx := context.Background()
	s, _ := davInternalServer(t)
	var mu sync.Mutex
	asked := []string{}
	answers := map[string]string{
		"org-a": `{"groups":[{"id":"ga","name":"A team","slug":"a-team","state":"active","members":["sub-a","sub-b"]}]}`,
		"org-b": `{"groups":[{"id":"gb","name":"B team","slug":"b-team","state":"active","members":["sub-b"]}]}`,
	}
	idp := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		org := r.URL.Query().Get("org_id")
		mu.Lock()
		asked = append(asked, org)
		body, ok := answers[org]
		mu.Unlock()
		if !ok {
			http.Error(w, `{"error":"unknown_org"}`, http.StatusNotFound)
			return
		}
		fmt.Fprint(w, body)
	}))
	t.Cleanup(idp.Close)
	p := sso.NewProvider(sso.KindKyIdentity, "single sign-on", idp.URL, "kycalendar", "suite-secret", idp.Client())
	s.signin.Store(p)
	if err := s.store.ApplyOrgs(ctx, "", []store.MirroredOrg{
		{ID: "org-a", Status: "active", Revision: 1, Enabled: true, Quotas: "{}"},
		{ID: "org-b", Status: "active", Revision: 1, Enabled: true, Quotas: "{}"},
		{ID: "org-c", Status: "suspended", Revision: 1, Enabled: true, Quotas: "{}"},
		{ID: "org-0", Status: "active", Revision: 1, Enabled: true, Quotas: "{}"}, // polled first; its list fails
	}, 1); err != nil {
		t.Fatal(err)
	}
	for _, u := range []*store.User{ssoUser("usr_a", "sub-a", p), ssoUser("usr_b", "sub-b", p)} {
		u.OrgID = "org-a"
		if u.ID == "usr_b" {
			u.OrgID = "org-b"
		}
		if err := s.store.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.store.Settings().SetSetting(ctx, store.TenancySetting, "multi"); err != nil {
		t.Fatal(err)
	}
	s.SyncSuiteGroups(ctx)

	mu.Lock()
	got := fmt.Sprint(asked)
	mu.Unlock()
	if !slices.Contains(asked, "org-a") || !slices.Contains(asked, "org-b") || slices.Contains(asked, "org-c") || slices.Contains(asked, "") ||
		slices.Contains(asked, string(store.DefaultOrg)) || !slices.Contains(asked, "org-0") {
		t.Errorf("asked for %s", got)
	}
	cals, err := s.store.Calendars().ListCalendarsByKind(ctx, "group")
	if err != nil || len(cals) != 2 {
		t.Fatalf("group calendars %v %v", cals, err)
	}
	// Each calendar's group must belong to exactly one org, with that org's member only.
	found := map[string]string{}
	for _, c := range cals {
		gs, err := s.store.Calendars().ListGrants(ctx, c.ID)
		if err != nil || len(gs) != 1 {
			t.Fatalf("%s grants %v %v", c.ID, gs, err)
		}
		for _, org := range []store.OrgID{"org-a", "org-b"} {
			if g, err := s.store.Org(org).GetGroup(ctx, gs[0].GroupID); err == nil {
				found[string(org)] += fmt.Sprintf("%s %v;", g.DisplayName, g.Members)
			}
		}
	}
	if want := map[string]string{"org-a": "suite-group:ga [usr_a];", "org-b": "suite-group:gb [usr_b];"}; fmt.Sprint(found) != fmt.Sprint(want) {
		t.Errorf("groups by org %v, want %v", found, want)
	}
}

// With more serving orgs than KyIdentity's rate limit lets one pass list, the next pass starts at
// the first org that failed, so no org is starved: two passes reconcile all four.
func TestSyncSuiteGroupsPerOrgRotatesPastARateLimit(t *testing.T) {
	ctx := context.Background()
	s, _ := davInternalServer(t)
	var mu sync.Mutex
	budget := 0
	idp := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if budget == 0 {
			http.Error(w, `{"error":"rate_limited"}`, http.StatusTooManyRequests)
			return
		}
		budget--
		org := r.URL.Query().Get("org_id")
		fmt.Fprintf(w, `{"groups":[{"id":"g-%s","name":"Team %s","slug":"team","state":"active","members":[]}]}`, org, org)
	}))
	t.Cleanup(idp.Close)
	s.signin.Store(sso.NewProvider(sso.KindKyIdentity, "single sign-on", idp.URL, "kycalendar", "suite-secret", idp.Client()))
	var orgs []store.MirroredOrg
	for _, id := range []store.OrgID{"org-a", "org-b", "org-c", "org-d"} {
		orgs = append(orgs, store.MirroredOrg{ID: id, Status: "active", Revision: 1, Enabled: true, Quotas: "{}"})
	}
	if err := s.store.ApplyOrgs(ctx, "", orgs, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.store.Settings().SetSetting(ctx, store.TenancySetting, "multi"); err != nil {
		t.Fatal(err)
	}
	pass := func() int {
		t.Helper()
		mu.Lock()
		budget = 2
		mu.Unlock()
		s.SyncSuiteGroups(ctx)
		cals, err := s.store.Calendars().ListCalendarsByKind(ctx, "group")
		if err != nil {
			t.Fatal(err)
		}
		return len(cals)
	}
	if n := pass(); n != 2 {
		t.Fatalf("first pass made %d group calendars, want 2", n)
	}
	if n := pass(); n != 4 {
		t.Errorf("second pass left %d of 4 orgs reconciled: the rate-limited tail was starved", n)
	}
}

// An org that always fails (gone from KyIdentity) must not pin the rotation: with it sorted first
// and two lists allowed per pass, every other org is reconciled within three passes.
func TestSyncSuiteGroupsPerOrgSurvivesAPermanentFailure(t *testing.T) {
	ctx := context.Background()
	s, _ := davInternalServer(t)
	var mu sync.Mutex
	budget := 0
	idp := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if budget == 0 {
			http.Error(w, `{"error":"rate_limited"}`, http.StatusTooManyRequests)
			return
		}
		budget-- // a refused list costs the client its rate limit too
		org := r.URL.Query().Get("org_id")
		if org == "org-0" {
			http.Error(w, `{"error":"unknown_org"}`, http.StatusNotFound)
			return
		}
		fmt.Fprintf(w, `{"groups":[{"id":"g-%s","name":"Team %s","slug":"team","state":"active","members":[]}]}`, org, org)
	}))
	t.Cleanup(idp.Close)
	s.signin.Store(sso.NewProvider(sso.KindKyIdentity, "single sign-on", idp.URL, "kycalendar", "suite-secret", idp.Client()))
	var orgs []store.MirroredOrg
	for _, id := range []store.OrgID{"org-0", "org-a", "org-b", "org-c", "org-d"} {
		orgs = append(orgs, store.MirroredOrg{ID: id, Status: "active", Revision: 1, Enabled: true, Quotas: "{}"})
	}
	if err := s.store.ApplyOrgs(ctx, "", orgs, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.store.Settings().SetSetting(ctx, store.TenancySetting, "multi"); err != nil {
		t.Fatal(err)
	}
	n := 0
	for pass := 0; pass < 3; pass++ {
		mu.Lock()
		budget = 2
		mu.Unlock()
		s.SyncSuiteGroups(ctx)
		cals, err := s.store.Calendars().ListCalendarsByKind(ctx, "group")
		if err != nil {
			t.Fatal(err)
		}
		n = len(cals)
	}
	if n != 4 {
		t.Errorf("after three passes %d of 4 orgs reconciled: a permanently failing org pinned the rotation", n)
	}
}
