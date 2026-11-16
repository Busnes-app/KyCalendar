package tasks

import (
	"bytes"
	"time"

	"github.com/emersion/go-ical"
)

// Completion is a task finished (Done) or reopened between two versions of its object, for the
// completion log behind streaks and insights. Occurrence names which occurrence: the due (else
// start) value it had, so the same completion seen twice (on the web, then synced back from a
// phone) is one entry.
type Completion struct {
	Occurrence string
	At         time.Time
	Done       bool
}

// Change compares prev (nil for a new object) and next as any client wrote them, received at
// received. A repeating task that moved to its next occurrence, as Tasks.org and the web do, is
// a completion of the occurrence it left. A COMPLETED time in the future or before the task was
// created is replaced by received; an older one is kept (a phone offline for days counts on the
// day it happened).
func Change(prev, next *ical.Calendar, received time.Time) (Completion, bool) {
	nm, err := Master(next)
	if err != nil {
		return Completion{}, false
	}
	var pm *ical.Component
	if prev != nil {
		pm, _ = Master(prev)
	}
	nextDone, prevDone := done(nm), pm != nil && done(pm)
	switch {
	case nextDone && !prevDone:
		return Completion{Occurrence: occurrence(nm), At: completedAt(nm, received), Done: true}, true
	case prevDone && !nextDone && occurrence(pm) == occurrence(nm):
		return Completion{Occurrence: occurrence(pm)}, true
	case pm != nil && !prevDone && !nextDone && pm.Props.Get(ical.PropRecurrenceRule) != nil && occurrence(pm) != occurrence(nm):
		// Moved on: it is a completion when the move is the rule's own next step.
		if movedOn(prev, occurrence(nm), received) {
			return Completion{Occurrence: occurrence(pm), At: received, Done: true}, true
		}
	}
	return Completion{}, false
}

func done(m *ical.Component) bool {
	st, _ := m.Props.Text(ical.PropStatus)
	return st == Completed || st == "" && m.Props.Get(ical.PropCompleted) != nil
}

func occurrence(m *ical.Component) string {
	for _, name := range []string{ical.PropDue, ical.PropDateTimeStart} {
		if p := m.Props.Get(name); p != nil {
			return p.Params.Get(ical.ParamTimezoneID) + p.Value
		}
	}
	return ""
}

func completedAt(m *ical.Component, received time.Time) time.Time {
	p := m.Props.Get(ical.PropCompleted)
	if p == nil {
		return received
	}
	at, err := p.DateTime(time.UTC)
	if err != nil || at.After(received) {
		return received
	}
	if c := m.Props.Get(ical.PropCreated); c != nil {
		if created, err := c.DateTime(time.UTC); err == nil && at.Before(created) {
			return received
		}
	}
	return at
}

// movedOn reports whether completing prev now would move it to the occurrence want.
func movedOn(prev *ical.Calendar, want string, now time.Time) bool {
	var b bytes.Buffer
	if ical.NewEncoder(&b).Encode(prev) != nil {
		return false
	}
	c, err := ical.NewDecoder(&b).Decode()
	if err != nil {
		return false
	}
	st := Completed
	res, err := Apply(c, Patch{Status: &st}, now, time.UTC)
	if err != nil || !res.Advanced {
		return false
	}
	m, _ := Master(c)
	return occurrence(m) == want
}
