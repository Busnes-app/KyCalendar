package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

const (
	bindingA = "kyidentity https://a.example"
	bindingB = "kyidentity https://b.example"
)

// liveAt makes a KyIdentity provider at issuer the live sign-in.
func liveAt(srv *api.Server, issuer string) {
	api.SetSignInForTest(srv, sso.NewProvider(sso.KindKyIdentity, "KyIdentity", issuer, "kc", "sec", nil))
}

// carolOf seeds carol, a KyIdentity account of issuer a disabled by a move to b, with grants.
func carolOf(t *testing.T, st store.Store) *store.User {
	t.Helper()
	carol := &store.User{ID: "usr_carol", Username: "carol", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "s1", SSOIssuer: bindingA}
	if err := st.Users().CreateUser(context.Background(), carol); err != nil {
		t.Fatal(err)
	}
	sessionFor(t, st, carol)
	if err := st.AppPasswords().Create(context.Background(), &store.AppPassword{ID: "ap_carol", UserID: carol.ID, Label: "phone", Hash: "h"}); err != nil {
		t.Fatal(err)
	}
	carol.Status = "inactive" // whole-row write: the grants stay, so the reattach must revoke them
	if err := st.Users().UpdateUser(context.Background(), carol); err != nil {
		t.Fatal(err)
	}
	return carol
}

