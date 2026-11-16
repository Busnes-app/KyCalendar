package tasks

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
)

// unfold returns the content lines of an iCalendar text, folding undone (RFC 5545 §3.1).
func unfold(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\n ", "")
	s = strings.ReplaceAll(s, "\n\t", "")
	return strings.Split(strings.TrimSpace(s), "\n")
}

// canonical sorts a line's parameters: RFC 5545 §3.2 gives their order no meaning, and the
// encoder writes them sorted. Values are compared byte for byte.
func canonical(line string) string {
	head, value, _ := strings.Cut(line, ":")
	// a quoted parameter may hold ':'; such lines are compared whole
	if strings.Count(head, "\"")%2 == 1 {
		return line
	}
	parts := strings.Split(head, ";")
	slices.Sort(parts[1:])
	return strings.Join(parts, ";") + ":" + value
}

func propName(line string) string {
	end := strings.IndexAny(line, ";:")
	if end < 0 {
		return line
	}
	return strings.ToUpper(line[:end])
}

// TestInteropFixturesSurviveWebEdits loads VTODOs as Tasks.org, Apple Reminders and Thunderbird
// write them (testdata/SOURCES.md), applies every web edit, and checks that every content line
// the edit does not own is still there byte for byte, VALARMs and VTIMEZONEs included.
func TestInteropFixturesSurviveWebEdits(t *testing.T) {
	files, _ := filepath.Glob("testdata/*.ics")
	if len(files) < 9 {
		t.Fatalf("fixtures missing: %v", files)
	}
	berlin, _ := time.LoadLocation("Europe/Berlin")
	now := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	edits := []struct {
		name  string
		patch Patch
		owns  []string
	}{
		{"title", Patch{Title: ptr("Renamed")}, []string{"SUMMARY"}},
		{"notes", Patch{Notes: ptr("New notes")}, []string{"DESCRIPTION"}},
		{"priority", Patch{Priority: ptr(1)}, []string{"PRIORITY"}},
		{"tags", Patch{Tags: &[]string{"Home", "Later"}}, []string{"CATEGORIES"}},
		{"due", Patch{Due: &Moment{T: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), AllDay: true}}, []string{"DUE"}},
		{"start", Patch{Start: &Moment{T: time.Date(2026, 11, 20, 9, 0, 0, 0, berlin)}, Zone: berlin}, []string{"DTSTART"}},
		{"in progress", Patch{Status: ptr(InProcess)}, []string{"STATUS", "COMPLETED", "PERCENT-COMPLETE"}},
		{"complete", Patch{Status: ptr(Completed)}, []string{"STATUS", "COMPLETED", "PERCENT-COMPLETE", "DUE", "DTSTART", "RRULE"}},
		{"estimate", Patch{Estimate: ptr(45 * time.Minute)}, []string{PropEstimate}},
		{"parent", Patch{Parent: ptr("parent-uid")}, []string{"RELATED-TO"}},
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range edits {
			t.Run(filepath.Base(f)+"/"+e.name, func(t *testing.T) {
				cal, err := ical.NewDecoder(strings.NewReader(strings.ReplaceAll(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n", "\r\n"))).Decode()
				if err != nil {
					t.Fatal(err)
				}
				if _, err := Apply(cal, e.patch, now, berlin); err != nil {
					t.Fatal(err)
				}
				var got []string
				for _, l := range unfold(encode(t, cal)) {
					got = append(got, canonical(l))
				}
				owned := append([]string{"DTSTAMP", "LAST-MODIFIED", "SEQUENCE", "BEGIN", "END"}, e.owns...)
				if e.patch.Start != nil {
					owned = append(owned, "TZID", "TZOFFSETFROM", "TZOFFSETTO", "TZNAME", "RRULE", "DTSTART")
				}
				for _, line := range unfold(string(raw)) {
					if slices.Contains(owned, propName(line)) {
						continue
					}
					if !slices.Contains(got, canonical(line)) {
						t.Errorf("lost or changed %q", line)
					}
				}
				if _, err := Read(cal, berlin); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
