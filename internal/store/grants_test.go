package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

// grantWorld has users a and b, groups Readers (a) and Editors (a, b), a group calendar and
// a's personal calendar.
func grantWorld(t *testing.T) store.Store {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for _, id := range []string{"usr_a", "usr_b"} {
		if err := st.Users().CreateUser(ctx, &store.User{ID: id, Username: id, Role: "user", Status: "active", SSOProvider: "local"}); err != nil {
			t.Fatal(err)
		}
	}
	for id, name := range map[string]string{"grp_r": "Readers", "grp_e": "Editors"} {
		if err := st.Groups().CreateGroup(ctx, &store.Group{ID: id, DisplayName: name}); err != nil {
			t.Fatal(err)
		}
	}
	for _, m := range [][2]string{{"grp_r", "usr_a"}, {"grp_e", "usr_a"}, {"grp_e", "usr_b"}} {
		if err := st.Groups().AddGroupMember(ctx, m[0], m[1]); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []*store.Calendar{
		{ID: "cal_g", OwnerKind: "group", OwnerID: "cal_g", Slug: "group", Name: "Team"},
		{ID: "cal_p", OwnerKind: "user", OwnerID: "usr_a", Slug: "default", Name: "Calendar"},
	} {
		if err := st.Calendars().CreateCalendar(ctx, c, 0); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func TestSetGrantUpsertsAndLists(t *testing.T) {
	ctx := context.Background()
	cs := grantWorld(t).Calendars()
	for _, role := range []string{"reader", "editor"} {
		if err := cs.SetGrant(ctx, store.CalendarGrant{CalendarID: "cal_g", GroupID: "grp_r", Role: role}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := cs.ListGrants(ctx, "cal_g")
	if err != nil || len(got) != 1 || got[0].Role != "editor" || got[0].GroupName != "Readers" {
		t.Fatalf("grants %+v %v", got, err)
	}
}

func TestSetGrantRefusesPersonalOrMissing(t *testing.T) {
	ctx := context.Background()
	cs := grantWorld(t).Calendars()
	for name, g := range map[string]store.CalendarGrant{
		"personal calendar": {CalendarID: "cal_p", GroupID: "grp_r", Role: "reader"},
		"missing group":     {CalendarID: "cal_g", GroupID: "grp_x", Role: "reader"},
		"missing calendar":  {CalendarID: "cal_x", GroupID: "grp_r", Role: "reader"},
	} {
		if err := cs.SetGrant(ctx, g); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s: want ErrNotFound, got %v", name, err)
		}
	}
}

func TestUserGrantsFollowMembership(t *testing.T) {
	ctx := context.Background()
	st := grantWorld(t)
	cs := st.Calendars()
	_ = cs.SetGrant(ctx, store.CalendarGrant{CalendarID: "cal_g", GroupID: "grp_r", Role: "reader"})
	_ = cs.SetGrant(ctx, store.CalendarGrant{CalendarID: "cal_g", GroupID: "grp_e", Role: "editor"})
	if got, err := cs.UserGrants(ctx, "usr_a"); err != nil || len(got) != 2 {
		t.Fatalf("usr_a grants %+v %v", got, err)
	}
	if err := st.Groups().RemoveGroupMember(ctx, "grp_e", "usr_a"); err != nil {
		t.Fatal(err)
	}
	if got, _ := cs.UserGrants(ctx, "usr_a"); len(got) != 1 || got[0].Role != "reader" {
		t.Fatalf("after leaving Editors: %+v", got)
	}
}

func TestDeleteGroupRemovesGrantsKeepsCalendar(t *testing.T) {
	ctx := context.Background()
	st := grantWorld(t)
	cs := st.Calendars()
	_ = cs.SetGrant(ctx, store.CalendarGrant{CalendarID: "cal_g", GroupID: "grp_r", Role: "reader"})
	if err := st.Groups().DeleteGroup(ctx, "grp_r"); err != nil {
		t.Fatal(err)
	}
	if got, _ := cs.ListGrants(ctx, "cal_g"); len(got) != 0 {
		t.Fatalf("grants survived the group: %+v", got)
	}
	if _, err := cs.GetCalendarByID(ctx, "cal_g"); err != nil {
		t.Fatalf("the calendar did not survive the group: %v", err)
	}
}

func TestDeleteCalendarCascades(t *testing.T) {
	ctx := context.Background()
	cs := grantWorld(t).Calendars()
	_ = cs.SetGrant(ctx, store.CalendarGrant{CalendarID: "cal_g", GroupID: "grp_r", Role: "reader"})
	if _, err := cs.PutObject(ctx, obj("cal_g", "a.ics", "a", "x", 0, nil), "", false, store.OwnerLimits{}); err != nil {
		t.Fatal(err)
	}
	if err := cs.DeleteCalendar(ctx, "cal_g"); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.GetObject(ctx, "cal_g", "a.ics"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("object survived: %v", err)
	}
	if got, _ := cs.ListGrants(ctx, "cal_g"); len(got) != 0 {
		t.Fatalf("grants survived: %+v", got)
	}
	if err := cs.DeleteCalendar(ctx, "cal_g"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second delete: want ErrNotFound, got %v", err)
	}
}

func TestDeleteGrantIsIdempotent(t *testing.T) {
	ctx := context.Background()
	cs := grantWorld(t).Calendars()
	for i := 0; i < 2; i++ {
		if err := cs.DeleteGrant(ctx, "cal_g", "grp_r"); err != nil {
			t.Fatalf("delete %d: %v", i, err)
		}
	}
}

func TestListCalendarsByKind(t *testing.T) {
	cs := grantWorld(t).Calendars()
	got, err := cs.ListCalendarsByKind(context.Background(), "group")
	if err != nil || len(got) != 1 || got[0].ID != "cal_g" {
		t.Fatalf("group calendars %+v %v", got, err)
	}
}
