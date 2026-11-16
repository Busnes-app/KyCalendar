package store_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

const suiteBinding = "kyidentity https://id.example.com"

var suiteScope = store.SCIMScope{Issuer: suiteBinding, Providers: []string{"kysignon", "scim"}}

// suiteWorld has everyday SSO accounts ann (kysignon) and bob (scim), an administrator, a
// disabled account, an account of another issuer and a local account, all with subjects.
func suiteWorld(t *testing.T) store.Store {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for _, u := range []*store.User{
		{ID: "usr_ann", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "sub-ann", SSOIssuer: suiteBinding},
		{ID: "usr_bob", Role: "user", Status: "active", SSOProvider: "scim", SSOSubject: "sub-bob", SSOIssuer: suiteBinding},
		{ID: "usr_adm", Role: "admin", Status: "active", SSOProvider: "kysignon", SSOSubject: "sub-adm", SSOIssuer: suiteBinding},
		{ID: "usr_off", Role: "user", Status: "disabled", SSOProvider: "kysignon", SSOSubject: "sub-off", SSOIssuer: suiteBinding},
		{ID: "usr_old", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "sub-old", SSOIssuer: "kyidentity https://old.example.com"},
		{ID: "usr_loc", Role: "user", Status: "active", SSOProvider: "local", SSOSubject: "sub-loc"},
	} {
		u.Username = u.ID
		if err := st.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func reconcile(t *testing.T, st store.Store, g store.SuiteGroup) store.SuiteGroupChange {
	t.Helper()
	ch, err := st.Groups().ReconcileSuiteGroup(context.Background(), suiteScope, g)
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

// suiteCalendar is suite group id's local group and calendar; nil calendar when there is none.
func suiteCalendar(t *testing.T, st store.Store, id string) (*store.Group, *store.Calendar) {
	t.Helper()
	ctx := context.Background()
	g, err := st.Groups().GetGroupByName(ctx, store.SuiteGroupDisplayName(id))
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(g.Members)
	cals, err := st.Calendars().ListCalendarsByKind(ctx, "group")
	if err != nil {
		t.Fatal(err)
	}
	var found *store.Calendar
	for _, c := range cals {
		gs, err := st.Calendars().ListGrants(ctx, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, gr := range gs {
			if gr.GroupID == g.ID {
				if found != nil {
					t.Fatalf("two calendars for suite group %s", id)
				}
				if gr.Role != "editor" {
					t.Fatalf("grant role %s", gr.Role)
				}
				found = c
			}
		}
	}
	return g, found
}

func groupCalendarCount(t *testing.T, st store.Store) int {
	t.Helper()
	cals, err := st.Calendars().ListCalendarsByKind(context.Background(), "group")
	if err != nil {
		t.Fatal(err)
	}
	return len(cals)
}

func TestSuiteGroupLifecycle(t *testing.T) {
	ctx := context.Background()
	st := suiteWorld(t)
	// Create: admin, disabled, other-issuer, local and unknown subjects are never members.
	ch := reconcile(t, st, store.SuiteGroup{ID: "kg1", Name: "Sales", Active: true, Subjects: []string{"sub-ann", "sub-adm", "sub-off", "sub-old", "sub-loc", "sub-unknown"}})
	if !ch.Created || ch.Added != 1 {
		t.Fatalf("create %+v", ch)
	}
	g, c := suiteCalendar(t, st, "kg1")
	if c == nil || c.Name != "Sales" || c.Archived || g.Source != store.GroupSourceSuite || g.ExternalID != "kg1" || !slices.Equal(g.Members, []string{"usr_ann"}) {
		t.Fatalf("created %+v %+v", g, c)
	}
	calID := c.ID
	// The same input again writes nothing.
	if ch := reconcile(t, st, store.SuiteGroup{ID: "kg1", Name: "Sales", Active: true, Subjects: []string{"sub-ann", "sub-adm", "sub-off", "sub-old", "sub-loc", "sub-unknown"}}); ch.Changed() {
		t.Fatalf("second run wrote %+v", ch)
	}
	// Rename and add a member.
	ch = reconcile(t, st, store.SuiteGroup{ID: "kg1", Name: "Sales EMEA", Active: true, Subjects: []string{"sub-ann", "sub-bob"}})
	if !ch.Renamed || ch.Added != 1 || ch.Removed != 0 || ch.Created {
		t.Fatalf("rename/add %+v", ch)
	}
	g, c = suiteCalendar(t, st, "kg1")
	if c.ID != calID || c.Name != "Sales EMEA" || !slices.Equal(g.Members, []string{"usr_ann", "usr_bob"}) {
		t.Fatalf("after rename %+v %+v", g, c)
	}
	// Remove a member.
	if ch := reconcile(t, st, store.SuiteGroup{ID: "kg1", Name: "Sales EMEA", Active: true, Subjects: []string{"sub-bob"}}); ch.Removed != 1 || ch.Added != 0 {
		t.Fatalf("remove %+v", ch)
	}
	if _, err := st.Calendars().PutObject(ctx, obj(calID, "a.ics", "a", "A", 1, i64(2)), "", false, store.OwnerLimits{}); err != nil {
		t.Fatal(err)
	}
	// Archive: read-only, no members, kept with its events.
	ch = reconcile(t, st, store.SuiteGroup{ID: "kg1", Name: "Sales EMEA"})
	if !ch.Archived || ch.Removed != 1 {
		t.Fatalf("archive %+v", ch)
	}
	if ch := reconcile(t, st, store.SuiteGroup{ID: "kg1", Name: "Sales EMEA"}); ch.Changed() {
		t.Fatalf("second archive wrote %+v", ch)
	}
	g, c = suiteCalendar(t, st, "kg1")
	if c == nil || c.ID != calID || !c.Archived || len(g.Members) != 0 {
		t.Fatalf("archived %+v %+v", g, c)
	}
	if _, err := st.Calendars().PutObject(ctx, obj(calID, "b.ics", "b", "B", 1, i64(2)), "", false, store.OwnerLimits{}); !errors.Is(err, store.ErrReadOnly) {
		t.Fatalf("archived put: %v, want ErrReadOnly", err)
	}
	if err := st.Calendars().DeleteObject(ctx, calID, "a.ics", ""); !errors.Is(err, store.ErrReadOnly) {
		t.Fatalf("archived delete: %v, want ErrReadOnly", err)
	}
	if objs, err := st.Calendars().ListObjects(ctx, calID); err != nil || len(objs) != 1 {
		t.Fatalf("archived objects %d %v", len(objs), err)
	}
	// Re-enable: the same calendar, writable, members back.
	ch = reconcile(t, st, store.SuiteGroup{ID: "kg1", Name: "Sales EMEA", Active: true, Subjects: []string{"sub-ann"}})
	if !ch.Unarchived || ch.Created || ch.Added != 1 {
		t.Fatalf("unarchive %+v", ch)
	}
	g, c = suiteCalendar(t, st, "kg1")
	if c.ID != calID || c.Archived || !slices.Equal(g.Members, []string{"usr_ann"}) {
		t.Fatalf("unarchived %+v %+v", g, c)
	}
	if _, err := st.Calendars().PutObject(ctx, obj(calID, "b.ics", "b", "B", 1, i64(2)), "", false, store.OwnerLimits{}); err != nil {
		t.Fatalf("unarchived put: %v", err)
	}
	// Deleted in KyIdentity (archived), then a namesake recreated with a new ID: a new calendar,
	// never the archived one.
	reconcile(t, st, store.SuiteGroup{ID: "kg1", Name: "Sales EMEA"})
	if ch := reconcile(t, st, store.SuiteGroup{ID: "kg2", Name: "Sales EMEA", Active: true, Subjects: []string{"sub-ann"}}); !ch.Created {
		t.Fatalf("namesake %+v", ch)
	}
	_, c2 := suiteCalendar(t, st, "kg2")
	_, c1 := suiteCalendar(t, st, "kg1")
	if c2 == nil || c2.ID == calID || c2.Archived || !c1.Archived || c1.ID != calID {
		t.Fatalf("namesake %+v, old %+v", c2, c1)
	}
}

func TestSuiteGroupArchivedWithoutCalendarDoesNothing(t *testing.T) {
	st := suiteWorld(t)
	if ch := reconcile(t, st, store.SuiteGroup{ID: "kg9", Name: "Never on"}); ch.Changed() {
		t.Fatalf("wrote %+v", ch)
	}
	if n := groupCalendarCount(t, st); n != 0 {
		t.Fatalf("%d calendars", n)
	}
}

// A state no single reconcile leaves (each runs in one transaction), such as members written
// beside the reconcile or an administrator's edits, heals without duplicates.
func TestSuiteGroupHealsDrift(t *testing.T) {
	ctx := context.Background()
	st := suiteWorld(t)
	want := store.SuiteGroup{ID: "kg1", Name: "Ops", Active: true, Subjects: []string{"sub-ann"}}
	reconcile(t, st, want)
	g, c := suiteCalendar(t, st, "kg1")
	// A stray member and a removed grant.
	if err := st.Groups().AddGroupMember(ctx, g.ID, "usr_bob"); err != nil {
		t.Fatal(err)
	}
	if err := st.Calendars().DeleteGrant(ctx, c.ID, g.ID); err != nil {
		t.Fatal(err)
	}
	if ch := reconcile(t, st, want); ch.Removed != 1 || !ch.GrantRestored || ch.Created {
		t.Fatalf("heal %+v", ch)
	}
	// An administrator raises the grant to manager: the next reconcile puts it back.
	if err := st.Calendars().SetGrant(ctx, store.CalendarGrant{CalendarID: c.ID, GroupID: g.ID, Role: "manager"}); err != nil {
		t.Fatal(err)
	}
	if ch := reconcile(t, st, want); !ch.GrantRestored || ch.Added != 0 || ch.Removed != 0 {
		t.Fatalf("manager heal %+v", ch)
	}
	if ch := reconcile(t, st, want); ch.Changed() {
		t.Fatalf("after manager heal wrote %+v", ch)
	}
	g, c2 := suiteCalendar(t, st, "kg1")
	if c2 == nil || c2.ID != c.ID || !slices.Equal(g.Members, []string{"usr_ann"}) {
		t.Fatalf("healed %+v %+v", g, c2)
	}
	// Archived but a member left behind: archiving again removes it.
	reconcile(t, st, store.SuiteGroup{ID: "kg1", Name: "Ops"})
	if err := st.Groups().AddGroupMember(ctx, g.ID, "usr_ann"); err != nil {
		t.Fatal(err)
	}
	if ch := reconcile(t, st, store.SuiteGroup{ID: "kg1", Name: "Ops"}); ch.Removed != 1 || ch.Archived {
		t.Fatalf("archived heal %+v", ch)
	}
	// An administrator deletes the calendar: a still-active group gets a new one on the same
	// local group, never a second group.
	reconcile(t, st, want)
	if err := st.Calendars().DeleteCalendar(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if ch := reconcile(t, st, want); !ch.Created {
		t.Fatalf("recreate %+v", ch)
	}
	g2, c3 := suiteCalendar(t, st, "kg1")
	if c3 == nil || c3.ID == c.ID || g2.ID != g.ID || groupCalendarCount(t, st) != 1 {
		t.Fatalf("recreated %+v %+v", g2, c3)
	}
	if ch := reconcile(t, st, want); ch.Changed() {
		t.Fatalf("after recreate wrote %+v", ch)
	}
}

// The suite group's local group is not an administrator's to edit, and SCIM never sees it.
func TestSuiteGroupIsNotLocal(t *testing.T) {
	ctx := context.Background()
	st := suiteWorld(t)
	reconcile(t, st, store.SuiteGroup{ID: "kg1", Name: "Ops", Active: true})
	scim, _, err := st.Groups().ListGroups(ctx, 0, 10, store.GroupSourceSCIM)
	if err != nil || len(scim) != 0 {
		t.Fatalf("scim groups %v %v", scim, err)
	}
	local, _, err := st.Groups().ListGroups(ctx, 0, 10, store.GroupSourceLocal)
	if err != nil || len(local) != 0 {
		t.Fatalf("local groups %v %v", local, err)
	}
}
