package store_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"

	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/contentkey"
	"github.com/Busnes-app/kycalendar/internal/skeleton"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// Content encryption CE3: what workers, who never hold a key, change in sealed objects.

func partsOf(t *testing.T, st store.Store, cal, name string) []contentkey.Part {
	t.Helper()
	parts, err := contentkey.DecodeParts(rowOf(t, st, cal, name).sealed)
	if err != nil {
		t.Fatal(err)
	}
	return parts
}

// workerPut writes body as a worker: no credential, with wc.
func (f contentFixture) workerPut(t *testing.T, name, body string, wc *store.WorkerContent) error {
	t.Helper()
	o := &store.CalendarObject{CalendarID: f.cal.ID, Name: name, Component: "VEVENT", UID: uidOf(body), Data: []byte(body), FirstStart: 1}
	_, err := f.st.Calendars().PutObjectPlanned(store.WithWorkerContent(context.Background(), wc), o, "", false, f.lim, f.plan)
	return err
}

// workerEdit is a worker's read-change-write of name: it reads the skeleton, as workers do.
func (f contentFixture) workerEdit(t *testing.T, name string, edit func(*ical.Calendar), wc *store.WorkerContent) error {
	t.Helper()
	o, err := f.st.Calendars().GetObject(context.Background(), f.cal.ID, name)
	if err != nil {
		t.Fatal(err)
	}
	cal, err := ical.NewDecoder(bytes.NewReader(o.Data)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	edit(cal)
	var b bytes.Buffer
	if err := ical.NewEncoder(&b).Encode(cal); err != nil {
		t.Fatal(err)
	}
	return f.workerPut(t, name, b.String(), wc)
}

func (f contentFixture) read(t *testing.T, name string) *store.CalendarObject {
	t.Helper()
	o, err := f.st.Calendars().GetObject(f.unlocked(), f.cal.ID, name)
	if err != nil || o.Locked {
		t.Fatalf("unlocked read of %s: %v", name, err)
	}
	return o
}

func eventOf(t *testing.T, data []byte, rid string) *ical.Component {
	t.Helper()
	cal, err := ical.NewDecoder(bytes.NewReader(data)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cal.Children {
		if c.Name == ical.CompEvent {
			if p := c.Props.Get(ical.PropRecurrenceID); (p == nil && rid == "") || (p != nil && p.Value == rid) {
				return c
			}
		}
	}
	t.Fatalf("no VEVENT %q in\n%s", rid, data)
	return nil
}

func textOf(c *ical.Component, name string) string {
	v, _ := c.Props.Text(name)
	return v
}

// sizeIsMerged checks plain_size, as stored before the read (which may fold), is the merged
// object's size, as a plaintext row counts it.
func sizeIsMerged(t *testing.T, f contentFixture, name, when string) {
	t.Helper()
	r := rowOf(t, f.st, f.cal.ID, name)
	if r.plainSize == nil {
		t.Fatalf("%s: no plain_size", when)
	}
	if o := f.read(t, name); *r.plainSize != int64(len(o.Data)) {
		t.Fatalf("%s: plain_size %d, merged object %d bytes", when, *r.plainSize, len(o.Data))
	}
}

const attendeeCopy = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\nBEGIN:VEVENT\r\nUID:mtg\r\nDTSTAMP:20261127T000000Z\r\nDTSTART:20261201T090000Z\r\nDTEND:20261201T100000Z\r\nSUMMARY:Secret board meeting\r\nLOCATION:Room 42\r\nORGANIZER:mailto:org@example.com\r\nATTENDEE;PARTSTAT=ACCEPTED:mailto:me@example.com\r\nBEGIN:VALARM\r\nACTION:DISPLAY\r\nTRIGGER:-PT15M\r\nDESCRIPTION:private wake-up note\r\nEND:VALARM\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

// organizerUpdate is what delivery writes over the copy (itip.AttendeeCopy): the organizer's new
// object with the copy's own alarms, which a worker reads as skeletons.
func organizerUpdate(summary, location, alarm string) string {
	loc := ""
	if location != "" {
		loc = "LOCATION:" + location + "\r\n"
	}
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\nBEGIN:VEVENT\r\nUID:mtg\r\nDTSTAMP:20261128T000000Z\r\nDTSTART:20261201T100000Z\r\nDTEND:20261201T110000Z\r\nSEQUENCE:1\r\nSUMMARY:" + summary + "\r\n" + loc +
		"ORGANIZER:mailto:org@example.com\r\nATTENDEE;PARTSTAT=ACCEPTED:mailto:me@example.com\r\n" + alarm + "END:VEVENT\r\nEND:VCALENDAR\r\n"
}

const skeletonAlarm = "BEGIN:VALARM\r\nACTION:DISPLAY\r\nTRIGGER:-PT15M\r\nEND:VALARM\r\n"

// An organizer's update that clears LOCATION reaches an attendee's sealed copy with the new
// content, no location and the attendee's own alarm intact; fifty of them to a copy nobody opens
// leave two bases, and the next unlocked read folds them to one with the same ETag (E4).
func TestOrganizerUpdateReachesASealedCopy(t *testing.T) {
	st := newTestStore(t)
	f := newContentFixture(t, st, "usr_ou", true)
	if _, err := f.put(t, f.unlocked(), "mtg.ics", attendeeCopy); err != nil {
		t.Fatal(err)
	}
	keep := &store.WorkerContent{Base: true, KeepAlarms: true}
	for i := range 50 {
		if err := f.workerPut(t, "mtg.ics", organizerUpdate(fmt.Sprintf("Merger with Acme v%d", i), "", skeletonAlarm), keep); err != nil {
			t.Fatal(err)
		}
	}
	r := rowOf(t, st, f.cal.ID, "mtg.ics")
	noPlaintext(t, r.data)
	if parts := partsOf(t, st, f.cal.ID, "mtg.ics"); len(parts) != 2 || parts[0].KeepAlarms || !parts[1].KeepAlarms {
		t.Fatalf("after 50 updates: %d parts, want the alarms' base and the newest update", len(parts))
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM worker_keys WHERE calendar_id = 'cal_usr_ou'`); n != 1 {
		t.Fatalf("%d worker keys left, want the newest update's only", n)
	}
	sizeIsMerged(t, f, "mtg.ics", "before the fold")
	o := f.read(t, "mtg.ics")
	ev := eventOf(t, o.Data, "")
	if textOf(ev, ical.PropSummary) != "Merger with Acme v49" || ev.Props.Get(ical.PropLocation) != nil {
		t.Fatalf("the update did not apply: %s", o.Data)
	}
	if len(ev.Children) != 1 || textOf(ev.Children[0], ical.PropDescription) != "private wake-up note" {
		t.Fatalf("the attendee's own alarm was lost: %s", o.Data)
	}
	if o.ETag != r.etag {
		t.Fatal("a fold changed the ETag")
	}
	if parts := partsOf(t, st, f.cal.ID, "mtg.ics"); len(parts) != 1 || parts[0].WorkerKey != "" {
		t.Fatalf("the read did not fold: %d parts", len(parts))
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM worker_keys WHERE calendar_id = 'cal_usr_ou'`); n != 0 {
		t.Fatalf("the fold left %d worker keys", n)
	}
	if again := f.read(t, "mtg.ics"); !bytes.Equal(again.Data, o.Data) || again.ETag != r.etag {
		t.Fatal("the folded object reads differently")
	}
	sizeIsMerged(t, f, "mtg.ics", "after the fold")
}

// An update plus the copy's own alarms over MaxObjectSize is refused, as the plaintext copy
// would be; one byte under is accepted and counted with the kept alarms (E4).
func TestKeptAlarmsCountTowardsTheLimit(t *testing.T) {
	st := newTestStore(t)
	f := newContentFixture(t, st, "usr_ka", true)
	note := strings.Repeat("a", 600<<10)
	body := strings.Replace(attendeeCopy, "DESCRIPTION:private wake-up note\r\n", fold("DESCRIPTION:"+note), 1)
	if _, err := f.put(t, f.unlocked(), "mtg.ics", body); err != nil {
		t.Fatal(err)
	}
	keep := &store.WorkerContent{Base: true, KeepAlarms: true}
	var alarmBytes int64
	db, q := store.DBForTest(st)
	if err := db.QueryRow(q(`SELECT alarm_bytes FROM calendar_objects WHERE calendar_id = ? AND name = 'mtg.ics'`), f.cal.ID).Scan(&alarmBytes); err != nil || alarmBytes < int64(len(note)) {
		t.Fatalf("alarm_bytes %d: %v", alarmBytes, err)
	}
	update := func(n int) string {
		return organizerUpdate("Merger with Acme", "", "X-PAD:"+strings.Repeat("b", n)+"\r\n"+skeletonAlarm)
	}
	// plain_size is the update without its own alarm skeletons plus the kept alarms.
	own := int64(len(skeletonAlarm))
	fits := int(calendar.MaxObjectSize - alarmBytes + own - int64(len(update(0))))
	if err := f.workerPut(t, "mtg.ics", update(fits+1), keep); !errors.Is(err, store.ErrTooLarge) {
		t.Fatalf("an update past the limit with the kept alarms: %v", err)
	}
	if err := f.workerPut(t, "mtg.ics", update(fits), keep); err != nil {
		t.Fatalf("an update exactly at the limit: %v", err)
	}
	if r := rowOf(t, st, f.cal.ID, "mtg.ics"); *r.plainSize != calendar.MaxObjectSize {
		t.Fatalf("plain_size %d, want %d", *r.plainSize, calendar.MaxObjectSize)
	}
	if got := countedUsage(t, st); got != calendar.MaxObjectSize {
		t.Fatalf("counted usage %d, want %d", got, calendar.MaxObjectSize)
	}
}

const series = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\nBEGIN:VEVENT\r\nUID:ser\r\nDTSTAMP:20261127T000000Z\r\nDTSTART:20261201T090000Z\r\nDTEND:20261201T100000Z\r\nRRULE:FREQ=DAILY;COUNT=60\r\nSUMMARY:Secret board meeting\r\nDESCRIPTION:Merger with Acme\r\nLOCATION:Room 42\r\nORGANIZER:mailto:me@example.com\r\nATTENDEE;PARTSTAT=ACCEPTED:mailto:me@example.com\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

// addOverride is a worker's override of one occurrence: a copy of the master's readable
// properties, as a REPLY or a booking reschedule makes it.
func addOverride(rid string) func(*ical.Calendar) {
	return func(cal *ical.Calendar) {
		m := cal.Children[0]
		ov := ical.NewComponent(ical.CompEvent)
		for name, ps := range m.Props {
			if name != ical.PropRecurrenceRule {
				ov.Props[name] = ps
			}
		}
		for _, name := range []string{ical.PropRecurrenceID, ical.PropDateTimeStart} {
			p := ical.NewProp(name)
			p.Value = rid
			ov.Props.Set(p)
		}
		ov.Props.Del(ical.PropDateTimeEnd)
		cal.Children = append(cal.Children, ov)
	}
}

func setLocation(rid, value string) *store.WorkerContent {
	return &store.WorkerContent{Patch: []skeleton.Op{{Op: "set", Component: "VEVENT", RecurrenceID: rid, Property: "LOCATION", Values: []skeleton.PropValue{{Value: value}}}}}
}

// A worker override shows the master's content; a room's later set LOCATION on it keeps the
// master's title and description, before and after a fold, and plain_size follows (E2, E4).
func TestWorkerOverrideAndRoomLocation(t *testing.T) {
	st := newTestStore(t)
	f := newContentFixture(t, st, "usr_wo", true)
	long := strings.Repeat("x", 40<<10)
	if _, err := f.put(t, f.unlocked(), "ser.ics", strings.Replace(series, "LOCATION:Room 42\r\n", fold("LOCATION:"+long), 1)); err != nil {
		t.Fatal(err)
	}
	rid := "20261203T090000Z"
	if err := f.workerEdit(t, "ser.ics", addOverride(rid), nil); err != nil {
		t.Fatal(err)
	}
	ov := eventOf(t, f.read(t, "ser.ics").Data, rid)
	if textOf(ov, ical.PropSummary) != "Secret board meeting" || textOf(ov, ical.PropDescription) != "Merger with Acme" || textOf(ov, ical.PropLocation) != long {
		t.Fatal("a worker's override does not show the master's content")
	}
	sizeIsMerged(t, f, "ser.ics", "after a worker's override")
	if err := f.workerEdit(t, "ser.ics", func(*ical.Calendar) {}, setLocation(rid, "Room 7")); err != nil {
		t.Fatal(err)
	}
	for _, when := range []string{"before the fold", "after the fold"} {
		sizeIsMerged(t, f, "ser.ics", when) // its read is the one that folds
		o := f.read(t, "ser.ics")
		ov := eventOf(t, o.Data, rid)
		if textOf(ov, ical.PropSummary) != "Secret board meeting" || textOf(ov, ical.PropDescription) != "Merger with Acme" || textOf(ov, ical.PropLocation) != "Room 7" {
			t.Fatalf("%s: the room's location on the override: %s", when, o.Data)
		}
		if textOf(eventOf(t, o.Data, ""), ical.PropLocation) != long {
			t.Fatalf("%s: the master's location changed", when)
		}
	}
	// The master's 40 KiB location replaced by a short one.
	if err := f.workerEdit(t, "ser.ics", func(*ical.Calendar) {}, setLocation("", "Room 8")); err != nil {
		t.Fatal(err)
	}
	sizeIsMerged(t, f, "ser.ics", "after replacing the long location")
	noPlaintext(t, rowOf(t, st, f.cal.ID, "ser.ics").data)
}

// 40 room locations, one per occurrence, on a meeting nobody opens stop adding parts at 16,
// audit content_part_dropped for the rest and keep every readable change; the next unlocked
// read folds them to one base with the same ETag.
func TestWorkerPatchesStopAtTheCap(t *testing.T) {
	st := newTestStore(t)
	f := newContentFixture(t, st, "usr_cap", true)
	if _, err := f.put(t, f.unlocked(), "ser.ics", series); err != nil {
		t.Fatal(err)
	}
	for i := range 40 {
		rid := time.Date(2026, 12, 1+i, 9, 0, 0, 0, time.UTC).Format("20060102T150405Z")
		if err := f.workerEdit(t, "ser.ics", addOverride(rid), setLocation(rid, fmt.Sprintf("Room %d", i))); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(partsOf(t, st, f.cal.ID, "ser.ics")); n != 17 {
		t.Fatalf("%d parts, want the base and 16 patches", n)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM audit_records WHERE action = 'content_part_dropped'`); n != 24 {
		t.Fatalf("%d content_part_dropped audits, want 24", n)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM audit_records WHERE action = 'content_part_dropped' AND details LIKE '%Room%'`); n != 0 {
		t.Fatal("an audit row holds content")
	}
	etag := rowOf(t, st, f.cal.ID, "ser.ics").etag
	o := f.read(t, "ser.ics")
	cal, _ := ical.NewDecoder(bytes.NewReader(o.Data)).Decode()
	if len(cal.Children) != 41 {
		t.Fatalf("%d components, want every readable change (41)", len(cal.Children))
	}
	if loc := textOf(cal.Children[16], ical.PropLocation); loc != "Room 15" {
		t.Fatalf("the 16th patch: %q", loc)
	}
	if loc := textOf(cal.Children[17], ical.PropLocation); loc != "Room 42" {
		t.Fatalf("a dropped patch's occurrence shows %q, want the master's", loc)
	}
	if parts := partsOf(t, st, f.cal.ID, "ser.ics"); len(parts) != 1 || o.ETag != etag {
		t.Fatalf("the read did not fold to one base with the same ETag (%d parts)", len(parts))
	}
}

// Twenty room links to an unopened meeting leave one conference patch with the latest link, and
// the description stays the user's (E5).
func TestRoomLinksReplaceEachOther(t *testing.T) {
	st := newTestStore(t)
	f := newContentFixture(t, st, "usr_rl", true)
	if _, err := f.put(t, f.unlocked(), "mtg.ics", sealedEvent); err != nil {
		t.Fatal(err)
	}
	for i := range 20 {
		url := fmt.Sprintf("https://messages.example.com/r/%d", i)
		wc := &store.WorkerContent{Patch: []skeleton.Op{{Op: "conference", URL: url}}}
		if err := f.workerEdit(t, "mtg.ics", func(cal *ical.Calendar) { calendar.SetConference(cal, url) }, wc); err != nil {
			t.Fatal(err)
		}
		r := rowOf(t, st, f.cal.ID, "mtg.ics")
		noPlaintext(t, r.data)
		if bytes.Contains(r.data, []byte("messages.example.com")) {
			t.Fatal("the room link reached data")
		}
	}
	if n := len(partsOf(t, st, f.cal.ID, "mtg.ics")); n != 2 {
		t.Fatalf("%d parts, want the base and one conference patch", n)
	}
	if r := rowOf(t, st, f.cal.ID, "mtg.ics"); *r.plainSize < int64(len(f.read(t, "mtg.ics").Data)) {
		t.Fatal("plain_size counts less than the merged object")
	}
	ev := eventOf(t, f.read(t, "mtg.ics").Data, "")
	if got := calendar.Conference(ev); got != "https://messages.example.com/r/19" {
		t.Fatalf("room link %q", got)
	}
	if d := textOf(ev, ical.PropDescription); d != "Merger with Acme\n\nJoin: https://messages.example.com/r/19" || textOf(ev, ical.PropSummary) != "Secret board meeting" {
		t.Fatalf("description %q", d)
	}
	// Removing the room takes both out again.
	wc := &store.WorkerContent{Patch: []skeleton.Op{{Op: "conference"}}}
	if err := f.workerEdit(t, "mtg.ics", func(cal *ical.Calendar) { calendar.SetConference(cal, "") }, wc); err != nil {
		t.Fatal(err)
	}
	ev = eventOf(t, f.read(t, "mtg.ics").Data, "")
	if calendar.Conference(ev) != "" || textOf(ev, ical.PropDescription) != "Merger with Acme" {
		t.Fatal("removing the room left it")
	}
	sizeIsMerged(t, f, "mtg.ics", "after removing the room")
}

// Patches on an object 2 KiB under MaxObjectSize are refused once they would pass it, so the
// fold after them stays under the limit and the user's next save succeeds. A worker set of any
// property but LOCATION is refused before anything is sealed.
func TestWorkerPatchesKeepUnderTheLimit(t *testing.T) {
	st := newTestStore(t)
	f := newContentFixture(t, st, "usr_lim", true)
	body, target := sealedEvent, calendar.MaxObjectSize-2048
	for target-len(body) > 300 {
		body = strings.Replace(body, "END:VEVENT", fold("X-PAD:"+strings.Repeat("p", min(60000, (target-len(body)-300)*74/77)))+"END:VEVENT", 1)
	}
	for target-len(body) >= 62 {
		body = strings.Replace(body, "END:VEVENT", "X-PAD-END:"+strings.Repeat("q", 50)+"\r\nEND:VEVENT", 1)
	}
	if len(body) > target || len(body) < target-300 {
		t.Fatalf("padding: %d bytes, want just under %d", len(body), target)
	}
	if _, err := f.put(t, f.unlocked(), "big.ics", body); err != nil {
		t.Fatal(err)
	}
	// A 1,500-byte location fits; a long room link on top of it would not.
	if err := f.workerEdit(t, "big.ics", func(*ical.Calendar) {}, setLocation("", strings.Repeat("l", 1500))); err != nil {
		t.Fatal(err)
	}
	link := &store.WorkerContent{Patch: []skeleton.Op{{Op: "conference", URL: "https://messages.example.com/r/" + strings.Repeat("u", 1500)}}}
	if err := f.workerEdit(t, "big.ics", func(*ical.Calendar) {}, link); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM audit_records WHERE action = 'content_part_dropped' AND details = 'reason=size'`); n != 1 {
		t.Fatalf("%d patches refused for size, want the room link", n)
	}
	if n := len(partsOf(t, st, f.cal.ID, "big.ics")); n != 2 {
		t.Fatalf("%d parts, want the base and the location", n)
	}
	o := f.read(t, "big.ics")
	if len(o.Data) > calendar.MaxObjectSize {
		t.Fatalf("the fold made %d bytes", len(o.Data))
	}
	if _, err := f.put(t, f.unlocked(), "big.ics", string(o.Data)); err != nil {
		t.Fatalf("the user's next save: %v", err)
	}
	before := rowOf(t, st, f.cal.ID, "big.ics")
	bad := &store.WorkerContent{Patch: []skeleton.Op{{Op: "set", Component: "VEVENT", Property: "SUMMARY", Values: []skeleton.PropValue{{Value: "x"}}}}}
	if err := f.workerEdit(t, "big.ics", func(*ical.Calendar) {}, bad); !errors.Is(err, store.ErrWorkerPatch) {
		t.Fatalf("a worker SUMMARY patch: %v", err)
	}
	if after := rowOf(t, st, f.cal.ID, "big.ics"); after.etag != before.etag {
		t.Fatal("a refused patch changed the object")
	}
}

// A fold racing a worker's patch or a client's new body never loses either: it is skipped,
// and the next read folds what is there (E4).
func TestFoldNeverOverwritesALaterWrite(t *testing.T) {
	st := newTestStore(t)
	f := newContentFixture(t, st, "usr_fr", true)
	if _, err := f.put(t, f.unlocked(), "mtg.ics", sealedEvent); err != nil {
		t.Fatal(err)
	}
	if err := f.workerEdit(t, "mtg.ics", func(*ical.Calendar) {}, setLocation("", "Room 1")); err != nil {
		t.Fatal(err)
	}
	restore := store.SetFoldHookForTest(func() {
		if err := f.workerEdit(t, "mtg.ics", func(*ical.Calendar) {}, setLocation("", "Room 2")); err != nil {
			t.Error(err)
		}
	})
	f.read(t, "mtg.ics")
	restore()
	if got := textOf(eventOf(t, f.read(t, "mtg.ics").Data, ""), ical.PropLocation); got != "Room 2" {
		t.Fatalf("a fold racing a patch lost it: %q", got)
	}
	if err := f.workerEdit(t, "mtg.ics", func(*ical.Calendar) {}, setLocation("", "Room 3")); err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(sealedEvent, "Secret board meeting", "Secret board meeting, moved", 1)
	restore = store.SetFoldHookForTest(func() {
		if _, err := f.put(t, f.unlocked(), "mtg.ics", edited); err != nil {
			t.Error(err)
		}
	})
	f.read(t, "mtg.ics")
	restore()
	if got := f.read(t, "mtg.ics"); string(got.Data) != edited {
		t.Fatalf("a fold racing a client's body: %s", got.Data)
	}
}

// fold is line folded at 75 octets as RFC 5545 asks and Merge writes it, with its CRLF.
func fold(line string) string {
	var b strings.Builder
	limit := 75
	for len(line) > limit {
		b.WriteString(line[:limit] + "\r\n ")
		line, limit = line[limit:], 74
	}
	return b.String() + line + "\r\n"
}

// A patch dropped at the cap or by a quota is not sent to attendees either: the messages a skeleton-planned
// write queues carry only the patch the organizer's event stored.
func TestADroppedPatchIsNotQueued(t *testing.T) {
	st := newTestStore(t)
	f := newContentFixture(t, st, "usr_dq", true)
	if _, err := f.put(t, f.unlocked(), "ser.ics", series); err != nil {
		t.Fatal(err)
	}
	for i := range 16 {
		rid := time.Date(2026, 12, 1+i, 9, 0, 0, 0, time.UTC).Format("20060102T150405Z")
		if err := f.workerEdit(t, "ser.ics", addOverride(rid), setLocation(rid, fmt.Sprintf("Room %d", i))); err != nil {
			t.Fatal(err)
		}
	}
	f.plan = func([]byte) ([]byte, []*store.OutboxMessage, error) {
		return nil, []*store.OutboxMessage{{ID: "msg_dq", DedupeKey: "dq", SenderID: f.user, Method: "REQUEST", Recipient: "mailto:ana@example.com", Body: []byte("x")}}, nil
	}
	rid := "20261220T090000Z"
	if err := f.workerEdit(t, "ser.ics", addOverride(rid), setLocation(rid, "Dropped room")); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM audit_records WHERE action = 'content_part_dropped' AND details = 'reason=cap'`); n != 1 {
		t.Fatalf("%d patches dropped at the cap, want 1", n)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM scheduling_outbox WHERE id = 'msg_dq' AND worker_patch = '[]'`); n != 1 {
		t.Fatal("the queued message does not carry an empty patch")
	}
	// The same for a patch the owner's byte quota refuses after the messages were planned.
	g := newContentFixture(t, st, "usr_dq2", true)
	if _, err := g.put(t, g.unlocked(), "mtg.ics", sealedEvent); err != nil {
		t.Fatal(err)
	}
	g.lim = store.OwnerLimits{MaxBytes: *rowOf(t, st, g.cal.ID, "mtg.ics").plainSize + 100}
	g.plan = func([]byte) ([]byte, []*store.OutboxMessage, error) {
		return nil, []*store.OutboxMessage{{ID: "msg_dq2", DedupeKey: "dq2", SenderID: g.user, Method: "REQUEST", Recipient: "mailto:ana@example.com", Body: []byte("x")}}, nil
	}
	if err := g.workerEdit(t, "mtg.ics", func(*ical.Calendar) {}, setLocation("", strings.Repeat("r", 1500))); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM audit_records WHERE action = 'content_part_dropped' AND details = 'reason=quota'`); n != 1 {
		t.Fatalf("%d patches dropped for quota, want 1", n)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM scheduling_outbox WHERE id = 'msg_dq2' AND worker_patch = '[]'`); n != 1 {
		t.Fatal("a quota-dropped patch was queued")
	}
}

// PostgreSQL: a fold during a worker's write, after the write read the parts it keeps, would
// delete worker keys those parts name, and the object could never be opened again. The fold
// waits for no writer and skips instead (E4).
func TestFoldNeverRacesAWriteInFlight(t *testing.T) {
	if os.Getenv("KY_TEST_POSTGRES_DSN") == "" {
		t.Skip("PostgreSQL only: SQLite's one connection serializes them")
	}
	st := newTestStore(t)
	f := newContentFixture(t, st, "usr_fw", true)
	if _, err := f.put(t, f.unlocked(), "mtg.ics", sealedEvent); err != nil {
		t.Fatal(err)
	}
	if err := f.workerEdit(t, "mtg.ics", func(*ical.Calendar) {}, setLocation("", "Room 1")); err != nil {
		t.Fatal(err)
	}
	once := false
	restore := store.SetBeforeObjectWriteForTest(func() {
		if !once {
			once = true
			f.read(t, "mtg.ics") // folds, unless the write in flight stops it
		}
	})
	wc := &store.WorkerContent{Patch: []skeleton.Op{{Op: "conference", URL: "https://messages.example.com/r/1"}}}
	if err := f.workerEdit(t, "mtg.ics", func(cal *ical.Calendar) { calendar.SetConference(cal, "https://messages.example.com/r/1") }, wc); err != nil {
		t.Fatal(err)
	}
	restore()
	ev := eventOf(t, f.read(t, "mtg.ics").Data, "")
	if textOf(ev, ical.PropLocation) != "Room 1" || calendar.Conference(ev) != "https://messages.example.com/r/1" {
		t.Fatalf("after a fold raced a write: %+v", ev.Props)
	}
}

// dropOverride removes the override of rid, as an occurrence-only CANCEL does to a copy.
func dropOverride(rid string) func(*ical.Calendar) {
	return func(cal *ical.Calendar) {
		kept := cal.Children[:0]
		for _, c := range cal.Children {
			if p := c.Props.Get(ical.PropRecurrenceID); p == nil || p.Value != rid {
				kept = append(kept, c)
			}
		}
		cal.Children = kept
	}
}

// A worker that drops an override, its own content in the base or the master's it inherited,
// with a room's location on it, takes that content out of plain_size before any unlocked read
// folds the object (E4).
func TestDroppedOverrideLeavesPlainSize(t *testing.T) {
	st := newTestStore(t)
	f := newContentFixture(t, st, "usr_do", true)
	rid := "20261202T090000Z"
	withOwn := strings.Replace(series, "END:VEVENT\r\n", "END:VEVENT\r\nBEGIN:VEVENT\r\nUID:ser\r\nRECURRENCE-ID:"+rid+"\r\nDTSTAMP:20261127T000000Z\r\nDTSTART:"+rid+"\r\n"+
		fold("SUMMARY:"+strings.Repeat("y", 8<<10))+"LOCATION:Room 9\r\nORGANIZER:mailto:me@example.com\r\nATTENDEE:mailto:me@example.com\r\nEND:VEVENT\r\n", 1)
	for _, inherited := range []bool{false, true} {
		name := fmt.Sprintf("ser-%v.ics", inherited)
		body := withOwn
		if inherited {
			body = series
		}
		body = strings.ReplaceAll(body, "UID:ser\r\n", fmt.Sprintf("UID:ser-%v\r\n", inherited))
		if _, err := f.put(t, f.unlocked(), name, body); err != nil {
			t.Fatal(err)
		}
		if inherited {
			if err := f.workerEdit(t, name, addOverride(rid), nil); err != nil {
				t.Fatal(err)
			}
		}
		if err := f.workerEdit(t, name, func(*ical.Calendar) {}, setLocation(rid, strings.Repeat("z", 1500))); err != nil {
			t.Fatal(err)
		}
		if err := f.workerEdit(t, name, dropOverride(rid), nil); err != nil {
			t.Fatal(err)
		}
		sizeIsMerged(t, f, name, fmt.Sprintf("after dropping the override (inherited %v)", inherited))
	}
}
