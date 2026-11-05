package store

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/emersion/go-ical"
)

func TestCancellationGrowthBoundsDateFormsAndOverlaps(t *testing.T) {
	for _, form := range []struct{ name, params, suffix string }{
		{"UTC", "", "T090000Z"},
		{"floating", "", "T090000"},
		{"date", ";VALUE=DATE", ""},
		{"implicit_date_datetime", "", ""},
		{"TZID", ";TZID=America/New_York", "T090000"},
	} {
		t.Run(form.name, func(t *testing.T) {
			decode := func(raw string) *ical.Calendar {
				t.Helper()
				cal, err := ical.NewDecoder(strings.NewReader(raw)).Decode()
				if err != nil {
					t.Fatal(err)
				}
				return cal
			}
			encode := func(cal *ical.Calendar) []byte {
				t.Helper()
				var b bytes.Buffer
				if err := ical.NewEncoder(&b).Encode(cal); err != nil {
					t.Fatal(err)
				}
				return b.Bytes()
			}
			event := func(day string, override bool) string {
				rid := ""
				if override {
					rid = "RECURRENCE-ID" + form.params + ":" + day + form.suffix + "\r\n"
				}
				return "BEGIN:VEVENT\r\nUID:bounds\r\nDTSTAMP:20261101T000000Z\r\nDTSTART" + form.params + ":" + day + form.suffix + "\r\n" + rid + "ORGANIZER:mailto:alice@example.com\r\nATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:bob@example.com\r\nSEQUENCE:000100\r\nLAST-MODIFIED:20261101T000000Z\r\nEND:VEVENT\r\n"
			}
			wrap := func(events string) string {
				return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Bounds test//EN\r\n" + events + "END:VCALENDAR\r\n"
			}
			master := strings.Replace(event("20261110", false), "END:VEVENT", "RRULE:FREQ=DAILY;COUNT=40\r\nEND:VEVENT", 1)
			initial := []byte(strings.ReplaceAll(wrap(master+event("20261111", true)), "\r\n", "\n"))
			var notices []*ical.Calendar
			for i := 0; i < 24; i++ {
				day := time.Date(2026, 11, 12+i, 0, 0, 0, 0, time.UTC).Format("20060102")
				notices = append(notices, decode(wrap(event("20261111", true)+event(day, true))))
			}
			for _, reverse := range []bool{false, true} {
				raw := bytes.Clone(initial)
				ordered := append([]*ical.Calendar(nil), notices...)
				if reverse {
					for i, j := 0, len(ordered)-1; i < j; i, j = i+1, j-1 {
						ordered[i], ordered[j] = ordered[j], ordered[i]
					}
				}
				var reserve int64
				for _, msg := range ordered {
					n, err := cancellationCopyGrowth(raw, msg)
					if err != nil {
						t.Fatal(err)
					}
					reserve += n
				}
				limit := int64(len(raw)) + reserve
				for step, msg := range ordered {
					cal := decode(string(raw))
					for _, ev := range msg.Children {
						key, ok := calendar.OccurrenceKey(cal, ev.Props.Get(ical.PropRecurrenceID))
						if !ok {
							t.Fatal("fixture occurrence not resolved")
						}
						if err := calendar.DeleteOne(cal, key, time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)); err != nil {
							t.Fatal(err)
						}
					}
					raw = encode(cal)
					used := int64(len(raw))
					for _, pending := range ordered[step+1:] {
						n, err := cancellationCopyGrowth(raw, pending)
						if err != nil {
							t.Fatal(err)
						}
						used += n
					}
					if used > limit {
						t.Fatalf("%s reverse=%v step=%d unreserved growth: %d > %d", form.name, reverse, step, used, limit)
					}
				}
				if !strings.Contains(string(raw), fmt.Sprintf("EXDATE%s:20261111%s", form.params, form.suffix)) {
					t.Fatal("wrong occurrence date form")
				}
			}
		})
	}
}
