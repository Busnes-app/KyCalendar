package tasks

import (
	"github.com/emersion/go-ical"
)

// PropRepeatFrom marks a rule that repeats from the completion date; see the interop research.
const PropRepeatFrom = "X-KY-REPEAT-FROM"

func fromCompletion(m *ical.Component) bool {
	p := m.Props.Get(PropRepeatFrom)
	return p != nil && p.Value == "COMPLETION"
}

func setFromCompletion(m *ical.Component, on bool) {
	m.Props.Del(PropRepeatFrom)
	if on {
		setRaw(m, PropRepeatFrom, "COMPLETION")
	}
}

// PropHabit marks a repeating task as a habit; other clients see an ordinary repeating task.
const PropHabit = "X-KY-HABIT"

func isHabit(m *ical.Component) bool {
	p := m.Props.Get(PropHabit)
	return p != nil && p.Value == "TRUE"
}

func setHabit(m *ical.Component, on bool) {
	m.Props.Del(PropHabit)
	if on {
		setRaw(m, PropHabit, "TRUE")
	}
}

// PropRef is KyPost's opaque reference of the message a flagged-mail task came from.
const PropRef = "X-KY-REF"

// SetRef records ref on the task.
func SetRef(cal *ical.Calendar, ref string) error {
	m, err := Master(cal)
	if err != nil {
		return err
	}
	setRaw(m, PropRef, ref)
	return nil
}