func TestAdminReattach(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	admin := loginAs(t, srv, st, "root", "admin")
	carol := carolOf(t, st)
	reattachAs := func(id, body string) (int, string) {
		w := call(t, srv, "POST", "/api/admin/users/"+id+"/reattach", body, admin)
		return w.Code, codeOf(t, w.Body.Bytes())
	}
	reattach := func(id string) (int, string) { return reattachAs(id, `{"binding":"`+bindingB+`"}`) }

	if code, c := reattach(carol.ID); code != http.StatusConflict || c != "no_signin" {
		t.Errorf("no live sign-in: %d %s, want 409 no_signin", code, c)
	}
	liveAt(srv, "https://b.example")
	if code, c := reattach("usr_root"); code != http.StatusConflict || c != "local_account" {
		t.Errorf("local account: %d %s, want 409 local_account", code, c)
	}
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_olga", Username: "olga", Role: "user", Status: "inactive", SSOProvider: "oidc", SSOSubject: "o1"}); err != nil {
		t.Fatal(err)
	}
	if code, c := reattach("usr_olga"); code != http.StatusConflict || c != "other_provider" {
		t.Errorf("an oidc account under KyIdentity: %d %s, want 409 other_provider", code, c)
	}
	if code, _ := reattach("usr_nobody"); code != http.StatusNotFound {
		t.Errorf("unknown person: %d, want 404", code)
	}
	for _, body := range []string{"", `{}`, `{"binding":""}`, `not json`} {
		if code, _ := reattachAs(carol.ID, body); code != http.StatusBadRequest {
			t.Errorf("body %q: %d, want 400", body, code)
		}
	}
	// The admin confirmed a binding that is no longer live: nothing is written.
	if code, c := reattachAs(carol.ID, `{"binding":"`+bindingA+`"}`); code != http.StatusConflict || c != "binding_changed" {
		t.Errorf("stale binding: %d %s, want 409 binding_changed", code, c)
	}
	if u, _ := st.Users().GetUserByID(ctx, carol.ID); u.SSOIssuer != bindingA || u.Status != "inactive" {
		t.Fatalf("binding_changed wrote: %+v", u)
	}
	// Disabled by the IdP under the live binding: the IdP restores it, not reattach.
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_dan", Username: "dan", Role: "user", Status: "inactive", SSOProvider: "scim", SSOSubject: "d1", SSOIssuer: bindingB}); err != nil {
		t.Fatal(err)
	}
	if code, c := reattach("usr_dan"); code != http.StatusConflict || c != "managed_externally" {
		t.Errorf("IdP-disabled row: %d %s, want 409 managed_externally", code, c)
	}
	if u, _ := st.Users().GetUserByID(ctx, "usr_dan"); u.Status != "inactive" {
		t.Fatalf("an IdP-disabled row was enabled: %+v", u)
	}
	// Active but bound to another issuer: reattachable, and stamped.
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_eve", Username: "eve", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "e1", SSOIssuer: bindingA}); err != nil {
		t.Fatal(err)
	}

	// The list tells the screen who needs it and what each is bound to.
	var list struct {
		Users []struct {
			ID            string `json:"id"`
			NeedsReattach bool   `json:"needs_reattach"`
			BoundTo       string `json:"bound_to"`
		} `json:"users"`
		SignInBinding string `json:"signin_binding"`
	}
	_ = json.Unmarshal(call(t, srv, "GET", "/api/admin/users", "", admin).Body.Bytes(), &list)
	if list.SignInBinding != bindingB {
		t.Errorf("signin_binding %q, want %q", list.SignInBinding, bindingB)
	}
	for _, u := range list.Users {
		want := u.ID == carol.ID || u.ID == "usr_eve"
		if u.NeedsReattach != want {
			t.Errorf("%s: needs_reattach %v, want %v", u.ID, u.NeedsReattach, want)
		}
		if u.ID == carol.ID && u.BoundTo != bindingA {
			t.Errorf("carol bound_to %q, want %q", u.BoundTo, bindingA)
		}
	}

	if code, c := reattach("usr_eve"); code != http.StatusOK {
		t.Fatalf("active row of another issuer: %d %s", code, c)
	}
	if u, _ := st.Users().GetUserByID(ctx, "usr_eve"); u.SSOIssuer != bindingB || u.Status != "active" {
		t.Fatalf("eve after reattach: %+v", u)
	}

	restore := api.SetStepUpWindowForTest(0)
	if code, c := reattach(carol.ID); code != http.StatusForbidden || c != "reauth_required" {
		t.Errorf("stale sign-in: %d %s, want 403 reauth_required", code, c)
	}
	restore()
	if u, _ := st.Users().GetUserByID(ctx, carol.ID); u.SSOIssuer != bindingA || u.Status != "inactive" {
		t.Fatalf("a refused reattach wrote: %+v", u)
	}

	if code, c := reattach(carol.ID); code != http.StatusOK {
		t.Fatalf("reattach: %d %s", code, c)
	}
	u, _ := st.Users().GetUserByID(ctx, carol.ID)
	if u.SSOIssuer != bindingB || u.Status != "active" || u.Role != "user" {
		t.Fatalf("after reattach: %+v", u)
	}
	if _, err := st.Sessions().GetSession(ctx, crypto.SHA256Hex([]byte("tok-"+carol.ID))); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("carol's session survived the reattach: %v", err)
	}
	if list, _ := st.AppPasswords().ListByUser(ctx, carol.ID); len(list) != 0 {
		t.Error("app passwords survived the reattach")
	}
	var rows []*store.AuditRecord
	for _, r := range auditRows(t, st, "admin.user_reattach") {
		if r.Resource == carol.ID {
			rows = append(rows, r)
		}
	}
	if len(rows) != 1 || rows[0].UserID != "usr_root" || rows[0].Details != `from="`+bindingA+`" to="`+bindingB+`"` {
		t.Fatalf("audit: %+v", rows)
	}
	// Already bound and active: nothing to do, nothing audited.
	if code, _ := reattach(carol.ID); code != http.StatusOK {
		t.Errorf("no-op: %d, want 200", code)
	}
	if rows := auditRows(t, st, "admin.user_reattach"); len(rows) != 2 {
		t.Errorf("a no-op was audited: %d rows, want carol's and eve's", len(rows))
	}
}

func (u revokeBeforeWrite) ReattachSSOUser(ctx context.Context, a store.Actor, id, binding string) (string, error) {
	u.revoke()
	return u.UserStore.ReattachSSOUser(ctx, a, id, binding)
}

