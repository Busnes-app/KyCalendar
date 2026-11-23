// Package calexport writes a calendar's stored objects as one iCalendar file, for kycalendar
// export-org and a member's own export (tenancy spec, Export).
package calexport

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/emersion/go-ical"

	"github.com/Busnes-app/kycalendar/internal/store"
)

// CalendarICS is one VCALENDAR holding every stored object of calendar calID, each time zone
// once, how many objects it holds, and the objects it could not merge: those that do not parse or
// that go-ical cannot write back. A caller hands those over as stored, so an export is never short.
func CalendarICS(ctx context.Context, st store.Store, calID string) (body []byte, items int, unmerged []*store.CalendarObject, err error) {
	objs, err := st.Calendars().ListObjects(ctx, calID)
	if err != nil {
		return nil, 0, nil, err
	}
	out := ical.NewCalendar()
	out.Props.SetText(ical.PropVersion, "2.0")
	out.Props.SetText(ical.PropProductID, "-//Busnes-app//KyCalendar export//EN")
	zones := map[string][]byte{} // TZID -> the encoded VTIMEZONE written under it
	for _, o := range objs {
		cal, err := ical.NewDecoder(bytes.NewReader(o.Data)).Decode()
		if err == nil {
			err = ical.NewEncoder(io.Discard).Encode(cal) // what is merged must write back
		}
		if err != nil {
			unmerged = append(unmerged, o)
			continue
		}
		renamed := map[string]string{}
		var comps []*ical.Component
		for _, child := range cal.Children {
			if child.Name != ical.CompTimezone {
				comps = append(comps, child)
				continue
			}
			id, _ := child.Props.Text(ical.PropTimezoneID)
			body, err := zoneBytes(child)
			if err != nil {
				return nil, 0, nil, err // the whole object encoded, so its zone does too
			}
			// One TZID may name different rules in different objects (Outlook's "Customized Time
			// Zone"): only an identical definition is shared; another gets a TZID of its own.
			name := id
			for n := 2; zones[name] != nil && !bytes.Equal(zones[name], body); n++ {
				name = fmt.Sprintf("%s (%d)", id, n)
			}
			if name != id {
				renamed[id] = name // set before sharing, so a reused renamed zone is still pointed at
			}
			if zones[name] != nil {
				continue // this exact definition is already written under name
			}
			child.Props.SetText(ical.PropTimezoneID, name)
			zones[name] = body
			out.Children = append(out.Children, child)
		}
		for _, c := range comps {
			renameTZID(c, renamed)
		}
		out.Children = append(out.Children, comps...)
		items++
	}
	var b bytes.Buffer
	if len(out.Children) == 0 {
		// go-ical refuses a calendar with no components; an empty calendar is still exported.
		b.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Busnes-app//KyCalendar export//EN\r\nEND:VCALENDAR\r\n")
		return b.Bytes(), items, unmerged, nil
	}
	if err := ical.NewEncoder(&b).Encode(out); err != nil {
		return nil, 0, nil, err
	}
	return b.Bytes(), items, unmerged, nil
}

// zoneBytes is z encoded on its own, to compare two definitions.
func zoneBytes(z *ical.Component) ([]byte, error) {
	var b bytes.Buffer
	err := ical.NewEncoder(&b).Encode(&ical.Calendar{Component: &ical.Component{Name: ical.CompCalendar,
		Props:    ical.Props{ical.PropVersion: {{Name: ical.PropVersion, Value: "2.0"}}, ical.PropProductID: {{Name: ical.PropProductID, Value: "x"}}},
		Children: []*ical.Component{z}}})
	return b.Bytes(), err
}

// renameTZID points every TZID parameter in c and its children at the renamed zones.
func renameTZID(c *ical.Component, renamed map[string]string) {
	if len(renamed) == 0 {
		return
	}
	for _, ps := range c.Props {
		for i := range ps {
			if to, ok := renamed[ps[i].Params.Get(ical.ParamTimezoneID)]; ok {
				ps[i].Params.Set(ical.ParamTimezoneID, to)
			}
		}
	}
	for _, child := range c.Children {
		renameTZID(child, renamed)
	}
}
