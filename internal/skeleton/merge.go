package skeleton

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/emersion/go-ical"

	"github.com/Busnes-app/kycalendar/internal/calendar"
)

// Layer is one opened part of an object, in the order its Seq gives (E4).
type Layer struct {
	Base       bool   // a whole body; otherwise a patch
	KeepAlarms bool   // a base whose alarms come from the newest unmarked base before it
	Seq        uint64 // the part's sequence number
	Plaintext  []byte // a base's body, or EncodePatch's bytes
}

// Op is one patch operation (E5): set or remove one property of one component, or set the
// KyMessages room link.
type Op struct {
	Op           string      `json:"op"` // set, remove or conference
	Component    string      `json:"component,omitempty"`
	RecurrenceID string      `json:"recurrence_id,omitempty"` // "" is the master
	Property     string      `json:"property,omitempty"`
	Values       []PropValue `json:"values,omitempty"` // set: every instance it leaves
	URL          string      `json:"url,omitempty"`    // conference: "" removes it
}

// PropValue is one property instance in a patch.
type PropValue struct {
	Params map[string][]string `json:"params,omitempty"`
	Value  string              `json:"value"`
}

// Label is the readable label of a patch made of ops: the operations and properties it touches,
// never a value (E5). Two patches with one label touch the same thing, so the newer replaces
// the older.
func Label(ops []Op) string {
	var b bytes.Buffer
	for i, o := range ops {
		if i > 0 {
			b.WriteByte(';')
		}
		b.WriteString(o.Op)
		if o.Op != "conference" {
			fmt.Fprintf(&b, " %s %s %s", o.Property, o.Component, o.RecurrenceID)
		}
	}
	return b.String()
}

// EncodePatch is a patch's plaintext.
func EncodePatch(ops []Op) ([]byte, error) {
	for _, o := range ops {
		if err := o.check(); err != nil {
			return nil, err
		}
	}
	return json.Marshal(ops)
}

// DecodePatch reads EncodePatch's bytes.
func DecodePatch(b []byte) ([]Op, error) {
	var ops []Op
	if err := json.Unmarshal(b, &ops); err != nil {
		return nil, fmt.Errorf("skeleton: patch: %w", err)
	}
	for _, o := range ops {
		if err := o.check(); err != nil {
			return nil, err
		}
	}
	return ops, nil
}

func (o Op) check() error {
	switch o.Op {
	case "conference":
		return nil
	case "set", "remove":
		if o.Component != ical.CompEvent && o.Component != ical.CompToDo || o.Property == "" {
			// apply reaches only the stored components; a patch it cannot apply is refused, never
			// stored as if it had been applied.
			return errors.New("skeleton: patch op needs a VEVENT or VTODO component and a property")
		}
		if isReadable(o.Component, o.Property) {
			return fmt.Errorf("skeleton: %s is readable; it changes in the skeleton, never in a patch", o.Property)
		}
		return nil
	}
	return fmt.Errorf("skeleton: patch op %q", o.Op)
}

// Merge is the object a reader is served (E2): readable properties and the set of components
// from skeleton, content from the newest base, alarms' content from the newest base that is not
// marked keep-alarms, then the patches written after that base, in order. When only one base
// exists and skeleton is still that base's own skeleton, the base's bytes are served unchanged.
func Merge(skeleton []byte, layers []Layer) ([]byte, error) {
	layers = slices.Clone(layers)
	slices.SortFunc(layers, func(a, b Layer) int { return cmpSeq(a.Seq, b.Seq) })
	newest, alarms := -1, -1
	for i, l := range layers {
		if l.Base {
			newest = i
			if !l.KeepAlarms {
				alarms = i
			}
		}
	}
	if newest < 0 {
		return nil, errors.New("skeleton: no base")
	}
	if alarms < 0 {
		alarms = newest
	}
	if newest == len(layers)-1 && newest == 0 {
		if own, err := Split(layers[0].Plaintext); err == nil && bytes.Equal(own, skeleton) {
			return layers[0].Plaintext, nil
		}
	}
	skel, err := decode(skeleton)
	if err != nil {
		return nil, err
	}
	content, err := decode(layers[newest].Plaintext)
	if err != nil {
		return nil, err
	}
	alarmSrc := content
	if alarms != newest {
		if alarmSrc, err = decode(layers[alarms].Plaintext); err != nil {
			return nil, err
		}
	}
	out := &ical.Calendar{Component: merge(skel.Component, content.Component, nil)}
	for _, c := range out.Children {
		if c.Name == ical.CompTimezone {
			continue
		}
		src := find(content.Component, c)
		if src != nil {
			*c = *merge(c, src, find(alarmSrc.Component, c))
		}
	}
	for _, l := range layers[newest+1:] {
		if l.Base {
			continue
		}
		ops, err := DecodePatch(l.Plaintext)
		if err != nil {
			return nil, err
		}
		for _, o := range ops {
			apply(out, o)
		}
	}
	return encode(out.Component)
}

func cmpSeq(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// merge is skel with src's content properties added, and each nested child merged with the child
// of the same name at the same position: VALARMs from alarmsFrom, every other child (RFC 9073
// VLOCATION, PARTICIPANT and the like) from src. Readable properties always come from skel.
func merge(skel, src, alarmsFrom *ical.Component) *ical.Component {
	out := &ical.Component{Name: skel.Name, Props: ical.Props{}}
	for name, ps := range skel.Props {
		out.Props[name] = ps
	}
	for name, ps := range src.Props {
		if !isReadable(skel.Name, name) {
			out.Props[name] = ps
		}
	}
	byName := func(c *ical.Component) map[string][]*ical.Component {
		m := map[string][]*ical.Component{}
		if c != nil {
			for _, x := range c.Children {
				m[x.Name] = append(m[x.Name], x)
			}
		}
		return m
	}
	from, alarms := byName(src), byName(alarmsFrom)
	seen := map[string]int{}
	for _, child := range skel.Children {
		pool := from[child.Name]
		if child.Name == ical.CompAlarm {
			pool = alarms[child.Name]
		}
		if i := seen[child.Name]; i < len(pool) {
			child = merge(child, pool[i], pool[i])
		}
		seen[child.Name]++
		out.Children = append(out.Children, child)
	}
	return out
}

// find is the component of in with c's name and RECURRENCE-ID; an override the content does not
// carry (a worker made it from the master) takes the master, the one with no RECURRENCE-ID.
func find(in *ical.Component, c *ical.Component) *ical.Component {
	rid := recurrenceID(c)
	var master *ical.Component
	for _, x := range in.Children {
		if x.Name != c.Name {
			continue
		}
		switch recurrenceID(x) {
		case rid:
			return x
		case "":
			master = x
		}
	}
	return master
}

func recurrenceID(c *ical.Component) string {
	if p := c.Props.Get(ical.PropRecurrenceID); p != nil {
		return p.Value
	}
	return ""
}

func apply(cal *ical.Calendar, o Op) {
	if o.Op == "conference" {
		calendar.SetConference(cal, o.URL)
		return
	}
	for _, c := range cal.Children {
		if c.Name != o.Component || recurrenceID(c) != o.RecurrenceID {
			continue
		}
		c.Props.Del(o.Property)
		if o.Op == "set" {
			for _, v := range o.Values {
				p := ical.NewProp(o.Property)
				p.Value = v.Value
				for k, vs := range v.Params {
					p.Params[k] = vs
				}
				c.Props.Add(p)
			}
		}
	}
}
