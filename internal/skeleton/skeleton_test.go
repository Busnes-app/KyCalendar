package skeleton

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/emersion/go-ical"
)

// corpus is every real client object in the repository's fixtures: Apple, Google, Outlook,
// Thunderbird and Microsoft 365 events and tasks.
func corpus(t *testing.T) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for _, glob := range []string{"../calendar/testdata/recurrence/*.ics", "../tasks/testdata/*.ics", "../feed/testdata/*.ics"} {
		files, err := filepath.Glob(glob)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			c, err := decode(b)
			if err != nil {
				continue // a fixture of a parse failure
			}
			// A stored object is one UID (calendar_objects is unique on it); a whole feed such as
			// m365.ics, with several UIDs and one event without any, is not one object.
			uids := map[string]bool{}
			for _, x := range c.Children {
				if x.Name == ical.CompEvent || x.Name == ical.CompToDo {
					uid, _ := x.Props.Text(ical.PropUID)
					uids[uid] = true
				}
			}
			if len(uids) != 1 || uids[""] {
				continue
			}
			out[filepath.Base(f)] = b
		}
	}
	if len(out) < 10 {
		t.Fatalf("corpus of %d objects", len(out))
	}
	return out
}

// props is c's properties as sorted "COMP NAME;PARAMS:VALUE" lines, its children's too, in
// order: what a reader sees, whatever the byte layout.
func props(c *ical.Component) []string {
	var out []string
	for name, ps := range c.Props {
		for _, p := range ps {
			var params []string
			for k, v := range p.Params {
				params = append(params, fmt.Sprintf("%s=%q", k, v))
			}
			slices.Sort(params)
			out = append(out, fmt.Sprintf("%s %s;%s:%q", c.Name, name, strings.Join(params, ";"), p.Value))
		}
	}
	slices.Sort(out)
	for i, child := range c.Children {
		for _, l := range props(child) {
			out = append(out, fmt.Sprintf("%d/%s", i, l))
		}
	}
	return out
}

func mustDecode(t *testing.T, b []byte) *ical.Calendar {
	t.Helper()
	c, err := decode(b)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// E1, E2: a skeleton holds no content property, and a base on its own skeleton is served back
// byte for byte; the slow path (any patch present) serves every property the client wrote.
func TestCorpusSplitsAndMergesBack(t *testing.T) {
	for name, raw := range corpus(t) {
		skel, err := Split(raw)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var check func(c *ical.Component)
		check = func(c *ical.Component) {
			for p := range c.Props {
				if !isReadable(c.Name, p) {
					t.Errorf("%s: skeleton keeps %s %s", name, c.Name, p)
				}
			}
			for _, child := range c.Children {
				check(child)
			}
		}
		check(mustDecode(t, skel).Component)
		for _, secret := range []string{"SUMMARY", "DESCRIPTION", "LOCATION"} {
			if bytes.Contains(skel, []byte("\n"+secret)) {
				t.Errorf("%s: skeleton holds %s", name, secret)
			}
		}
		got, err := Merge(skel, []Layer{{Base: true, Seq: 1, Plaintext: raw}})
		if err != nil || !bytes.Equal(got, raw) {
			t.Errorf("%s: fast path changed the bytes (%v)", name, err)
		}
		empty, _ := EncodePatch(nil)
		slow, err := Merge(skel, []Layer{{Base: true, Seq: 1, Plaintext: raw}, {Seq: 2, Plaintext: empty}})
		if err != nil {
			t.Fatalf("%s: slow path: %v", name, err)
		}
		if a, b := props(mustDecode(t, raw).Component), props(mustDecode(t, slow).Component); !slices.Equal(a, b) {
			t.Errorf("%s: slow path lost or changed properties:\n%s\nwant\n%s", name, strings.Join(b, "\n"), strings.Join(a, "\n"))
		}
	}
}

const meeting = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nX-WR-CALNAME:Private name\r\n" +
	"BEGIN:VEVENT\r\nUID:m1\r\nDTSTAMP:20261101T000000Z\r\nDTSTART:20261110T090000Z\r\nDTEND:20261110T100000Z\r\n" +
	"RRULE:FREQ=WEEKLY;COUNT=4\r\nSUMMARY:Board review\r\nDESCRIPTION:Numbers\r\nLOCATION:Room 1\r\nX-APPLE-SECRET:x\r\n" +
	"ORGANIZER:mailto:ann@example.com\r\nATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:bob@example.com\r\n" +
	"BEGIN:VALARM\r\nACTION:DISPLAY\r\nTRIGGER:-PT15M\r\nDESCRIPTION:Mine\r\nEND:VALARM\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

func text(t *testing.T, cal *ical.Calendar, rid, name string) string {
	t.Helper()
	for _, c := range cal.Children {
		if c.Name == ical.CompEvent && recurrenceID(c) == rid {
			if p := c.Props.Get(name); p != nil {
				return p.Value
			}
			return ""
		}
	}
	t.Fatalf("no component with RECURRENCE-ID %q", rid)
	return ""
}

// E2: readable properties come from the skeleton, so a worker's change to times or PARTSTAT,
// made in the skeleton alone, reaches the reader with the content intact. Calendar-level and
// unknown X- content survives.
func TestSkeletonWinsForReadableProperties(t *testing.T) {
	skel, _ := Split([]byte(meeting))
	moved := strings.Replace(strings.Replace(string(skel), "DTSTART:20261110T090000Z", "DTSTART:20261110T110000Z", 1),
		"PARTSTAT=NEEDS-ACTION", "PARTSTAT=ACCEPTED", 1)
	got, err := Merge([]byte(moved), []Layer{{Base: true, Seq: 1, Plaintext: []byte(meeting)}})
	if err != nil {
		t.Fatal(err)
	}
	cal := mustDecode(t, got)
	if v := text(t, cal, "", "DTSTART"); v != "20261110T110000Z" {
		t.Errorf("DTSTART %q", v)
	}
	if !strings.Contains(string(got), "PARTSTAT=ACCEPTED") || strings.Contains(string(got), "NEEDS-ACTION") {
		t.Errorf("PARTSTAT not from the skeleton:\n%s", got)
	}
	for _, want := range []string{"SUMMARY:Board review", "X-APPLE-SECRET:x", "X-WR-CALNAME:Private name", "DESCRIPTION:Mine"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("lost %s:\n%s", want, got)
		}
	}
}

