// Package booking is the pure part of booking pages: a type's rules, the slot grid, who is busy,
// visitor text and the calendar objects a booking sends and stores. Storage, mail and HTTP live
// in internal/store and internal/api.
package booking

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/emersion/go-ical"

	"github.com/Busnes-app/kycalendar/internal/calendar"
)

// Bounds on what a member configures and what a visitor sends.
const (
	MaxTypesPerUser = 20
	MaxWindows      = 21
	MaxTitle        = 100
	MaxName         = 100
	MaxNote         = 1000
	MaxHorizonDays  = 60
	MaxDailyCap     = 100
	HoldFor         = 15 * time.Minute
	MaxHoldsPerMail = 2
	// TypeProp marks a booked event with its booking type, which the daily cap counts.
	TypeProp = "X-KYCALENDAR-BOOKING-TYPE"
	// maxInstances bounds the expansion of one busy scan, like free/busy.
	maxInstances = 5000
)

// Defaults for a new type.
const (
	DefaultNoticeMin   = 240
	DefaultHorizonDays = 30
)

// ErrInvalid is a type, or visitor input, outside the rules; its message says which rule.
var ErrInvalid = errors.New("booking: invalid")

// ErrTooBusy is a busy scan past its instance or time budget.
var ErrTooBusy = errors.New("booking: too many events to scan")

func invalid(msg string) error { return fmt.Errorf("%w: %s", ErrInvalid, msg) }

// Window is a weekly availability window in the type's zone: Day 0 is Sunday, Start and End
// are "HH:MM" (End may be "24:00").
type Window struct {
	Day   int    `json:"day"`
	Start string `json:"start"`
	End   string `json:"end"`
}

// Rules is one booking type's availability.
type Rules struct {
	Duration, BufferBefore, BufferAfter, Notice time.Duration
	HorizonDays, DailyCap                       int
	Zone                                        *time.Location
	Windows                                     []Window
}

// Interval is [Start, End).
type Interval struct{ Start, End time.Time }

func minutes(hm string) (int, bool) {
	if len(hm) != 5 || hm[2] != ':' {
		return 0, false
	}
	h, m := int(hm[0]-'0')*10+int(hm[1]-'0'), int(hm[3]-'0')*10+int(hm[4]-'0')
	for _, c := range hm[:2] + hm[3:] {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	if m > 59 || h > 24 || (h == 24 && m != 0) {
		return 0, false
	}
	return h*60 + m, true
}

// CheckWindows refuses malformed, empty, reversed or overlapping windows.
func CheckWindows(ws []Window) error {
	if len(ws) == 0 || len(ws) > MaxWindows {
		return invalid(fmt.Sprintf("give 1 to %d availability windows", MaxWindows))
	}
	type span struct{ day, a, b int }
	var spans []span
	for _, w := range ws {
		a, ok1 := minutes(w.Start)
		b, ok2 := minutes(w.End)
		if w.Day < 0 || w.Day > 6 || !ok1 || !ok2 || a >= b {
			return invalid("each window needs a weekday and a start before its end (HH:MM)")
		}
		spans = append(spans, span{w.Day, a, b})
	}
	slices.SortFunc(spans, func(x, y span) int { return (x.day*1440 + x.a) - (y.day*1440 + y.a) })
	for i := 1; i < len(spans); i++ {
		if spans[i].day == spans[i-1].day && spans[i].a < spans[i-1].b {
			return invalid("availability windows on one day must not overlap")
		}
	}
	return nil
}

// at is the wall time hm on day's date in day's zone.
func at(day time.Time, hm string) time.Time {
	m, _ := minutes(hm)
	return time.Date(day.Year(), day.Month(), day.Day(), m/60, m%60, 0, 0, day.Location())
}

// Day is the date t falls on in the type's zone, the daily cap's key.
func (r Rules) Day(t time.Time) string { return t.In(r.Zone).Format(time.DateOnly) }

// DayBounds is the local day around t.
func (r Rules) DayBounds(t time.Time) Interval {
	l := t.In(r.Zone)
	d := time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, r.Zone)
	return Interval{d, time.Date(l.Year(), l.Month(), l.Day()+1, 0, 0, 0, 0, r.Zone)}
}

// Range is what Slots looks at from now: the horizon, widened by the buffers.
func (r Rules) Range(now time.Time) Interval {
	return Interval{now.Add(-r.BufferBefore), now.AddDate(0, 0, r.HorizonDays).Add(r.Duration + r.BufferAfter)}
}

// Free reports that no busy period overlaps slot widened by the buffers.
func (r Rules) Free(busy []Interval, slot Interval) bool {
	a, b := slot.Start.Add(-r.BufferBefore), slot.End.Add(r.BufferAfter)
	for _, x := range busy {
		if x.Start.Before(b) && a.Before(x.End) {
			return false
		}
	}
	return true
}

