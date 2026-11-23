package main

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"

	"github.com/Busnes-app/kycalendar/internal/davbackend"
	"github.com/Busnes-app/kycalendar/internal/store"
)

func exportFixture(t *testing.T, st store.Store, calID, name, summary string) {
	t.Helper()
	exportFixtureZone(t, st, calID, name, summary, "Europe/Berlin", "+0100")
}

// exportFixtureZone stores an event in zone tzid, whose standard offset is offset.
func exportFixtureZone(t *testing.T, st store.Store, calID, name, summary, tzid, offset string) {
	t.Helper()
	ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nBEGIN:VTIMEZONE\r\nTZID:" + tzid + "\r\nBEGIN:STANDARD\r\n" +
		"DTSTART:19701025T030000\r\nTZOFFSETFROM:+0200\r\nTZOFFSETTO:" + offset + "\r\nEND:STANDARD\r\nEND:VTIMEZONE\r\nBEGIN:VEVENT\r\nUID:" + name +
		"\r\nDTSTAMP:20261101T000000Z\r\nDTSTART;TZID=" + tzid + ":20261110T090000\r\nDTEND;TZID=" + tzid + ":20261110T100000\r\nSUMMARY:" + summary +
		"\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	cal, err := ical.NewDecoder(strings.NewReader(ics)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	o, err := davbackend.Prepare(calID, name+".ics", cal, []byte(ics))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Calendars().PutObject(context.Background(), o, "", true, store.OwnerLimits{}); err != nil {
		t.Fatal(err)
	}
}