// E2: an override a worker made (in the skeleton only) shows its master's content, then a patch
// for that occurrence alone; the master keeps its own.
func TestOverridesInheritTheMasterThenTheirPatches(t *testing.T) {
	skel, _ := Split([]byte(meeting))
	override := "BEGIN:VEVENT\r\nUID:m1\r\nDTSTAMP:20261101T000000Z\r\nRECURRENCE-ID:20261117T090000Z\r\n" +
		"DTSTART:20261117T130000Z\r\nDTEND:20261117T140000Z\r\nORGANIZER:mailto:ann@example.com\r\n" +
		"ATTENDEE;PARTSTAT=DECLINED:mailto:bob@example.com\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	withOverride := strings.Replace(string(skel), "END:VCALENDAR\r\n", override, 1)
	patch, err := EncodePatch([]Op{{Op: "set", Component: "VEVENT", RecurrenceID: "20261117T090000Z", Property: "LOCATION", Values: []PropValue{{Value: "Room 9"}}}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := Merge([]byte(withOverride), []Layer{{Base: true, Seq: 1, Plaintext: []byte(meeting)}, {Seq: 2, Plaintext: patch}})
	if err != nil {
		t.Fatal(err)
	}
	cal := mustDecode(t, got)
	for rid, want := range map[string][2]string{"": {"Board review", "Room 1"}, "20261117T090000Z": {"Board review", "Room 9"}} {
		if s, l := text(t, cal, rid, "SUMMARY"), text(t, cal, rid, "LOCATION"); s != want[0] || l != want[1] {
			t.Errorf("RECURRENCE-ID %q: SUMMARY %q LOCATION %q, want %v", rid, s, l, want)
		}
	}
}

// E4: an organizer's update (a keep-alarms base) replaces every content property, so a cleared
// LOCATION is gone, and the attendee's own alarm text comes from the base before it. A patch
// older than the newest base no longer applies.
func TestKeepAlarmsBase(t *testing.T) {
	update := strings.NewReplacer("SUMMARY:Board review", "SUMMARY:Board review v2", "LOCATION:Room 1\r\n", "",
		"DESCRIPTION:Mine", "DESCRIPTION:Organizer alarm").Replace(meeting)
	skel, _ := Split([]byte(update))
	stale, _ := EncodePatch([]Op{{Op: "set", Component: "VEVENT", Property: "LOCATION", Values: []PropValue{{Value: "Room 5"}}}})
	got, err := Merge(skel, []Layer{
		{Base: true, Seq: 1, Plaintext: []byte(meeting)},
		{Seq: 2, Plaintext: stale},
		{Base: true, KeepAlarms: true, Seq: 3, Plaintext: []byte(update)},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, "SUMMARY:Board review v2") || strings.Contains(s, "LOCATION") {
		t.Errorf("content not from the newest base:\n%s", s)
	}
	if !strings.Contains(s, "DESCRIPTION:Mine") || strings.Contains(s, "Organizer alarm") {
		t.Errorf("alarm text not kept from the earlier base:\n%s", s)
	}
}

// E5: set, remove and conference change only what they name, in sequence order, and a patch
// cannot touch a readable property.
func TestPatchOperations(t *testing.T) {
	skel, _ := Split([]byte(meeting))
	first, _ := EncodePatch([]Op{{Op: "remove", Component: "VEVENT", Property: "DESCRIPTION"}})
	second, _ := EncodePatch([]Op{{Op: "conference", URL: "https://messages.example/r/1"}})
	got, err := Merge(skel, []Layer{{Seq: 9, Plaintext: second}, {Base: true, Seq: 1, Plaintext: []byte(meeting)}, {Seq: 5, Plaintext: first}})
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	for _, want := range []string{"SUMMARY:Board review", "LOCATION:Room 1", "LABEL=KyMessages", "Join: https://messages.example/r/1"} {
		if !strings.Contains(s, want) {
			t.Errorf("lost %s:\n%s", want, s)
		}
	}
	if strings.Contains(s, "Numbers") {
		t.Errorf("removed DESCRIPTION came back:\n%s", s)
	}
	for _, bad := range []Op{
		{Op: "set", Component: "VCALENDAR", Property: "X-WR-CALNAME"},
		{Op: "set", Component: "VALARM", Property: "DESCRIPTION"},
		{Op: "set", Component: "VEVENT", Property: "DTSTART"},
		{Op: "set", Component: "VEVENT", Property: "ATTENDEE"},
		{Op: "set", Property: "LOCATION"},
		{Op: "rename", Component: "VEVENT", Property: "LOCATION"},
	} {
		if _, err := EncodePatch([]Op{bad}); err == nil {
			t.Errorf("%+v encoded", bad)
		}
	}
	if Label([]Op{{Op: "set", Component: "VEVENT", Property: "LOCATION", Values: []PropValue{{Value: "secret"}}}}) != "set LOCATION VEVENT " ||
		strings.Contains(Label([]Op{{Op: "conference", URL: "https://secret"}}), "secret") {
		t.Error("a label carries a value")
	}
	if _, err := Merge(skel, []Layer{{Seq: 1, Plaintext: first}}); err == nil {
		t.Error("merged with no base")
	}
}

// Nested components other than alarms (RFC 9073 VLOCATION, PARTICIPANT) keep their content on
// the slow path, as the client wrote it.
func TestNestedComponentsKeepTheirContent(t *testing.T) {
	raw := strings.Replace(meeting, "BEGIN:VALARM", "BEGIN:VLOCATION\r\nUID:loc-1\r\nNAME:Board room\r\nX-SECRET:x\r\nEND:VLOCATION\r\n"+
		"BEGIN:PARTICIPANT\r\nUID:p-1\r\nPARTICIPANT-TYPE:SPEAKER\r\nEND:PARTICIPANT\r\nBEGIN:VALARM", 1)
	skel, err := Split([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(skel), "Board room") {
		t.Errorf("skeleton keeps a nested component's content:\n%s", skel)
	}
	empty, _ := EncodePatch(nil)
	got, err := Merge(skel, []Layer{{Base: true, Seq: 1, Plaintext: []byte(raw)}, {Seq: 2, Plaintext: empty}})
	if err != nil {
		t.Fatal(err)
	}
	if a, b := props(mustDecode(t, []byte(raw)).Component), props(mustDecode(t, got).Component); !slices.Equal(a, b) {
		t.Errorf("slow path lost nested content:\n%s\nwant\n%s", strings.Join(b, "\n"), strings.Join(a, "\n"))
	}
}

// The writer folds long lines without splitting a UTF-8 character and quotes parameter values
// holding ':', ';' or ',', so the slow path reads back exactly what the client wrote.
func TestWriterFoldsAndQuotes(t *testing.T) {
	long := strings.Repeat("Réunion ☕ budget, ", 20)
	raw := strings.Replace(meeting, "DESCRIPTION:Numbers", "DESCRIPTION;ALTREP=\"cid:part1@example.com\":"+long, 1)
	skel, _ := Split([]byte(raw))
	empty, _ := EncodePatch(nil)
	got, err := Merge(skel, []Layer{{Base: true, Seq: 1, Plaintext: []byte(raw)}, {Seq: 2, Plaintext: empty}})
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(string(got), "\r\n") {
		if len(l) > 75 || !utf8.ValidString(l) {
			t.Errorf("line of %d octets, or splitting a character: %q", len(l), l)
		}
	}
	if a, b := props(mustDecode(t, []byte(raw)).Component), props(mustDecode(t, got).Component); !slices.Equal(a, b) {
		t.Errorf("writer changed properties:\n%s\nwant\n%s", strings.Join(b, "\n"), strings.Join(a, "\n"))
	}
}

// Each component takes content only from its own RECURRENCE-ID match (or the master), never by
// position: a worker-made override B never shows a property only a dropped override A had.
func TestOverrideContentNeverComesFromAnotherOccurrence(t *testing.T) {
	overrideA := "BEGIN:VEVENT\r\nUID:m1\r\nDTSTAMP:20261101T000000Z\r\nRECURRENCE-ID:20261117T090000Z\r\nDTSTART:20261117T100000Z\r\n" +
		"DTEND:20261117T110000Z\r\nSUMMARY:Moved review\r\nCOMMENT:Only A\r\nX-ONLY-A:yes\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	base := strings.Replace(meeting, "END:VCALENDAR\r\n", overrideA, 1)
	skel, _ := Split([]byte(meeting)) // the master only
	overrideB := "BEGIN:VEVENT\r\nUID:m1\r\nDTSTAMP:20261101T000000Z\r\nRECURRENCE-ID:20261124T090000Z\r\nDTSTART:20261124T130000Z\r\n" +
		"DTEND:20261124T140000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	withB := strings.Replace(string(skel), "END:VCALENDAR\r\n", overrideB, 1)
	empty, _ := EncodePatch(nil)
	got, err := Merge([]byte(withB), []Layer{{Base: true, Seq: 1, Plaintext: []byte(base)}, {Seq: 2, Plaintext: empty}})
	if err != nil {
		t.Fatal(err)
	}
	cal := mustDecode(t, got)
	if s := text(t, cal, "20261124T090000Z", "SUMMARY"); s != "Board review" {
		t.Errorf("override B SUMMARY %q, want the master's", s)
	}
	if strings.Contains(string(got), "Only A") || strings.Contains(string(got), "X-ONLY-A") {
		t.Errorf("override A's content leaked into the served object:\n%s", got)
	}
}

// A keep-alarms base takes alarm content from the unmarked base only: a property only the newest
// base's alarm has is never served.
func TestKeepAlarmsTakesNoAlarmContentFromTheNewestBase(t *testing.T) {
	update := strings.Replace(meeting, "DESCRIPTION:Mine\r\n", "DESCRIPTION:Organizer alarm\r\nATTACH:https://organizer.example/sound\r\n", 1)
	skel, _ := Split([]byte(update))
	got, err := Merge(skel, []Layer{{Base: true, Seq: 1, Plaintext: []byte(meeting)}, {Base: true, KeepAlarms: true, Seq: 2, Plaintext: []byte(update)}})
	if err != nil {
		t.Fatal(err)
	}
	if s := string(got); strings.Contains(s, "organizer.example") || strings.Contains(s, "Organizer alarm") || !strings.Contains(s, "DESCRIPTION:Mine") {
		t.Errorf("alarm content not from the earlier base alone:\n%s", s)
	}
}

// iCalendar names are case-insensitive: a lower-case readable property is refused like an upper-
// case one, and a lower-case content property applies under its upper-case name.
func TestPatchNamesAreCaseInsensitive(t *testing.T) {
	if _, err := DecodePatch([]byte(`[{"op":"set","component":"vevent","property":"dtstart","values":[{"value":"20300101T000000Z"}]}]`)); err == nil {
		t.Error("a lower-case readable property was accepted")
	}
	ops, err := DecodePatch([]byte(`[{"op":"set","component":"vevent","property":"location","values":[{"value":"Room 7"}]}]`))
	if err != nil {
		t.Fatal(err)
	}
	if Label(ops) != Label([]Op{{Op: "set", Component: "VEVENT", Property: "LOCATION"}}) {
		t.Errorf("label %q depends on case", Label(ops))
	}
	patch, _ := EncodePatch(ops)
	skel, _ := Split([]byte(meeting))
	got, err := Merge(skel, []Layer{{Base: true, Seq: 1, Plaintext: []byte(meeting)}, {Seq: 2, Plaintext: patch}})
	if err != nil || strings.Count(string(got), "LOCATION") != 1 || !strings.Contains(string(got), "LOCATION:Room 7") {
		t.Errorf("lower-case patch: %v\n%s", err, got)
	}
}

// A patch value must already be in escaped wire form: a raw newline, another control character,
// or a '"' in a parameter value would break the content line, so the patch is refused.
func TestPatchValuesMustFitOneContentLine(t *testing.T) {
	ok := Op{Op: "set", Component: "VEVENT", Property: "DESCRIPTION", Values: []PropValue{{Value: `two\nlines`, Params: map[string][]string{"ALTREP": {"cid:x@y"}}}}}
	if _, err := EncodePatch([]Op{ok}); err != nil {
		t.Fatalf("escaped value refused: %v", err)
	}
	for name, v := range map[string]PropValue{
		"raw LF":             {Value: "two\nlines"},
		"raw CR":             {Value: "a\rb"},
		"NUL":                {Value: "a\x00b"},
		"DEL":                {Value: "a\x7fb"},
		"quote in param":     {Value: "x", Params: map[string][]string{"ALTREP": {`a"b`}}},
		"newline in param":   {Value: "x", Params: map[string][]string{"ALTREP": {"a\nb"}}},
		"colon in param key": {Value: "x", Params: map[string][]string{"X:Y": {"a"}}},
	} {
		op := Op{Op: "set", Component: "VEVENT", Property: "DESCRIPTION", Values: []PropValue{v}}
		if _, err := EncodePatch([]Op{op}); err == nil {
			t.Errorf("%s: patch encoded", name)
		}
		raw, _ := json.Marshal([]Op{op})
		if _, err := DecodePatch(raw); err == nil {
			t.Errorf("%s: patch decoded", name)
		}
	}
}

// One operation's values, or its room link, are at most MaxPatchValue bytes: that bounds how far
// plain_size can drift before a fold.
func TestPatchValuesAreBounded(t *testing.T) {
	long := strings.Repeat("x", MaxPatchValue+1)
	for name, op := range map[string]Op{
		"set":        {Op: "set", Component: "VEVENT", Property: "LOCATION", Values: []PropValue{{Value: long}}},
		"two values": {Op: "set", Component: "VEVENT", Property: "LOCATION", Values: []PropValue{{Value: long[:1100]}, {Value: long[:1100]}}},
		"param":      {Op: "set", Component: "VEVENT", Property: "LOCATION", Values: []PropValue{{Value: "x", Params: map[string][]string{"ALTREP": {long}}}}},
		"room link":  {Op: "conference", URL: "https://" + long},
	} {
		if _, err := EncodePatch([]Op{op}); err == nil {
			t.Errorf("%s over %d bytes encoded", name, MaxPatchValue)
		}
	}
	if _, err := EncodePatch([]Op{{Op: "set", Component: "VEVENT", Property: "LOCATION", Values: []PropValue{{Value: long[:MaxPatchValue]}}}}); err != nil {
		t.Errorf("exactly %d bytes refused: %v", MaxPatchValue, err)
	}
}

// A property name is an iCalendar name token and a RECURRENCE-ID a plain date or date-time: a
// name with a line break, ':' or ';' could add lines (even readable properties) or merge labels.
func TestPatchNamesAreTokens(t *testing.T) {
	for name, op := range map[string]Op{
		"line break":  {Op: "set", Component: "VEVENT", Property: "X-A:x\r\nATTENDEE;PARTSTAT=ACCEPTED", Values: []PropValue{{Value: "y"}}},
		"colon":       {Op: "remove", Component: "VEVENT", Property: "LOCATION:x"},
		"semicolon":   {Op: "remove", Component: "VEVENT", Property: "LOCATION;X=1"},
		"space":       {Op: "remove", Component: "VEVENT", Property: "LOCATION X"},
		"bad rid":     {Op: "remove", Component: "VEVENT", Property: "LOCATION", RecurrenceID: "2026;x"},
		"rid newline": {Op: "remove", Component: "VEVENT", Property: "LOCATION", RecurrenceID: "20261117T090000Z\n"},
	} {
		if _, err := EncodePatch([]Op{op}); err == nil {
			t.Errorf("%s: encoded", name)
		}
	}
	for _, rid := range []string{"", "20261117", "20261117T090000", "20261117T090000Z"} {
		if _, err := EncodePatch([]Op{{Op: "remove", Component: "VEVENT", Property: "x-apple-travel", RecurrenceID: rid}}); err != nil {
			t.Errorf("RECURRENCE-ID %q refused: %v", rid, err)
		}
	}
}