// Slots lists the free slots, in order: on the grid (steps of the duration from each window's
// start, ending by the window's end), starting no sooner than now+notice and before now+horizon,
// free with buffers, on days below the cap. perDay counts bookings and holds by Day.
func (r Rules) Slots(now time.Time, busy []Interval, perDay map[string]int) []Interval {
	earliest, latest := now.Add(r.Notice), now.AddDate(0, 0, r.HorizonDays)
	local := now.In(r.Zone)
	var out []Interval
	for d := -1; d <= r.HorizonDays+1; d++ {
		day := time.Date(local.Year(), local.Month(), local.Day()+d, 0, 0, 0, 0, r.Zone)
		for _, w := range r.Windows {
			if w.Day != int(day.Weekday()) {
				continue
			}
			end := at(day, w.End)
			for s := at(day, w.Start); !s.Add(r.Duration).After(end); s = s.Add(r.Duration) {
				slot := Interval{s, s.Add(r.Duration)}
				if s.Before(earliest) || !s.Before(latest) || !r.Free(busy, slot) {
					continue
				}
				if r.DailyCap > 0 && perDay[r.Day(s)] >= r.DailyCap {
					continue
				}
				out = append(out, slot)
			}
		}
	}
	slices.SortFunc(out, func(a, b Interval) int { return a.Start.Compare(b.Start) })
	return slices.CompactFunc(out, func(a, b Interval) bool { return a.Start.Equal(b.Start) })
}

// Offered is the slot starting at start when Slots offers it now.
func (r Rules) Offered(now time.Time, busy []Interval, perDay map[string]int, start time.Time) (Interval, bool) {
	for _, s := range r.Slots(now, busy, perDay) {
		if s.Start.Equal(start) {
			return s, true
		}
	}
	return Interval{}, false
}

// Busy scans stored objects for the periods busy in span, reading floating times in the type's
// zone, and counts by Day the busy instances of objects booked for typeID. TRANSP:TRANSPARENT
// and cancelled instances are free (calendar.BusyTimes); tentative ones are busy. Past
// deadline (zero: none) or maxInstances it is ErrTooBusy.
func (r Rules) Busy(objs [][]byte, typeID string, span Interval, deadline time.Time) ([]Interval, map[string]int, error) {
	var busy []Interval
	perDay := map[string]int{}
	for _, raw := range objs {
		if !deadline.IsZero() && time.Now().After(deadline) {
			return nil, nil, ErrTooBusy
		}
		cal, err := ical.NewDecoder(bytes.NewReader(raw)).Decode()
		if err != nil {
			return nil, nil, err
		}
		bt, err := calendar.BusyTimes(cal, span.Start, span.End, r.Zone, maxInstances-len(busy))
		if errors.Is(err, calendar.ErrTooManyInstances) {
			return nil, nil, ErrTooBusy
		}
		if err != nil {
			return nil, nil, err
		}
		ours := typeID != "" && bookedAs(cal) == typeID
		for _, b := range bt {
			busy = append(busy, Interval{b.Start, b.End})
			if ours {
				perDay[r.Day(b.Start)]++
			}
		}
	}
	return busy, perDay, nil
}

func bookedAs(cal *ical.Calendar) string {
	for _, c := range cal.Children {
		if c.Name == ical.CompEvent {
			if p := c.Props.Get(TypeProp); p != nil {
				return p.Value
			}
		}
	}
	return ""
}

func hidden(r rune) bool {
	return unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp)
}

// CleanTitle is a type's title: trimmed, 1-100 characters, nothing invisible.
func CleanTitle(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || utf8.RuneCountInString(s) > MaxTitle || strings.IndexFunc(s, hidden) >= 0 {
		return "", invalid(fmt.Sprintf("give a title of 1 to %d characters, with no control characters", MaxTitle))
	}
	return s, nil
}

// CleanName is a visitor's name: trimmed, 1-100 characters, nothing invisible and no '"', which
// no iCalendar parameter can carry.
func CleanName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if !utf8.ValidString(s) || s == "" || utf8.RuneCountInString(s) > MaxName || strings.IndexFunc(s, hidden) >= 0 || strings.ContainsRune(s, '"') {
		return "", invalid(fmt.Sprintf("give your name: 1 to %d characters, no quotation marks", MaxName))
	}
	return s, nil
}

// CleanNote is a visitor's note: plain text up to 1000 characters; newline and tab are the only
// control characters kept, CRLF becomes LF.
func CleanNote(s string) (string, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	bad := func(r rune) bool { return r != '\n' && r != '\t' && hidden(r) }
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > MaxNote || strings.IndexFunc(s, bad) >= 0 {
		return "", invalid(fmt.Sprintf("keep the note to %d characters of plain text", MaxNote))
	}
	return s, nil
}

