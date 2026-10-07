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
	if err := st.AppPasswords().Create(ctx, &store.AppPassword{ID: "ap_" + off.ID, UserID: off.ID, Label: "phone", Hash: "h"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Calendars().CreateCalendar(ctx, &store.Calendar{ID: "cal_ky", OwnerKind: "user", OwnerID: ky.ID, Slug: "default", Name: "Calendar"}, 0); err != nil {
		t.Fatal(err)
	}
	providers := []string{"kysignon", "scim"}
	if n, err := st.Users().CountSSOAccounts(ctx, providers); err != nil || n != 2 {
		t.Fatalf("count: %d %v, want 2", n, err)
	}
	if n, err := st.Users().BindSignIn(ctx, store.System, providers, nil); err != nil || n != 2 {
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
	if n, err := st.Users().BindSignIn(ctx, store.System, providers, nil); err != nil || n != 0 {
		t.Fatalf("second run: %d %v, want 0", n, err)
	}
	if n, err := st.Users().BindSignIn(ctx, store.System, nil, nil); err != nil || n != 0 {
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
	if n, err := st.Users().BindSignIn(ctx, store.System, local, nil); err != nil || n != 0 {
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
		if _, err := st.Users().BindSignIn(ctx, store.System, []string{"kysignon"}, map[string]string{"b": "\xff"}); err == nil {
			t.Fatal("invalid UTF-8 was stored")
		}
		if u, _ := st.Users().GetUserByID(ctx, ky.ID); u.Status != "active" {
			t.Fatal("a failed bind kept the disable")
		}
	}
	n, err := st.Users().BindSignIn(ctx, store.System, []string{"kysignon"}, map[string]string{"a": "", "b": "2", "never": ""})
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
