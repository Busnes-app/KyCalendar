package access_test

import (
	"testing"

	"github.com/Busnes-app/kycalendar/internal/access"
	"github.com/Busnes-app/kycalendar/internal/store"
)

func TestResolve(t *testing.T) {
	alice := &store.User{ID: "usr_a", Role: "user"}
	root := &store.User{ID: "usr_root", Role: "admin"}
	personal := &store.Calendar{ID: "cal_p", OwnerKind: "user", OwnerID: "usr_a"}
	rootsOwn := &store.Calendar{ID: "cal_r", OwnerKind: "user", OwnerID: "usr_root"}
	group := &store.Calendar{ID: "cal_g", OwnerKind: "group", OwnerID: "cal_g"}
	grant := func(cal, role string) store.CalendarGrant { return store.CalendarGrant{CalendarID: cal, Role: role} }

	cases := []struct {
		name   string
		user   *store.User
		cal    *store.Calendar
		grants []store.CalendarGrant
		want   access.Role
	}{
		{"owner of a personal calendar", alice, personal, nil, access.Owner},
		{"someone else's personal calendar", &store.User{ID: "usr_b", Role: "user"}, personal, nil, access.None},
		{"grants never reach a personal calendar", &store.User{ID: "usr_b", Role: "user"}, personal, []store.CalendarGrant{grant("cal_p", "manager")}, access.None},
		{"admin owns nothing, even their own rows", root, rootsOwn, nil, access.None},
		{"admin with a grant still reads nothing", root, group, []store.CalendarGrant{grant("cal_g", "manager")}, access.None},
		{"group without grants", alice, group, nil, access.None},
		{"highest role wins", alice, group, []store.CalendarGrant{grant("cal_g", "reader"), grant("cal_g", "editor")}, access.Editor},
		{"grant on another calendar", alice, group, []store.CalendarGrant{grant("cal_x", "manager")}, access.None},
		{"unknown role string is ignored", alice, group, []store.CalendarGrant{grant("cal_g", "owner"), grant("cal_g", "reader")}, access.Reader},
		{"unknown owner kind", alice, &store.Calendar{ID: "cal_g", OwnerKind: "org"}, []store.CalendarGrant{grant("cal_g", "manager")}, access.None},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := access.Resolve(tc.user, tc.cal, tc.grants); got != tc.want {
				t.Fatalf("Resolve = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRoleCapabilities(t *testing.T) {
	cases := []struct {
		role                access.Role
		read, write, manage bool
	}{
		{access.None, false, false, false},
		{access.Reader, true, false, false},
		{access.Editor, true, true, false},
		{access.Manager, true, true, true},
		{access.Owner, true, true, true},
	}
	for _, tc := range cases {
		if tc.role.CanRead() != tc.read || tc.role.CanWrite() != tc.write || tc.role.CanManage() != tc.manage {
			t.Errorf("role %v: read %v write %v manage %v", tc.role, tc.role.CanRead(), tc.role.CanWrite(), tc.role.CanManage())
		}
	}
	for _, s := range []string{"", "owner", "none", "Reader"} {
		if _, ok := access.ParseRole(s); ok {
			t.Errorf("ParseRole(%q) accepted", s)
		}
	}
}
