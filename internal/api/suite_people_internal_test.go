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

	"github.com/Busnes-app/kycalendar/internal/sso"
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

func TestSearchPeopleAsksKyIdentityAsTheUser(t *testing.T) {
	var got url.Values
	idp := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id, secret, ok := r.BasicAuth(); r.URL.Path != "/api/suite/people" || !ok || id != "kycalendar" || secret != "suite-secret" {
			http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
			return
		}
		got = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"people":[{"sub":"s-al","name":"Alice Example","email":"alice@example.com"}]}`))
	}))
	t.Cleanup(idp.Close)
	dir, err := sso.NewProvider(sso.KindKyIdentity, "x", idp.URL, "kycalendar", "suite-secret", idp.Client()).SuiteDirectory()
	if err != nil {
		t.Fatal(err)
	}
	people, err := searchPeople(context.Background(), dir, "sub-ann", "al", 10)
	if err != nil || len(people) != 1 || people[0] != (person{Sub: "s-al", Name: "Alice Example", Email: "alice@example.com"}) {
		t.Fatalf("people %v err %v", people, err)
	}
	if got.Get("sub") != "sub-ann" || got.Get("q") != "al" || got.Get("limit") != "10" {
		t.Fatalf("query %v", got)
	}
}
