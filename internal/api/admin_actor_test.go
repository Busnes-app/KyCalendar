package api_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// revokeBeforeWrite runs revoke after the handler has authorised the request and just before
// the store write, which is the window the recheck closes.
type revokeBeforeWrite struct {
	store.UserStore
	revoke func()
}

func (u revokeBeforeWrite) SetRole(ctx context.Context, a store.Actor, id, role string) error {
	u.revoke()
	return u.UserStore.SetRole(ctx, a, id, role)
}

func (u revokeBeforeWrite) SetStatus(ctx context.Context, a store.Actor, id, status string) error {
	u.revoke()
	return u.UserStore.SetStatus(ctx, a, id, status)
}

func (u revokeBeforeWrite) CreateUserAs(ctx context.Context, a store.Actor, nu *store.User) error {
	u.revoke()
	return u.UserStore.CreateUserAs(ctx, a, nu)
}

func (u revokeBeforeWrite) ResetPassword(ctx context.Context, a store.Actor, id, hash string) error {
	u.revoke()
	return u.UserStore.ResetPassword(ctx, a, id, hash)
}

type revokingStore struct {
	store.Store
	users store.UserStore
}

func (s revokingStore) Users() store.UserStore { return s.users }

func TestAccessWriteFailsWhenTheActorIsRevokedMidRequest(t *testing.T) {
	_, st, cfg := setupTestServer(t)
	ctx := context.Background()
	bob := &store.User{ID: "usr_bob", Username: "bob", Role: "admin", Status: "active", SSOProvider: "local"}
	for _, u := range []*store.User{
		{ID: "usr_root", Username: "root", Role: "admin", Status: "active", SSOProvider: "local"},
		bob,
		{ID: "usr_ann", Username: "ann", PasswordHash: "h_ann", Role: "user", Status: "active", SSOProvider: "local"},
		{ID: "usr_off", Username: "off", Role: "user", Status: "inactive", SSOProvider: "local"},
	} {
		if err := st.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	revocations := map[string]func() error{
		// Demotion by another admin revokes bob's sessions in the same transaction.
		"demoted":    func() error { return st.Users().SetRole(ctx, store.System, bob.ID, "user") },
		"signed out": func() error { return st.Sessions().DeleteUserSessions(ctx, bob.ID) },
	}
	requests := []struct{ name, path, body string }{
		{"promote ann", "/api/admin/users/usr_ann/role", `{"role":"admin"}`},
		{"reset ann", "/api/admin/users/usr_ann/reset-password", ""},
		{"disable ann", "/api/admin/users/usr_ann/disable", ""},
		{"enable off", "/api/admin/users/usr_off/enable", ""},
		{"create an admin", "/api/admin/users", `{"username":"eve","role":"admin"}`},
		{"create a person", "/api/admin/users", `{"username":"eve","role":"user"}`},
	}
	for how, revoke := range revocations {
		srv := api.NewServer(cfg, revokingStore{Store: st, users: revokeBeforeWrite{UserStore: st.Users(), revoke: func() {
			if err := revoke(); err != nil {
				t.Fatal(err)
			}
		}}})
		for _, rq := range requests {
			if err := st.Users().SetRole(ctx, store.System, bob.ID, "admin"); err != nil {
				t.Fatal(err)
			}
			bobCookie := sessionFor(t, st, bob)
			w := call(t, srv, "POST", rq.path, rq.body, bobCookie)
			if w.Code != http.StatusForbidden || codeOf(t, w.Body.Bytes()) != "actor_revoked" {
				t.Errorf("%s, %s: %d %s, want 403 actor_revoked", how, rq.name, w.Code, w.Body.String())
			}
			for id, want := range map[string]string{"usr_root": "admin/active", "usr_ann": "user/active", "usr_off": "user/inactive"} {
				if u, _ := st.Users().GetUserByID(ctx, id); u.Role+"/"+u.Status != want {
					t.Errorf("%s, %s: %s is %s/%s, want %s", how, rq.name, id, u.Role, u.Status, want)
				}
			}
			if strings.Contains(w.Body.String(), "temporary_password") {
				t.Errorf("%s, %s: the body carries a temporary password", how, rq.name)
			}
			if u, _ := st.Users().GetUserByID(ctx, "usr_ann"); u.PasswordHash != "h_ann" || u.MustChangePassword {
				t.Errorf("%s, %s: ann's password changed", how, rq.name)
			}
			if _, err := st.Users().GetUserByUsername(ctx, "eve"); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("%s, %s: eve was created", how, rq.name)
			}
			_ = st.Sessions().DeleteUserSessions(ctx, bob.ID)
		}
	}
}

// Nobody changes their own role or status, promotion and no-ops included: a demoted admin's
// in-flight request must not be able to restore them.
func TestAdminCannotChangeTheirOwnAccess(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	admin := loginAs(t, srv, st, "root", "admin")
	for _, tc := range []struct{ path, body string }{
		{"/api/admin/users/usr_root/role", `{"role":"admin"}`},
		{"/api/admin/users/usr_root/role", `{"role":"user"}`},
		{"/api/admin/users/usr_root/enable", ""},
		{"/api/admin/users/usr_root/disable", ""},
	} {
		if w := call(t, srv, "POST", tc.path, tc.body, admin); w.Code != http.StatusConflict || codeOf(t, w.Body.Bytes()) != "self" {
			t.Errorf("%s %s: %d %s, want 409 self", tc.path, tc.body, w.Code, w.Body.String())
		}
	}
}
