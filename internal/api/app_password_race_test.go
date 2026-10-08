package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// pauseBeforeInsert runs pause after the handler authenticated the request and just before the
// app password is stored: the window a provider switch and reattach can fall into.
type pauseBeforeInsert struct {
	store.AppPasswordStore
	pause func()
}

func (p pauseBeforeInsert) Create(ctx context.Context, by store.Grantor, ap *store.AppPassword, max int) error {
	p.pause()
	return p.AppPasswordStore.Create(ctx, by, ap, max)
}

type pausingStore struct {
	store.Store
	ap store.AppPasswordStore
}

func (s pausingStore) AppPasswords() store.AppPasswordStore { return s.ap }

// A request authenticated under the previous provider must not mint a CalDAV credential after a
// provider switch and reattach purged the account's grants: the person signs in fresh first.
func TestAppPasswordCreatedAcrossAReattachIsRefused(t *testing.T) {
	_, st, cfg := setupTestServer(t)
	ctx := context.Background()
	carol := &store.User{ID: "usr_carol", Username: "carol", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "s1", SSOIssuer: bindingA}
	if err := st.Users().CreateUser(ctx, carol); err != nil {
		t.Fatal(err)
	}
	cookie := sessionFor(t, st, carol)
	srv := api.NewServer(cfg, pausingStore{Store: st, ap: pauseBeforeInsert{AppPasswordStore: st.AppPasswords(), pause: func() {
		// The provider switch disables carol and purges her grants; the reattach re-enables her.
		if _, err := st.Users().BindSignIn(ctx, store.System, store.SignInBinding{Disable: []string{"kysignon", "scim"}}, map[string]string{"signin_bound": bindingB}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.Users().ReattachSSOUser(ctx, store.System, carol.ID, bindingB); err != nil {
			t.Fatal(err)
		}
	}}})
	w := doJSON(t, srv, "POST", "/api/app-passwords", cookie, map[string]string{"label": "stale phone"})
	if w.Code != http.StatusUnauthorized {
		t.Errorf("create across a reattach: %d %s, want 401", w.Code, w.Body.String())
	}
	if list, _ := st.AppPasswords().ListByUser(ctx, carol.ID); len(list) != 0 {
		t.Fatalf("a credential was stored after the purge: %+v", list[0])
	}
	if rows := auditRows(t, st, "app_password.create"); len(rows) != 0 {
		t.Errorf("a refused create was audited: %+v", rows)
	}
}

// The cap is counted inside the insert's transaction: a password created while the request was
// in flight counts.
func TestAppPasswordCapCountsWritesInFlight(t *testing.T) {
	_, st, cfg := setupTestServer(t)
	ctx := context.Background()
	ann := &store.User{ID: "usr_ann", Username: "ann", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "a1"}
	if err := st.Users().CreateUser(ctx, ann); err != nil {
		t.Fatal(err)
	}
	cookie := sessionFor(t, st, ann)
	for i := range 19 {
		if err := st.AppPasswords().Create(ctx, store.Seed, &store.AppPassword{ID: "ap_" + string(rune('a'+i)), UserID: ann.ID, Label: "x", Hash: "h"}, 0); err != nil {
			t.Fatal(err)
		}
	}
	srv := api.NewServer(cfg, pausingStore{Store: st, ap: pauseBeforeInsert{AppPasswordStore: st.AppPasswords(), pause: func() {
		if err := st.AppPasswords().Create(ctx, store.Seed, &store.AppPassword{ID: "ap_racer", UserID: ann.ID, Label: "x", Hash: "h"}, 0); err != nil {
			t.Fatal(err)
		}
	}}})
	if w := doJSON(t, srv, "POST", "/api/app-passwords", cookie, map[string]string{"label": "21st"}); w.Code != http.StatusConflict {
		t.Errorf("21st password while one was in flight: %d %s, want 409", w.Code, w.Body.String())
	}
	if list, _ := st.AppPasswords().ListByUser(ctx, ann.ID); len(list) != 20 {
		t.Fatalf("%d app passwords, want 20", len(list))
	}
}
