package csvimport

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// Outlook's "Export to a file" CSV header, as classic Outlook writes it.
const outlookHeader = `"Subject","Start Date","Start Time","End Date","End Time","All day event","Reminder on/off","Reminder Date","Reminder Time","Meeting Organizer","Required Attendees","Optional Attendees","Meeting Resources","Billing Information","Categories","Description","Location","Mileage","Priority","Private","Sensitivity","Show time as"`

// Google Calendar's documented import header.
const googleHeader = "Subject,Start Date,Start Time,End Date,End Time,All Day Event,Description,Location,Private"

func mustZone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestOutlookExport(t *testing.T) {
	berlin := mustZone(t, "Europe/Berlin")
	data := outlookHeader + "\r\n" +
		`"Standup","10/29/2026","9:00:00 AM","10/29/2026","9:15:00 AM","False","True","10/29/2026","8:45:00 AM","Me","","","","","","Daily` + "\r\n" + `sync","Room 1","","Normal","False","Normal","2"` + "\r\n" +
		`"Holiday","10/30/2026","","10/31/2026","","True","False","","","","","","","","","","","","Normal","False","Normal","3"` + "\r\n" +
		`"Trip","11/2/2026","","11/5/2026","","True","False","","","","","","","","","","","","Normal","False","Normal","3"` + "\r\n"
	rows, skips, err := Parse([]byte(data), berlin, false)
	if err != nil || len(skips) != 0 || len(rows) != 3 {
		t.Fatalf("rows %+v skips %+v err %v", rows, skips, err)
	}
	s := rows[0]
	if s.Title != "Standup" || s.Location != "Room 1" || s.Description != "Daily\nsync" || s.AllDay || s.Line != 2 ||
		!s.Start.Equal(time.Date(2026, 10, 29, 9, 0, 0, 0, berlin)) || !s.End.Equal(time.Date(2026, 10, 29, 9, 15, 0, 0, berlin)) {
		t.Fatalf("standup %+v", s)
	}
	// Outlook's End Date is already exclusive.
	if h := rows[1]; !h.AllDay || h.Start.Format(time.DateOnly) != "2026-10-30" || h.End.Format(time.DateOnly) != "2026-10-31" {
		t.Fatalf("holiday %+v", h)
	}
	if tr := rows[2]; tr.End.Format(time.DateOnly) != "2026-11-05" {
		t.Fatalf("trip %+v", tr)
	}
}

func TestGoogleFormat(t *testing.T) {
	data := googleHeader + "\n" +
		"Final exam,05/30/2020,10:00 AM,05/30/2020,1:00 PM,False,Two essays,Columbia,True\n" +
		"Conference,06/01/2020,,06/03/2020,,True,,,\n" +
		"Lunch,06/04/2020,12:00 PM,,,False,\"Old Mac\rline ends\",,\n"
	rows, skips, err := Parse([]byte(data), time.UTC, false)
	if err != nil || len(skips) != 0 || len(rows) != 3 {
		t.Fatalf("rows %+v skips %+v err %v", rows, skips, err)
	}
	if e := rows[0]; e.Start.Hour() != 10 || e.End.Hour() != 13 || e.Description != "Two essays" {
		t.Fatalf("exam %+v", e)
	}
	// Google's End Date is the last day, so the exclusive end is the day after.
	if c := rows[1]; !c.AllDay || c.Start.Format(time.DateOnly) != "2020-06-01" || c.End.Format(time.DateOnly) != "2020-06-04" {
		t.Fatalf("conference %+v", c)
	}
	// No end: one hour. A lone CR, which the CSV reader keeps, is a newline.
	if l := rows[2]; l.End.Sub(l.Start) != time.Hour || l.Description != "Old Mac\nline ends" {
		t.Fatalf("lunch %+v", l)
	}
}

