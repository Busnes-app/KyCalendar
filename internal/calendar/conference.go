package calendar

import (
	"strings"

	"github.com/emersion/go-ical"
)

// ConferenceLabel marks the CONFERENCE property (RFC 7986) KyCalendar writes for a KyMessages room.
const ConferenceLabel = "KyMessages"

const propConference = "CONFERENCE"

// Conference is the KyMessages room link on ev, "" when none.
func Conference(ev *ical.Component) string {
	for _, p := range ev.Props.Values(propConference) {
		if strings.EqualFold(p.Params.Get("LABEL"), ConferenceLabel) {
			return p.Value
		}
	}
	return ""
}

// SetConference puts the room link url on every VEVENT of cal, as
// CONFERENCE;VALUE=URI;FEATURE=VIDEO;LABEL=KyMessages:<url> and a final "Join: <url>"
// description line, so other calendar clients show it. "" removes both. Other CONFERENCE
// properties stay.
func SetConference(cal *ical.Calendar, url string) {
	for _, ev := range cal.Children {
		if ev.Name != ical.CompEvent {
			continue
		}
		old := Conference(ev)
		var keep []ical.Prop
		for _, p := range ev.Props.Values(propConference) {
			if !strings.EqualFold(p.Params.Get("LABEL"), ConferenceLabel) {
				keep = append(keep, p)
			}
		}
		if url != "" {
			p := ical.NewProp(propConference)
			p.Value = url
			p.Params.Set(ical.ParamValue, string(ical.ValueURI))
			p.Params.Set("FEATURE", "VIDEO")
			p.Params.Set("LABEL", ConferenceLabel)
			keep = append(keep, *p)
		}
		if len(keep) == 0 {
			ev.Props.Del(propConference)
		} else {
			ev.Props[propConference] = keep
		}
		desc, _ := ev.Props.Text(ical.PropDescription)
		setText(ev, ical.PropDescription, withJoin(StripJoin(desc, old), url))
	}
}

// KeepJoin puts the "Join:" line back on every VEVENT that has a room link: a web or agent
// edit replaces the description with text that never shows it (StripJoin).
func KeepJoin(cal *ical.Calendar) {
	for _, ev := range cal.Children {
		if ev.Name != ical.CompEvent {
			continue
		}
		if url := Conference(ev); url != "" {
			desc, _ := ev.Props.Text(ical.PropDescription)
			setText(ev, ical.PropDescription, withJoin(StripJoin(desc, url), url))
		}
	}
}

// StripJoin removes a final "Join: <url>" line from desc.
func StripJoin(desc, url string) string {
	if url == "" {
		return desc
	}
	if d, ok := strings.CutSuffix(strings.TrimRight(desc, "\r\n"), "Join: "+url); ok {
		return strings.TrimRight(d, "\r\n")
	}
	return desc
}

func withJoin(desc, url string) string {
	switch {
	case url == "":
		return desc
	case desc == "":
		return "Join: " + url
	}
	return desc + "\n\nJoin: " + url
}
