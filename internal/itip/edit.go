package itip

import (
	"errors"
	"strings"

	"github.com/emersion/go-ical"
)

var (
	ErrNotOrganizer = errors.New("itip: only the organizer changes attendees")
	ErrNotInvited   = errors.New("itip: you are not an attendee")
	ErrBadPartstat  = errors.New("itip: answer must be ACCEPTED, TENTATIVE or DECLINED")
)

// Answers a user may give from the web or KyPost.
var answers = map[string]bool{"ACCEPTED": true, "TENTATIVE": true, "DECLINED": true}

// Attendee is one attendee as the web shows it.
type Attendee struct {
	Address Address
	Status  string // PARTSTAT, NEEDS-ACTION when absent
}

// AttendeesOf lists comp's attendees in the order the object has them; unparseable ones are skipped.
func AttendeesOf(comp *ical.Component) []Attendee {
	var out []Attendee
	for _, p := range comp.Props[ical.PropAttendee] {
		if a, ok := ParseAddress(p.Value); ok {
			out = append(out, Attendee{Address: a, Status: strings.ToUpper(cmpOr(p.Params.Get(ical.ParamParticipationStatus), "NEEDS-ACTION"))})
		}
	}
	return out
}

// SetAttendees makes want the attendee list of every VEVENT, with self as organizer. Attendees
// kept keep their answers and parameters; new ones await an answer (RSVP=TRUE); self is listed
// as ACCEPTED. An empty want removes ORGANIZER and every ATTENDEE: no longer a meeting.
// ErrNotOrganizer when another organizer owns the object.
func SetAttendees(cal *ical.Calendar, self Address, want []Address) error {
	if org, ok := Organizer(cal); ok && org != self {
		return ErrNotOrganizer
	}
	if _, ok := Organizer(cal); !ok {
		for _, c := range events(cal) {
			if len(c.Props[ical.PropAttendee]) > 0 {
				return ErrNotOrganizer // attendees but no organizer: not ours to rewrite
			}
		}
	}
	seen := map[Address]bool{self: true}
	var list []Address
	for _, a := range want {
		if !seen[a] {
			seen[a] = true
			list = append(list, a)
		}
	}
	if len(list) > maxAttendeesPerComponent {
		return ErrInvalid
	}
	for _, c := range events(cal) {
		if len(list) == 0 {
			c.Props.Del(ical.PropOrganizer)
			c.Props.Del(ical.PropAttendee)
			continue
		}
		old := map[Address]ical.Prop{}
		for _, p := range c.Props[ical.PropAttendee] {
			if a, ok := ParseAddress(p.Value); ok {
				old[a] = p
			}
		}
		org := ical.NewProp(ical.PropOrganizer)
		org.Value = string(self)
		c.Props.Set(org)
		c.Props.Del(ical.PropAttendee)
		for _, a := range append([]Address{self}, list...) {
			if p, ok := old[a]; ok {
				c.Props.Add(&p)
				continue
			}
			p := ical.NewProp(ical.PropAttendee)
			p.Value = string(a)
			if a == self {
				p.Params.Set(ical.ParamParticipationStatus, "ACCEPTED")
			} else {
				p.Params.Set(ical.ParamParticipationStatus, "NEEDS-ACTION")
				p.Params.Set(ical.ParamRSVP, "TRUE")
			}
			c.Props.Add(p)
		}
	}
	return nil
}

// SetPartstat records self's answer on the occurrence recurrenceID, or on every occurrence when
// it is "". ErrNotInvited when self is not an attendee there.
func SetPartstat(cal *ical.Calendar, recurrenceID string, self Address, partstat string) error {
	partstat = strings.ToUpper(partstat)
	if !answers[partstat] {
		return ErrBadPartstat
	}
	found := false
	for _, c := range events(cal) {
		if recurrenceID != "" && recurrenceKey(c) != recurrenceID {
			continue
		}
		atts := c.Props[ical.PropAttendee]
		for i := range atts {
			if a, ok := ParseAddress(atts[i].Value); ok && a == self {
				atts[i].Params.Set(ical.ParamParticipationStatus, partstat)
				atts[i].Params.Del(ical.ParamRSVP)
				found = true
			}
		}
	}
	if !found {
		return ErrNotInvited
	}
	return nil
}