// A reattach authorised before its admin was demoted fails: it would let an IdP account in.
func TestReattachFailsWhenTheActorIsRevokedMidRequest(t *testing.T) {
	_, st, cfg := setupTestServer(t)
	ctx := context.Background()
	root := &store.User{ID: "usr_root", Username: "root", Role: "admin", Status: "active", SSOProvider: "local"}
	bob := &store.User{ID: "usr_bob", Username: "bob", Role: "admin", Status: "active", SSOProvider: "local"}
	for _, u := range []*store.User{root, bob} {
		if err := st.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	carol := carolOf(t, st)
	srv := api.NewServer(cfg, revokingStore{Store: st, users: revokeBeforeWrite{UserStore: st.Users(), revoke: func() {
		if err := st.Users().SetRole(ctx, store.System, bob.ID, "user"); err != nil {
			t.Fatal(err)
		}
	}}})
	liveAt(srv, "https://b.example")
	w := call(t, srv, "POST", "/api/admin/users/"+carol.ID+"/reattach", `{"binding":"`+bindingB+`"}`, sessionFor(t, st, bob))
	if w.Code != http.StatusForbidden || codeOf(t, w.Body.Bytes()) != "actor_revoked" {
		t.Fatalf("%d %s, want 403 actor_revoked", w.Code, w.Body.String())
	}
	if u, _ := st.Users().GetUserByID(ctx, carol.ID); u.SSOIssuer != bindingA || u.Status != "inactive" {
		t.Fatalf("carol changed: %+v", u)
	}
	if list, _ := st.AppPasswords().ListByUser(ctx, carol.ID); len(list) != 1 {
		t.Error("a refused reattach revoked carol's app password")
	}
	if _, err := st.Sessions().GetSession(ctx, crypto.SHA256Hex([]byte("tok-"+carol.ID))); err != nil {
		t.Errorf("a refused reattach revoked carol's session: %v", err)
	}
	if rows := auditRows(t, st, "admin.user_reattach"); len(rows) != 0 {
		t.Errorf("a refused reattach was audited: %+v", rows)
	}
}

// Another admin's reattach commits and the IdP disables the row before this write: the row is
// already on the live binding, so it stays disabled and nothing is audited.
func TestReattachRacingAnotherKeepsTheIdPsDeactivation(t *testing.T) {
	_, st, cfg := setupTestServer(t)
	ctx := context.Background()
	root := &store.User{ID: "usr_root", Username: "root", Role: "admin", Status: "active", SSOProvider: "local"}
	if err := st.Users().CreateUser(ctx, root); err != nil {
		t.Fatal(err)
	}
	carol := carolOf(t, st)
	srv := api.NewServer(cfg, revokingStore{Store: st, users: revokeBeforeWrite{UserStore: st.Users(), revoke: func() {
		// The other admin's reattach, then the IdP's deactivation.
		if _, err := st.Users().ReattachSSOUser(ctx, store.System, carol.ID, bindingB); err != nil {
			t.Fatal(err)
		}
		if err := st.Users().RevokeSSOUser(ctx, carol.ID, true); err != nil {
			t.Fatal(err)
		}
	}}})
	liveAt(srv, "https://b.example")
	w := call(t, srv, "POST", "/api/admin/users/"+carol.ID+"/reattach", `{"binding":"`+bindingB+`"}`, sessionFor(t, st, root))
	if w.Code != http.StatusConflict || codeOf(t, w.Body.Bytes()) != "managed_externally" {
		t.Fatalf("%d %s, want 409 managed_externally", w.Code, w.Body.String())
	}
	if u, _ := st.Users().GetUserByID(ctx, carol.ID); u.Status != "inactive" {
		t.Fatalf("the IdP's deactivation was undone: %+v", u)
	}
	if rows := auditRows(t, st, "admin.user_reattach"); len(rows) != 0 {
		t.Errorf("a skipped reattach was audited: %+v", rows)
	}
}
