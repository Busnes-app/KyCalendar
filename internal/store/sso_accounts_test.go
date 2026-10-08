package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

func TestBindSignInDisablesSSOAccounts(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	ky := &store.User{ID: "usr_ky", Username: "ky", Role: "admin", Status: "active", SSOProvider: "kysignon", SSOSubject: "s1"}
	sc := &store.User{ID: "usr_sc", Username: "sc", Role: "user", Status: "active", SSOProvider: "scim", SSOSubject: "s2"}
	oi := &store.User{ID: "usr_oi", Username: "oi", Role: "user", Status: "active", SSOProvider: "oidc", SSOSubject: "s3"}
	lo := &store.User{ID: "usr_lo", Username: "lo", Role: "admin", Status: "active", SSOProvider: "local"}
	off := &store.User{ID: "usr_off", Username: "off", Role: "user", Status: "inactive", SSOProvider: "kysignon", SSOSubject: "s4"}
	seedUsers(t, st, ky, sc, oi, lo, off)
	for _, u := range []*store.User{ky, sc, oi, lo} {
		seedSession(t, st, u)
	}
	// An inactive account cannot get a session, but a leftover app password must still go.
	if err := st.AppPasswords().Create(ctx, store.Seed, &store.AppPassword{ID: "ap_" + off.ID, UserID: off.ID, Label: "phone", Hash: "h"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.Calendars().CreateCalendar(ctx, &store.Calendar{ID: "cal_ky", OwnerKind: "user", OwnerID: ky.ID, Slug: "default", Name: "Calendar"}, 0); err != nil {
		t.Fatal(err)
	}
	providers := []string{"kysignon", "scim"}
	if n, err := st.Users().CountSSOAccounts(ctx, providers); err != nil || n != 2 {
		t.Fatalf("count: %d %v, want 2", n, err)
	}
	if n, err := st.Users().BindSignIn(ctx, store.System, store.SignInBinding{Disable: providers}, nil); err != nil || n != 2 {
		t.Fatalf("disable: %d %v, want 2", n, err)
	}
	for _, u := range []*store.User{ky, sc, oi, lo, off} {
		got, _ := st.Users().GetUserByID(ctx, u.ID)
		_, sessErr := st.Sessions().GetSession(ctx, "tok_"+u.ID)
		aps, _ := st.AppPasswords().ListByUser(ctx, u.ID)
		hit := u == ky || u == sc || u == off
		if (got.Status == "inactive") != hit || (u != off && errors.Is(sessErr, store.ErrNotFound) != hit) || (len(aps) == 0) != hit {
			t.Errorf("%s: status %s, session %v, %d app passwords; disabled want %v", u.ID, got.Status, sessErr, len(aps), hit)
		}
	}
	if _, err := st.Calendars().GetCalendarByID(ctx, "cal_ky"); err != nil {
		t.Errorf("calendar did not survive: %v", err)
	}
	if n, err := st.Users().BindSignIn(ctx, store.System, store.SignInBinding{Disable: providers}, nil); err != nil || n != 0 {
		t.Fatalf("second run: %d %v, want 0", n, err)
	}
	if n, err := st.Users().BindSignIn(ctx, store.System, store.SignInBinding{}, nil); err != nil || n != 0 {
		t.Fatalf("empty list: %d %v, want 0", n, err)
	}
}

// Local accounts are never SSO accounts, whatever list the caller passes.
func TestBindSignInNeverTouchesLocal(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	lo := &store.User{ID: "usr_lo", Username: "lo", Role: "admin", Status: "active", SSOProvider: "local"}
	seedUsers(t, st, lo)
	seedSession(t, st, lo)
	local := []string{"local"}
	if n, err := st.Users().CountSSOAccounts(ctx, local); err != nil || n != 0 {
		t.Fatalf("count local: %d %v, want 0", n, err)
	}
	if n, err := st.Users().BindSignIn(ctx, store.System, store.SignInBinding{Disable: local}, nil); err != nil || n != 0 {
		t.Fatalf("disable local: %d %v, want 0", n, err)
	}
	got, _ := st.Users().GetUserByID(ctx, lo.ID)
	if _, err := st.Sessions().GetSession(ctx, "tok_"+lo.ID); got.Status != "active" || err != nil {
		t.Fatalf("local account: status %s, session %v; want untouched", got.Status, err)
	}
}

// The binding and the settings are one write: settings land with it, and on Postgres an invalid
// value rolls back the disable too.
func TestBindSignInIsOneWrite(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	ky := &store.User{ID: "usr_ky", Username: "ky", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "s1"}
	seedUsers(t, st, ky)
	if err := st.Settings().SetSetting(ctx, "a", "1"); err != nil {
		t.Fatal(err)
	}
	if testdb.Config(t).Driver == "postgres" {
		if _, err := st.Users().BindSignIn(ctx, store.System, store.SignInBinding{Disable: []string{"kysignon"}}, map[string]string{"b": "\xff"}); err == nil {
			t.Fatal("invalid UTF-8 was stored")
		}
		if u, _ := st.Users().GetUserByID(ctx, ky.ID); u.Status != "active" {
			t.Fatal("a failed bind kept the disable")
		}
	}
	n, err := st.Users().BindSignIn(ctx, store.System, store.SignInBinding{Disable: []string{"kysignon"}}, map[string]string{"a": "", "b": "2", "never": ""})
	if err != nil || n != 1 {
		t.Fatalf("bind: %d %v", n, err)
	}
	all, err := st.Settings().GetAllSettings(ctx)
	if err != nil || len(all) != 1 || all["b"] != "2" {
		t.Fatalf("settings %v %v, want only b=2", all, err)
	}
}

// SetSSORole writes only the role, and only on an active SSO account.
func TestSetSSORoleTouchesOnlyActiveSSORoles(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	ky := &store.User{ID: "usr_ky", Username: "ky", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "s1"}
	off := &store.User{ID: "usr_off", Username: "off", Role: "user", Status: "inactive", SSOProvider: "kysignon", SSOSubject: "s2"}
	lo := &store.User{ID: "usr_lo", Username: "lo", Role: "user", Status: "active", SSOProvider: "local"}
	seedUsers(t, st, ky, off, lo)
	if err := st.Users().SetSSORole(ctx, ky.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{off.ID, lo.ID, "usr_missing"} {
		if err := st.Users().SetSSORole(ctx, id, "admin"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s: %v, want ErrNotFound", id, err)
		}
	}
	for id, want := range map[string]string{ky.ID: "admin active", off.ID: "user inactive", lo.ID: "user active"} {
		if got, _ := st.Users().GetUserByID(ctx, id); got.Role+" "+got.Status != want {
			t.Errorf("%s: %s %s, want %s", id, got.Role, got.Status, want)
		}
	}
}

// The directory webhook's writes only take access away: neither sets a row active, and neither
// reaches a local account; the profile write reaches only kysignon rows.
func TestWebhookWritesNeverActivate(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	off := &store.User{ID: "usr_off", Username: "off", Role: "user", Status: "inactive", SSOProvider: "kysignon", SSOSubject: "s1"}
	on := &store.User{ID: "usr_on", Username: "on", Role: "admin", Status: "active", SSOProvider: "kysignon", SSOSubject: "s2"}
	sc := &store.User{ID: "usr_sc", Username: "sc", Role: "user", Status: "active", SSOProvider: "scim", SSOSubject: "s3"}
	lo := &store.User{ID: "usr_lo", Username: "lo", Role: "admin", Status: "active", SSOProvider: "local"}
	seedUsers(t, st, off, on, sc, lo)
	for _, u := range []*store.User{on, sc, lo} {
		seedSession(t, st, u)
	}
	for _, deactivate := range []bool{false, true} {
		if err := st.Users().RevokeSSOUser(ctx, off.ID, deactivate); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Users().UpdateKySignOnProfile(ctx, off.ID, "Off", "off@example"); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Users().GetUserByID(ctx, off.ID); got.Status != "inactive" || got.DisplayName != "Off" {
		t.Fatalf("inactive row: %+v", got)
	}
	if err := st.Users().RevokeSSOUser(ctx, on.ID, false); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Users().GetUserByID(ctx, on.ID)
	if _, err := st.Sessions().GetSession(ctx, "tok_"+on.ID); !errors.Is(err, store.ErrNotFound) || got.Status != "active" || got.Role != "admin" {
		t.Fatalf("revoke without deactivate: %+v session %v", got, err)
	}
	if err := st.Users().RevokeSSOUser(ctx, sc.ID, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Users().GetUserByID(ctx, sc.ID); got.Status != "inactive" {
		t.Fatalf("scim row not deactivated: %+v", got)
	}
	if err := st.Users().RevokeSSOUser(ctx, lo.ID, true); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("local revoke: %v, want ErrNotFound", err)
	}
	for _, id := range []string{sc.ID, lo.ID, "usr_missing"} {
		if err := st.Users().UpdateKySignOnProfile(ctx, id, "X", "x@example"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("profile write to %s: %v, want ErrNotFound", id, err)
		}
	}
	if got, _ := st.Users().GetUserByID(ctx, lo.ID); got.Status != "active" || got.DisplayName != "" {
		t.Fatalf("local account touched: %+v", got)
	}
	if _, err := st.Sessions().GetSession(ctx, "tok_"+lo.ID); err != nil {
		t.Fatalf("local session revoked: %v", err)
	}
}

// SetSSORole and UpdateSCIMUser revoke with the write, row first: UpdateSCIMUser only when role
// or status changed. A removal of access lands even if the profile write fails; a grant of
// access with a failing profile write changes nothing.
func TestSSOAccessWritesRevokeInTheSameTransaction(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	eve := &store.User{ID: "usr_eve", Username: "eve", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "e1"}
	dan := &store.User{ID: "usr_dan", Username: "dan", Role: "user", Status: "active", SSOProvider: "scim", SSOSubject: "d1"}
	loc := &store.User{ID: "usr_loc", Username: "loc", Role: "user", Status: "active", SSOProvider: "local"}
	seedUsers(t, st, eve, dan, loc)
	for _, u := range []*store.User{eve, dan, loc} {
		seedSession(t, st, u)
	}
	grants := func(id string) int {
		list, _ := st.AppPasswords().ListByUser(ctx, id)
		_, err := st.Sessions().GetSession(ctx, "tok_"+id)
		if err == nil {
			return len(list) + 1
		}
		return len(list)
	}

	if err := st.Users().SetSSORole(ctx, eve.ID, "admin"); err != nil || grants(eve.ID) != 0 {
		t.Fatalf("SetSSORole: %v, %d grants left", err, grants(eve.ID))
	}
	if err := st.Users().SetSSORole(ctx, loc.ID, "admin"); !errors.Is(err, store.ErrNotFound) || grants(loc.ID) != 2 {
		t.Fatalf("SetSSORole on a local account: %v, %d grants", err, grants(loc.ID))
	}

	// A profile-only change keeps the grants.
	edited := *dan
	edited.DisplayName = "Dan D"
	if err := st.Users().UpdateSCIMUser(ctx, &edited); err != nil || grants(dan.ID) != 2 {
		t.Fatalf("profile change: %v, %d grants", err, grants(dan.ID))
	}
	// A refused write (an exact duplicate username) changes nothing, the access change included.
	clash := edited
	clash.Username, clash.Role = "eve", "admin"
	if err := st.Users().UpdateSCIMUser(ctx, &clash); !errors.Is(err, store.ErrAlreadyExists) {
		t.Fatalf("duplicate username: %v", err)
	}
	if u, _ := st.Users().GetUserByID(ctx, dan.ID); u.Role != "user" || u.Username != "dan" || grants(dan.ID) != 2 {
		t.Fatalf("a refused write changed %+v, %d grants", u, grants(dan.ID))
	}
	// A deactivation fails closed: it lands with its revocation even when the profile write fails.
	off := edited
	off.Status, off.Username = "inactive", "eve"
	if err := st.Users().UpdateSCIMUser(ctx, &off); !errors.Is(err, store.ErrAlreadyExists) || grants(dan.ID) != 0 {
		t.Fatalf("deactivation with a clashing name: %v, %d grants left", err, grants(dan.ID))
	}
	if u, _ := st.Users().GetUserByID(ctx, dan.ID); u.Status != "inactive" || u.Username != "dan" {
		t.Fatalf("after the failed profile write: %+v", u)
	}
	off.Username = "dan"
	if err := st.Users().UpdateSCIMUser(ctx, &off); err != nil {
		t.Fatalf("deactivation: %v", err)
	}
	if u, _ := st.Users().GetUserByID(ctx, dan.ID); u.Status != "inactive" || u.DisplayName != "Dan D" || u.SSOProvider != "scim" {
		t.Fatalf("after deactivation: %+v", u)
	}
	localEdit := *loc
	localEdit.Role = "admin"
	if err := st.Users().UpdateSCIMUser(ctx, &localEdit); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("UpdateSCIMUser on a local account: %v", err)
	}
	if u, _ := st.Users().GetUserByID(ctx, loc.ID); u.Role != "user" {
		t.Fatalf("a local account changed: %+v", u)
	}
}