// export-org writes exactly one org's calendars, a file per calendar in a folder per member or
// for groups, with a manifest, private file modes and each time zone once. Another org's events
// never appear, an existing output directory is refused, and so is an unknown org.
func TestExportOrgWritesOnlyThatOrg(t *testing.T) {
	st := openBootstrapStore(t)
	ctx := context.Background()
	if err := st.ApplyOrgs(ctx, "", []store.MirroredOrg{{ID: "org-b", Status: "deletion_scheduled", Revision: 1, Quotas: `{}`}}, 1); err != nil {
		t.Fatal(err)
	}
	for _, u := range []*store.User{
		{ID: "usr_alice", Username: "alice@example.com", Email: "alice@example.com", Role: "user", Status: "active", SSOProvider: "local"},
		{ID: "usr_bert", Username: "bert", Email: "bert@example.com", Role: "user", Status: "active", SSOProvider: "local", OrgID: "org-b"},
	} {
		if err := st.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
		if err := davbackend.EnsureDefault(ctx, st, u.ID, 0); err != nil {
			t.Fatal(err)
		}
	}
	calOf := func(user string) string {
		c, err := st.Calendars().GetCalendarBySlug(ctx, "user", user, "default")
		if err != nil {
			t.Fatal(err)
		}
		return c.ID
	}
	exportFixture(t, st, calOf("usr_alice"), "a1", "Alice dentist")
	exportFixture(t, st, calOf("usr_alice"), "a2", "Alice gym")
	exportFixture(t, st, calOf("usr_bert"), "b1", "Bert secret")
	if err := st.Calendars().CreateCalendar(ctx, &store.Calendar{ID: "cal_team", OwnerKind: "group", OwnerID: "cal_team", Slug: "team", Name: "Team", Components: []string{"VEVENT"}}, 0); err != nil {
		t.Fatal(err)
	}
	exportFixture(t, st, "cal_team", "t1", "Team sync")
	// Two clients' different rules under one non-IANA TZID: neither may be read with the other's.
	exportFixtureZone(t, st, "cal_team", "t2", "Outlook one", "Customized Time Zone", "+0100")
	exportFixtureZone(t, st, "cal_team", "t3", "Outlook two", "Customized Time Zone", "-0500")
	// A third object reuses the renamed definition: it must point at the renamed zone too.
	exportFixtureZone(t, st, "cal_team", "t4", "Outlook three", "Customized Time Zone", "-0500")

	out := filepath.Join(t.TempDir(), "export")
	m, err := exportOrg(ctx, st, store.DefaultOrg, out, time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	var all strings.Builder
	files := map[string]fs.FileMode{}
	if err := filepath.WalkDir(out, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(out, p)
		files[filepath.ToSlash(rel)] = info.Mode().Perm()
		if !d.IsDir() {
			b, err := os.ReadFile(p)
			all.Write(b)
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for f, mode := range files {
		if want := fs.FileMode(0o600); strings.HasSuffix(f, ".ics") || strings.HasSuffix(f, ".json") {
			if mode != want {
				t.Errorf("%s mode %o", f, mode)
			}
		} else if mode != 0o700 {
			t.Errorf("folder %s mode %o", f, mode)
		}
	}
	alice := "members/usr_alice/" + calOf("usr_alice") + ".ics"
	if _, ok := files[alice]; !ok {
		t.Errorf("no %s in %v", alice, files)
	}
	if _, ok := files["groups/cal_team.ics"]; !ok {
		t.Errorf("no group calendar in %v", files)
	}
	text := all.String()
	for _, s := range []string{"SUMMARY:Alice dentist", "SUMMARY:Alice gym", "SUMMARY:Team sync"} {
		if !strings.Contains(text, s) {
			t.Errorf("export lacks %s", s)
		}
	}
	if strings.Contains(text, "Bert") {
		t.Error("another org's event is in the export")
	}
	b, _ := os.ReadFile(filepath.Join(out, filepath.FromSlash(alice)))
	if n := strings.Count(string(b), "BEGIN:VTIMEZONE"); n != 1 {
		t.Errorf("alice's calendar has %d VTIMEZONEs, want 1", n)
	}
	team, _ := os.ReadFile(filepath.Join(out, "groups", "cal_team.ics"))
	tz := string(team)
	if !strings.Contains(tz, "TZID:Customized Time Zone\r\n") || !strings.Contains(tz, "TZID:Customized Time Zone (2)\r\n") ||
		!strings.Contains(tz, "TZOFFSETTO:-0500") || !strings.Contains(tz, "DTSTART;TZID=Customized Time Zone (2):20261110T090000") {
		t.Errorf("colliding zones were not kept apart:\n%s", tz)
	}
	if n := strings.Count(tz, "DTSTART;TZID=Customized Time Zone (2):20261110T090000"); n != 2 {
		t.Errorf("%d events point at the renamed zone, want 2 (the object that renamed it and the one that reuses it):\n%s", n, tz)
	}
	if strings.Count(tz, "BEGIN:VTIMEZONE") != 3 {
		t.Errorf("team calendar has %d VTIMEZONEs, want 3 (Berlin and two distinct customized zones)", strings.Count(tz, "BEGIN:VTIMEZONE"))
	}
	var read exportManifest
	mb, _ := os.ReadFile(filepath.Join(out, "manifest.json"))
	if err := json.Unmarshal(mb, &read); err != nil || read.OrgID != store.DefaultOrg || read.Items != 6 || len(read.Calendars) != len(m.Calendars) {
		t.Errorf("manifest %+v %v", read, err)
	}
	for _, c := range read.Calendars {
		if c.OwnerID == "usr_alice" && (c.Email != "alice@example.com" || c.Items != 2 || c.File != alice) {
			t.Errorf("alice's entry %+v", c)
		}
	}

	if _, err := exportOrg(ctx, st, store.DefaultOrg, out, time.Now()); err == nil {
		t.Error("an existing output directory was written over")
	}
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.Mkdir(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := exportOrg(ctx, st, store.DefaultOrg, empty, time.Now()); err == nil {
		t.Error("an existing empty output directory was replaced")
	}
	if _, err := exportOrg(ctx, st, "org-none", filepath.Join(t.TempDir(), "x"), time.Now()); err == nil {
		t.Error("an unknown org was exported")
	}
	// A failed export leaves nothing behind, so the same -out works on the retry. An object
	// that decodes but cannot be written back (an old row with no UID line) fails the encode.
	if err := davbackend.EnsureDefault(ctx, st, "usr_bert", 0); err != nil {
		t.Fatal(err)
	}
	bad := &store.CalendarObject{CalendarID: calOf("usr_bert"), Name: "bad.ics", UID: "bad", Component: "VEVENT",
		Data: []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nBEGIN:VEVENT\r\nDTSTAMP:20261101T000000Z\r\nDTSTART:20261110T090000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")}
	if _, err := st.Calendars().PutObject(ctx, bad, "", true, store.OwnerLimits{}); err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	failed := filepath.Join(parent, "export")
	if _, err := exportOrg(ctx, st, "org-b", failed, time.Now()); err == nil {
		t.Fatal("an export of an object that cannot be encoded succeeded")
	}
	if left, _ := os.ReadDir(parent); len(left) != 0 {
		t.Errorf("a failed export left %v", left)
	}
	if err := st.Calendars().DeleteObject(ctx, bad.CalendarID, bad.Name, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := exportOrg(ctx, st, "org-b", failed, time.Now()); err != nil {
		t.Errorf("the retry with the same -out: %v", err)
	}
	outB := filepath.Join(t.TempDir(), "b")
	if mb, err := exportOrg(ctx, st, "org-b", outB, time.Now()); err != nil || mb.Items != 1 {
		t.Fatalf("org-b, deletion scheduled: %+v %v", mb, err)
	}
}