func TestDateOrder(t *testing.T) {
	cases := []struct {
		name, dates string
		hint        bool
		want        string
	}{
		{"first part over 12 means day first", "13/02/2026", false, "2026-02-13"},
		{"second part over 12 means month first", "02/13/2026", true, "2026-02-13"},
		{"dotted dates are day first", "03.02.2026", false, "2026-02-03"},
		{"ambiguous follows the hint", "03/02/2026", true, "2026-02-03"},
		{"ambiguous without the hint is month first", "03/02/2026", false, "2026-03-02"},
		{"ISO dates need no order", "2026-03-02", true, "2026-03-02"},
		{"two-digit years are 20xx", "3/2/26", false, "2026-03-02"},
		{"dashed day-first dates", "13-02-2026", false, "2026-02-13"},
		{"year-first with slashes", "2026/03/02", true, "2026-03-02"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rows, skips, err := Parse([]byte("Subject,Start Date\nX,"+c.dates+"\n"), time.UTC, c.hint)
			if err != nil || len(skips) != 0 || len(rows) != 1 {
				t.Fatalf("rows %+v skips %+v err %v", rows, skips, err)
			}
			if got := rows[0].Start.Format(time.DateOnly); got != c.want {
				t.Fatalf("got %s, want %s", got, c.want)
			}
		})
	}
}

func TestSkippedRows(t *testing.T) {
	data := googleHeader + "\n" +
		"No date,,10:00,,,False,,,\n" +
		"Bad date,31/02/2026,10:00,,,False,,,\n" +
		"Bad time,03/02/2026,25:99,,,False,,,\n" +
		"Backwards,03/02/2026,10:00,03/02/2026,9:00,False,,,\n" +
		",,,,,,,,\n" +
		"Good,03/02/2026,10:00,,,False,,,\n"
	rows, skips, err := Parse([]byte(data), time.UTC, false)
	if err != nil || len(rows) != 1 || rows[0].Title != "Good" || rows[0].Line != 7 {
		t.Fatalf("rows %+v err %v", rows, err)
	}
	want := []Skip{{2, "Start Date is missing or not a date"}, {3, "Start Date is missing or not a date"}, {4, "Start Time is missing or not a time"}, {5, "it ends before it starts"}}
	if len(skips) != len(want) {
		t.Fatalf("skips %+v", skips)
	}
	for i := range want {
		if skips[i] != want[i] {
			t.Fatalf("skip %d: got %+v, want %+v", i, skips[i], want[i])
		}
	}
}

func TestEncodings(t *testing.T) {
	const text = "Subject,Start Date\nCafé meeting,03/02/2026\n"
	w1252, err := charmap.Windows1252.NewEncoder().String(text)
	if err != nil {
		t.Fatal(err)
	}
	le, be := []byte{0xFF, 0xFE}, []byte{0xFE, 0xFF}
	for _, r := range text {
		le = append(le, byte(r), byte(r>>8))
		be = append(be, byte(r>>8), byte(r))
	}
	for name, data := range map[string][]byte{
		"utf-8":          []byte(text),
		"utf-8 with BOM": []byte("\ufeff" + text),
		"windows-1252":   []byte(w1252),
		"utf-16 LE":      le,
		"utf-16 BE":      be,
	} {
		rows, _, err := Parse(data, time.UTC, false)
		if err != nil || len(rows) != 1 || rows[0].Title != "Café meeting" {
			t.Errorf("%s: rows %+v err %v", name, rows, err)
		}
	}
}

// TestMalformedEncodings: broken bytes never fail the file or reach the rows as invalid UTF-8.
func TestMalformedEncodings(t *testing.T) {
	for name, data := range map[string][]byte{
		// One stray byte makes the file not UTF-8, so all of it is read as Windows-1252.
		"invalid utf-8":          []byte("Subject,Start Date\nCaf\xc3\xa9 \xff,03/02/2026\n"),
		"utf-16 odd length":      append([]byte{0xFF, 0xFE}, []byte("S\x00u\x00b\x00")...),
		"utf-16 lone surrogate":  {0xFF, 0xFE, 'S', 0, 0x00, 0xD8, ',', 0},
		"windows-1252 undefined": []byte("Subject,Start Date\n\x81\x8d\x8f\x90\x9d,03/02/2026\n"),
	} {
		rows, _, err := Parse(data, time.UTC, false)
		if err != nil && !errors.Is(err, ErrNotCalendar) {
			t.Errorf("%s: err %v", name, err)
		}
		for _, r := range rows {
			if !utf8.ValidString(r.Title) {
				t.Errorf("%s: invalid UTF-8 title %q", name, r.Title)
			}
		}
	}
}

