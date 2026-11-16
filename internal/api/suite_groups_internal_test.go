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
			g, err := s.store.Groups().GetGroupByID(ctx, gr.GroupID)
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
	if err := s.store.Groups().AddGroupMember(ctx, watchers.ID, ann.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.store.Calendars().SetGrant(ctx, store.CalendarGrant{CalendarID: cal.ID, GroupID: watchers.ID, Role: "editor"}); err != nil {
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
