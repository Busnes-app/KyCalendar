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
