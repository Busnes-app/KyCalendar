package skeleton

import (
	"bytes"

	"github.com/emersion/go-ical"

	"github.com/Busnes-app/kycalendar/internal/calendar"
)

// Sizes is what a writer without the key needs to keep an object's plaintext size exact
// (content encryption E4): for each label a worker patch can carry (LocationLabel,
// ConferenceLabel), the bytes the content it would replace adds to the merged object, and for
// each kind of master component (InheritLabel), what its content adds to an override that has
// none of its own. Every size is measured as Merge writes the object.
type Sizes map[string]int64

// ConferenceLabel is the label of a conference patch (E5).
const ConferenceLabel = "conference"

// LocationLabel is the label of a patch setting LOCATION on one component (E5).
func LocationLabel(component, recurrenceID string) string {
	return Label([]Op{{Op: "set", Component: component, RecurrenceID: recurrenceID, Property: ical.PropLocation}})
}

// InheritLabel names the content an override of component inherits from its master.
func InheritLabel(component string) string { return "inherit " + component }

// Measure is raw's Sizes and the bytes of its VALARMs (alarm_bytes, E4).
func Measure(raw []byte) (Sizes, int64, error) {
	cal, err := decode(raw)
	if err != nil {
		return nil, 0, err
	}
	sizes := Sizes{}
	var alarms int64
	for _, c := range cal.Children {
		if c.Name != ical.CompEvent && c.Name != ical.CompToDo {
			continue
		}
		rid := recurrenceID(c)
		if n := propsSize(c.Props[ical.PropLocation], ical.PropLocation); n > 0 {
			sizes[LocationLabel(c.Name, rid)] = n
		}
		for _, a := range c.Children {
			if a.Name == ical.CompAlarm {
				alarms += size(a)
			}
		}
		if rid == "" {
			sizes[InheritLabel(c.Name)] = size(c) - size(strip(c))
		}
	}
	bare := &ical.Calendar{Component: clone(cal.Component)}
	calendar.SetConference(bare, "")
	if n := size(cal.Component) - size(bare.Component); n > 0 {
		sizes[ConferenceLabel] = n
	}
	return sizes, alarms, nil
}

// Adds is the most bytes op can add to an object with events VEVENTs where nothing it replaces
// is present. A LOCATION is exact. A room link is bounded: its CONFERENCE line is exact, and the
// "Join:" line it appends to each description is counted with its escaping and folding at
// their largest, so a limit checked against Adds is never passed by the merged object.
func Adds(op Op, events int) int64 {
	op = op.norm()
	switch {
	case op.Op == "set":
		var ps []ical.Prop
		for _, v := range op.Values {
			p := ical.NewProp(op.Property)
			p.Value = v.Value
			for k, vs := range v.Params {
				p.Params[k] = vs
			}
			ps = append(ps, *p)
		}
		return propsSize(ps, op.Property)
	case op.Op == "conference" && op.URL != "":
		ev := ical.NewComponent(ical.CompEvent)
		cal := &ical.Calendar{Component: &ical.Component{Name: ical.CompCalendar, Props: ical.Props{}, Children: []*ical.Component{ev}}}
		calendar.SetConference(cal, op.URL)
		conf := propsSize(ev.Props.Values("CONFERENCE"), "CONFERENCE")
		join := int64(2*len(op.URL) + len(`\n\nJoin: `) + len("DESCRIPTION:\r\n"))
		join += 3 * (join/74 + 2) // a fold is "\r\n "; the added text can start or move one
		return int64(events) * (conf + join)
	}
	return 0
}

// propsSize is the bytes ps take when written as properties named name.
func propsSize(ps []ical.Prop, name string) int64 {
	if len(ps) == 0 {
		return 0
	}
	c := &ical.Component{Name: ical.CompEvent, Props: ical.Props{name: ps}}
	return size(c) - size(&ical.Component{Name: ical.CompEvent, Props: ical.Props{}})
}

func size(c *ical.Component) int64 {
	var b bytes.Buffer
	write(&b, c)
	return int64(b.Len())
}

func clone(c *ical.Component) *ical.Component {
	out := &ical.Component{Name: c.Name, Props: ical.Props{}}
	for name, ps := range c.Props {
		cp := make([]ical.Prop, len(ps))
		for i, p := range ps {
			cp[i] = ical.Prop{Name: p.Name, Value: p.Value, Params: ical.Params{}}
			for k, vs := range p.Params {
				cp[i].Params[k] = append([]string(nil), vs...)
			}
		}
		out.Props[name] = cp
	}
	for _, child := range c.Children {
		out.Children = append(out.Children, clone(child))
	}
	return out
}

// WorkerOp reports whether op is one a writer without the key may make (E5): a set of LOCATION
// on an event or task (with no values it removes it, under the same label), or the room link.
func WorkerOp(op Op) bool {
	op = op.norm()
	return op.Op == "conference" ||
		op.Op == "set" && op.Property == ical.PropLocation && (op.Component == ical.CompEvent || op.Component == ical.CompToDo)
}

// Count is how many components named name raw holds at its top level.
func Count(raw []byte, name string) (int, error) {
	cal, err := decode(raw)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, c := range cal.Children {
		if c.Name == name {
			n++
		}
	}
	return n, nil
}

// AddedOverrides counts, per component name, the overrides (components with a RECURRENCE-ID)
// next holds and prev does not: a worker made them from the master, so they inherit its
// content (E2).
func AddedOverrides(prev, next []byte) (map[string]int, error) {
	before, err := decode(prev)
	if err != nil {
		return nil, err
	}
	after, err := decode(next)
	if err != nil {
		return nil, err
	}
	had := map[string]bool{}
	for _, c := range before.Children {
		had[c.Name+"/"+recurrenceID(c)] = true
	}
	out := map[string]int{}
	for _, c := range after.Children {
		if rid := recurrenceID(c); rid != "" && (c.Name == ical.CompEvent || c.Name == ical.CompToDo) && !had[c.Name+"/"+rid] {
			out[c.Name]++
		}
	}
	return out, nil
}