func TestRefusedFiles(t *testing.T) {
	for name, data := range map[string]string{
		"empty":       "",
		"header only": googleHeader + "\n",
		"no subject":  "Title,Start Date\nX,03/02/2026\n",
		"no start":    "Subject,When\nX,03/02/2026\n",
	} {
		if _, _, err := Parse([]byte(data), time.UTC, false); !errors.Is(err, ErrNotCalendar) {
			t.Errorf("%s: err %v, want ErrNotCalendar", name, err)
		}
	}
	full := "Subject,Start Date\n" + strings.Repeat("X,03/02/2026\n", MaxRows)
	if rows, _, err := Parse([]byte(full), time.UTC, false); err != nil || len(rows) != MaxRows {
		t.Errorf("MaxRows rows: %d rows, err %v", len(rows), err)
	}
	if _, _, err := Parse([]byte(full+"X,03/02/2026\n"), time.UTC, false); !errors.Is(err, ErrTooManyRows) {
		t.Errorf("too many rows: err %v", err)
	}
}

func TestUIDIsStable(t *testing.T) {
	data := []byte("Subject,Start Date,Start Time\nX,03/02/2026,10:00\nY,03/02/2026,10:00\n")
	a, _, _ := Parse(data, time.UTC, false)
	b, _, _ := Parse(data, time.UTC, false)
	if a[0].UID() != b[0].UID() || a[0].UID() == a[1].UID() || !strings.HasPrefix(a[0].UID(), "csv-") {
		t.Fatalf("uids %s %s %s", a[0].UID(), b[0].UID(), a[1].UID())
	}
}

// FuzzParse holds any upload to the parser's promises: no panic, at most MaxRows rows, valid
// UTF-8 text, ends never before starts, all-day rows on UTC midnights with an exclusive end, and
// every row number inside the file.
func FuzzParse(f *testing.F) {
	f.Add([]byte(outlookHeader+"\r\n"+`"A","10/29/2026","9:00:00 AM","10/29/2026","9:15:00 AM","False","True","","","","","","","","","","","","Normal","False","Normal","2"`), false)
	f.Add([]byte(googleHeader+"\nX,13/02/2026,,14/02/2026,,True,,,\n"), true)
	f.Add([]byte("Subject,Start Date,Start Time\n\"a\"\"b,1.2.26,3 PM\n"), false)
	f.Add([]byte{0xFE, 0xFF, 0, 'S'}, false)
	f.Fuzz(func(t *testing.T, data []byte, dayFirst bool) {
		rows, skips, err := Parse(data, time.UTC, dayFirst)
		if err != nil {
			if rows != nil || skips != nil || !(errors.Is(err, ErrNotCalendar) || errors.Is(err, ErrTooManyRows)) {
				t.Fatalf("err %v with rows %d skips %d", err, len(rows), len(skips))
			}
			return
		}
		if len(rows)+len(skips) > MaxRows {
			t.Fatalf("%d rows and %d skips", len(rows), len(skips))
		}
		for _, r := range rows {
			if !utf8.ValidString(r.Title + r.Location + r.Description) {
				t.Fatalf("invalid UTF-8 in %+v", r)
			}
			if r.End.Before(r.Start) || r.Line < 2 || r.Line > MaxRows+1 {
				t.Fatalf("row %+v", r)
			}
			if r.AllDay && (r.Start.Location() != time.UTC || r.Start.Hour() != 0 || !r.End.After(r.Start) || r.End.Hour() != 0) {
				t.Fatalf("all-day row %+v", r)
			}
		}
	})
}