func eventIn(cal *ical.Calendar) *ical.Component {
	for _, c := range cal.Children {
		if c.Name == ical.CompEvent {
			return c
		}
	}
	return nil
}

func setRaw(c *ical.Component, name, v string) {
	p := ical.NewProp(name)
	p.Value = v
	c.Props.Set(p)
}

func setAddress(c *ical.Component, name, email, cn string) {
	p := ical.NewProp(name)
	p.Value = "mailto:" + email
	if cn != "" {
		p.Params.Set(ical.ParamCommonName, cn)
	}
	if name == ical.PropAttendee {
		p.Params.Set(ical.ParamRole, "REQ-PARTICIPANT")
		p.Params.Set(ical.ParamParticipationStatus, "NEEDS-ACTION")
		p.Params.Set(ical.ParamRSVP, "TRUE")
	}
	c.Props.Add(p)
}

// HoldMessage is the METHOD:REQUEST mailed for a new hold: fixed text, the confirm link and
// nothing the visitor wrote, so a stranger's words never leave the member's mailbox.
func HoldMessage(uid, title, host, organizer, booker, confirmURL string, slot Interval, expires, now time.Time) *ical.Calendar {
	cal := calendar.NewEvent(uid, calendar.EventInput{
		Title: "Confirm your booking: " + title,
		Description: "Someone asked to book this time with " + host + " using this address.\n\n" +
			"Confirm before " + expires.UTC().Format("15:04 MST on 2 January") + ": " + confirmURL + "\n\n" +
			"If this was not you, ignore this message: nothing is booked and the time is released.",
		Start: slot.Start.UTC(), End: slot.End.UTC(), Zone: time.UTC,
	}, now)
	cal.Props.SetText(ical.PropMethod, "REQUEST")
	ev := eventIn(cal)
	setRaw(ev, ical.PropStatus, "TENTATIVE")
	setAddress(ev, ical.PropOrganizer, organizer, "")
	setAddress(ev, ical.PropAttendee, booker, "")
	return cal
}

// Booked is the member's event for a confirmed booking, SEQUENCE 1 so it supersedes the hold
// message of the same UID, marked with its type. The caller invites the booker (itip.SetAttendees,
// then NameAttendee) and writes it through the scheduled path, which mails the invitation.
func Booked(uid, typeID, title, name, note, manageURL string, zone *time.Location, slot Interval, now time.Time) *ical.Calendar {
	desc := "Booked by " + name + "."
	if note != "" {
		desc += "\n\n" + note
	}
	desc += "\n\nCancel or reschedule: " + manageURL
	cal := calendar.NewEvent(uid, calendar.EventInput{Title: title + " (" + name + ")", Description: desc,
		Start: slot.Start, End: slot.End, Zone: zone}, now)
	ev := eventIn(cal)
	setRaw(ev, ical.PropSequence, "1")
	setRaw(ev, TypeProp, typeID)
	return cal
}

// NameAttendee sets the CN of the attendee with email on cal's events.
func NameAttendee(cal *ical.Calendar, email, name string) {
	for _, c := range cal.Children {
		if c.Name != ical.CompEvent {
			continue
		}
		for i := range c.Props[ical.PropAttendee] {
			p := &c.Props[ical.PropAttendee][i]
			if strings.EqualFold(strings.TrimPrefix(strings.ToLower(p.Value), "mailto:"), email) {
				p.Params.Set(ical.ParamCommonName, name)
			}
		}
	}
}

// Move puts cal's event at slot, written in zone, for a reschedule. The scheduler raises
// SEQUENCE when the organizer's write changes the time.
func Move(cal *ical.Calendar, slot Interval, zone *time.Location, now time.Time) error {
	ev, err := calendar.Master(cal)
	if err != nil {
		return err
	}
	ev.Props.Del(ical.PropDuration)
	for name, t := range map[string]time.Time{ical.PropDateTimeStart: slot.Start, ical.PropDateTimeEnd: slot.End} {
		p := ical.NewProp(name)
		if zone == time.UTC {
			p.SetDateTime(t.UTC())
		} else {
			p.SetDateTime(t.In(zone))
		}
		ev.Props.Set(p)
	}
	if zone != time.UTC {
		hasZone := false
		for _, c := range cal.Children {
			if id, _ := c.Props.Text(ical.PropTimezoneID); c.Name == ical.CompTimezone && id == zone.String() {
				hasZone = true
			}
		}
		if !hasZone {
			from := time.Date(slot.Start.In(zone).Year(), 1, 1, 0, 0, 0, 0, zone)
			cal.Children = append([]*ical.Component{calendar.VTimezone(zone, from, from.AddDate(11, 0, 0))}, cal.Children...)
		}
	}
	stamp := ical.NewProp(ical.PropDateTimeStamp)
	stamp.SetDateTime(now.UTC())
	ev.Props.Set(stamp)
	return nil
}
