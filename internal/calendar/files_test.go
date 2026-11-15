package calendar

import (
	"strings"
	"testing"

	"github.com/emersion/go-ical"
)

var (
	report = File{Name: "Q3 report; final.pdf", URL: "https://drive.example/l/#tok1", Type: "application/pdf"}
	sheet  = File{Name: "Budget", URL: "https://drive.example/l/#tok2"}
)

func TestSetFilesWritesAttachAndLinesBeforeJoin(t *testing.T) {
	cal := conferenceEvent(t, "Agenda")
	ev, _ := Master(cal)
	inline := ical.NewProp(propAttach) // another client's inline file stays untouched
	inline.Value = "aGVsbG8="
	inline.Params.Set("ENCODING", "BASE64")
	inline.Params.Set(ical.ParamValue, "BINARY")
	inline.Params.Set("FILENAME", "hello.txt")
	ev.Props.Add(inline)

	SetFiles(cal, []File{report, sheet})
	if got := Files(ev); len(got) != 2 || got[0] != report || got[1] != sheet {
		t.Fatalf("files %+v", got)
	}
	want := "Agenda\n\nFile: Q3 report; final.pdf https://drive.example/l/#tok1\nFile: Budget https://drive.example/l/#tok2\n\nJoin: " + room
	if d, _ := ev.Props.Text(ical.PropDescription); d != want {
		t.Fatalf("description %q", d)
	}
	out := encoded(t, cal)
	if !strings.Contains(out, `ATTACH;FILENAME="Q3 report; final.pdf";FMTTYPE=application/pdf:https://drive.example/l/#tok1`) ||
		!strings.Contains(out, "ATTACH;FILENAME=Budget:https://drive.example/l/#tok2") || !strings.Contains(out, "ENCODING=BASE64") {
		t.Fatalf("encoded:\n%s", out)
	}
	// The room going away keeps the files; the files going away keep the room and the inline file.
	SetConference(cal, "")
	if d, _ := ev.Props.Text(ical.PropDescription); d != "Agenda\n\nFile: Q3 report; final.pdf https://drive.example/l/#tok1\nFile: Budget https://drive.example/l/#tok2" {
		t.Fatalf("after the room: %q", d)
	}
	SetConference(cal, room)
	SetFiles(cal, []File{sheet})
	if d, _ := ev.Props.Text(ical.PropDescription); d != "Agenda\n\nFile: Budget https://drive.example/l/#tok2\n\nJoin: "+room {
		t.Fatalf("after one removal: %q", d)
	}
	SetFiles(cal, nil)
	if d, _ := ev.Props.Text(ical.PropDescription); d != "Agenda\n\nJoin: "+room || len(ev.Props.Values(propAttach)) != 1 {
		t.Fatalf("after removal: %q %v", d, ev.Props.Values(propAttach))
	}
}

// A web edit sends the description without the lines (the view strips them): KeepJoin puts
// both back, and the view's StripJoin then StripFiles gives the user's text again.
func TestWebEditKeepsTheFiles(t *testing.T) {
	cal := conferenceEvent(t, "Agenda")
	SetFiles(cal, []File{report})
	ev, _ := Master(cal)
	setText(ev, ical.PropDescription, "New agenda")
	KeepJoin(cal)
	d, _ := ev.Props.Text(ical.PropDescription)
	if d != "New agenda\n\nFile: Q3 report; final.pdf https://drive.example/l/#tok1\n\nJoin: "+room {
		t.Fatalf("description %q", d)
	}
	if got := StripFiles(StripJoin(d, room), Files(ev)); got != "New agenda" {
		t.Fatalf("shown %q", got)
	}
	KeepJoin(cal) // idempotent
	if d2, _ := ev.Props.Text(ical.PropDescription); d2 != d {
		t.Fatalf("second keep %q", d2)
	}
}

func TestFilesIgnoresWhatItDoesNotManage(t *testing.T) {
	ev := ical.NewComponent(ical.CompEvent)
	for _, c := range []struct{ value, filename, valueType string }{
		{"http://drive.example/x", "a", ""},        // not https
		{"https://drive.example/x", "", ""},        // no file name
		{"https://drive.example/x", "b", "BINARY"}, // not a URI
	} {
		p := ical.NewProp(propAttach)
		p.Value = c.value
		if c.filename != "" {
			p.Params.Set("FILENAME", c.filename)
		}
		if c.valueType != "" {
			p.Params.Set(ical.ParamValue, c.valueType)
		}
		ev.Props.Add(p)
	}
	if got := Files(ev); len(got) != 0 {
		t.Fatalf("files %+v", got)
	}
}
