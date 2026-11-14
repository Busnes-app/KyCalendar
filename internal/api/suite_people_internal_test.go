package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Busnes-app/ky-primitives/suitedir"

	"github.com/Busnes-app/kycalendar/internal/store"
)

func peopleSearch(t *testing.T, s *Server, u *store.User, q string) (int, []person) {
	t.Helper()
	w := httptest.NewRecorder()
	s.handleSuitePeople(w, withSessionUser(httptest.NewRequest(http.MethodGet, "/api/suite/people?q="+url.QueryEscape(q), nil), u, nil))
	var body struct{ People []person }
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code == http.StatusOK && body.People == nil {
		t.Fatalf("no people array: %s", w.Body)
	}
	return w.Code, body.People
}

func TestSuitePeopleSearchesAsTheUserAndCaches(t *testing.T) {
	s, _ := davInternalServer(t)
	p := newSuiteIdP(t).provider()
	s.signin.Store(p)
	var asked []string
	old := searchPeople
	searchPeople = func(_ context.Context, _ *suitedir.Client, sub, q string, limit int) ([]person, error) {
		asked = append(asked, sub+" "+q)
		if limit != 10 {
			t.Errorf("limit %d", limit)
		}
		if q == "boom" {
			return nil, errors.New("down")
		}
		return []person{{Sub: "s-al", Name: "Alice Example", Email: "alice@example.com"}}, nil
	}
	t.Cleanup(func() { searchPeople = old })

	ann, bob := ssoUser("usr_ann", "sub-ann", p), ssoUser("usr_bob", "sub-bob", p)
	if code, got := peopleSearch(t, s, ann, "al"); code != 200 || len(got) != 1 || got[0].Email != "alice@example.com" {
		t.Fatalf("%d %v", code, got)
	}
	peopleSearch(t, s, ann, "al") // cached for this user and query
	peopleSearch(t, s, bob, "al") // never another user's answer
	if code, got := peopleSearch(t, s, ann, "boom"); code != 200 || len(got) != 0 {
		t.Fatalf("failure: %d %v", code, got)
	}
	// Out of bounds, a local account and an administrator never reach KyIdentity.
	local := &store.User{ID: "usr_loc", Username: "loc", Role: "user", Status: "active", SSOProvider: "local"}
	for _, c := range []struct {
		u *store.User
		q string
	}{{ann, "a"}, {ann, strings.Repeat("a", 65)}, {local, "al"}} {
		if code, got := peopleSearch(t, s, c.u, c.q); code != 200 || len(got) != 0 {
			t.Fatalf("%s %q: %d %v", c.u.ID, c.q, code, got)
		}
	}
	if strings.Join(asked, ",") != "sub-ann al,sub-bob al,sub-ann boom" {
		t.Fatalf("KyIdentity asked %v", asked)
	}
}

func TestSuitePeopleIsRateLimited(t *testing.T) {
	s, _ := davInternalServer(t)
	p := newSuiteIdP(t).provider()
	s.signin.Store(p)
	ann := ssoUser("usr_ann", "sub-ann", p)
	limited := false
	for i := 0; i < 70 && !limited; i++ {
		code, _ := peopleSearch(t, s, ann, "al")
		limited = code == http.StatusTooManyRequests
	}
	if !limited {
		t.Fatal("never rate limited")
	}
}
