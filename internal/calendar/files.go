package calendar

import (
	"strings"

	"github.com/emersion/go-ical"
)

// File is a linked file on an event (suite contract I): ATTACH;FMTTYPE=<type>;FILENAME=<name>:<url>.
type File struct{ Name, URL, Type string }

const propAttach = "ATTACH"

// linkFile reports whether p is a file link KyCalendar manages: a URI ATTACH (no inline
// bytes) with an https value and a FILENAME. Other ATTACH properties are never touched.
func linkFile(p ical.Prop) bool {
	v := p.Params.Get(ical.ParamValue)
	return p.Params.Get("ENCODING") == "" && (v == "" || strings.EqualFold(v, string(ical.ValueURI))) &&
		p.Params.Get("FILENAME") != "" && strings.HasPrefix(strings.ToLower(p.Value), "https://")
}

// Files are ev's file links, in order.
func Files(ev *ical.Component) []File {
	var out []File
	for _, p := range ev.Props.Values(propAttach) {
		if linkFile(p) {
			out = append(out, File{Name: p.Params.Get("FILENAME"), URL: p.Value, Type: p.Params.Get("FMTTYPE")})
		}
	}
	return out
}

// SetFiles makes files the file links of every VEVENT and VTODO of cal, each as an ATTACH and a
// "File: <name> <url>" description line before any "Join:" line. Other ATTACHes stay.
func SetFiles(cal *ical.Calendar, files []File) {
	for _, ev := range cal.Children {
		if ev.Name != ical.CompEvent && ev.Name != ical.CompToDo {
			continue
		}
		old := Files(ev)
		var keep []ical.Prop
		for _, p := range ev.Props.Values(propAttach) {
			if !linkFile(p) {
				keep = append(keep, p)
			}
		}
		for _, f := range files {
			p := ical.NewProp(propAttach)
			p.Value = f.URL
			p.Params.Set("FILENAME", f.Name)
			if f.Type != "" {
				p.Params.Set("FMTTYPE", f.Type)
			}
			keep = append(keep, *p)
		}
		if len(keep) == 0 {
			ev.Props.Del(propAttach)
		} else {
			ev.Props[propAttach] = keep
		}
		relink(ev, old, Conference(ev))
	}
}

// relink rewrites ev's description as its own text, then a "File:" line per file link, then
// the "Join:" line: oldFiles and oldJoin are the lines to take off first.
func relink(ev *ical.Component, oldFiles []File, oldJoin string) {
	desc, _ := ev.Props.Text(ical.PropDescription)
	desc = StripFiles(StripJoin(desc, oldJoin), oldFiles)
	setText(ev, ical.PropDescription, withJoin(withFiles(desc, Files(ev)), Conference(ev)))
}

func fileLines(files []File) string {
	lines := make([]string, len(files))
	for i, f := range files {
		lines[i] = "File: " + f.Name + " " + f.URL
	}
	return strings.Join(lines, "\n")
}

// StripFiles removes the final "File:" lines for files from desc (after StripJoin).
func StripFiles(desc string, files []File) string {
	if len(files) == 0 {
		return desc
	}
	if d, ok := strings.CutSuffix(strings.TrimRight(desc, "\r\n"), fileLines(files)); ok {
		return strings.TrimRight(d, "\r\n")
	}
	return desc
}

func withFiles(desc string, files []File) string {
	switch {
	case len(files) == 0:
		return desc
	case desc == "":
		return fileLines(files)
	}
	return desc + "\n\n" + fileLines(files)
}
