package store_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/contentkey"
	"github.com/Busnes-app/kycalendar/internal/skeleton"
	"github.com/Busnes-app/kycalendar/internal/store"
)

type snapshotRow struct {
	data, sealed []byte
	etag         string
}

// snapshot is every object row of cal by name, as stored.
func snapshot(t *testing.T, st store.Store, cal string) map[string]snapshotRow {
	t.Helper()
	db, q := store.DBForTest(st)
	rows, err := db.Query(q(`SELECT name, data, sealed, etag FROM calendar_objects WHERE calendar_id = ?`), cal)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]snapshotRow{}
	for rows.Next() {
		var name string
		var r snapshotRow
		if err := rows.Scan(&name, &r.data, &r.sealed, &r.etag); err != nil {
			t.Fatal(err)
		}
		out[name] = r
	}
	return out
}

// legacyCorpus writes n synthetic events and every real client fixture (Apple, Thunderbird,
// Tasks.org, the recurrence corpus) to f's calendar before setup, as an existing install holds
// them, and returns the bytes each name was written with.
func legacyCorpus(t *testing.T, f contentFixture, n int) map[string][]byte {
	t.Helper()
	ctx := context.Background()
	out := map[string][]byte{}
	for i := 0; i < n; i++ {
		body := strings.ReplaceAll(sealedEvent, "UID:ev1", fmt.Sprintf("UID:ev%03d", i))
		name := fmt.Sprintf("ev%03d.ics", i)
		o := &store.CalendarObject{CalendarID: f.cal.ID, Name: name, Component: "VEVENT", UID: fmt.Sprintf("ev%03d", i), Data: []byte(body), FirstStart: 1}
		if _, err := f.st.Calendars().PutObject(ctx, o, "", false, store.OwnerLimits{}); err != nil {
			t.Fatal(err)
		}
		out[name] = []byte(body)
	}
	for _, glob := range []string{"../tasks/testdata/*.ics", "../calendar/testdata/recurrence/*.ics"} {
		files, _ := filepath.Glob(glob)
		for _, file := range files {
			body, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			uid := strings.TrimSpace(uidOf(strings.ReplaceAll(string(body), "\n", "\r\n")))
			name := "fx-" + filepath.Base(file)
			o := &store.CalendarObject{CalendarID: f.cal.ID, Name: name, Component: "VEVENT", UID: uid, Data: body, FirstStart: 1}
			if uid == "" {
				continue
			}
			if _, err := f.st.Calendars().PutObject(ctx, o, "", false, store.OwnerLimits{}); err == nil {
				out[name] = body
			}
		}
	}
	return out
}

func setUp(t *testing.T, f contentFixture) {
	t.Helper()
	_, code, _ := contentkey.NewRecoveryCode()
	if err := f.st.Content().Setup(context.Background(), f.user, store.Setup{Passphrase: "correct horse battery", Recovery: code, Session: f.session, Transition: transitionKey, Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

// sweepAll runs passes until one seals nothing, and returns how many rows it sealed.
func sweepAll(t *testing.T, st store.Store) int {
	t.Helper()
	total := 0
	for pass := 0; pass < 100; pass++ {
		var at store.SweepCursor
		sealed := 0
		for {
			n, next, done, err := st.Content().SweepOnce(context.Background(), at, 7, feedKey)
			if err != nil {
				t.Fatal(err)
			}
			sealed, at = sealed+n, next
			if done {
				break
			}
		}
		if total += sealed; sealed == 0 {
			return total
		}
	}
	t.Fatal("the sweep did not finish")
	return 0
}

func countedUsage(t *testing.T, st store.Store) int64 {
	t.Helper()
	db, _ := store.DBForTest(st)
	var n int64
	if err := db.QueryRow(`SELECT bytes FROM calendar_usage WHERE id = 1`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// assertSealed: every row of cal is sealed, holds no content in data, no longer has its plaintext ETag, and an
// unlocked read returns the bytes it was written with.
func assertSealed(t *testing.T, f contentFixture, want map[string][]byte, before map[string]snapshotRow) {
	t.Helper()
	after := snapshot(t, f.st, f.cal.ID)
	for name, body := range want {
		r := after[name]
		if r.sealed == nil {
			t.Fatalf("%s is still plaintext after the sweep", name)
		}
		if own, err := skeleton.Split(r.data); err != nil || !bytes.Equal(own, r.data) {
			t.Fatalf("%s: data holds content properties:\n%s", name, r.data)
		}
		noPlaintext(t, r.data)
		// The legacy ETag is the plaintext's SHA-256: it must not survive the sweep.
		if sum := sha256.Sum256(body); r.etag == before[name].etag || r.etag == hex.EncodeToString(sum[:]) || r.etag != fmt.Sprintf("%x", sha256.Sum256(append(append([]byte{}, r.data...), r.sealed...))) {
			t.Fatalf("%s: the sweep kept a plaintext-derived ETag %q", name, r.etag)
		}
		got, err := f.st.Calendars().GetObject(f.unlocked(), f.cal.ID, name)
		if err != nil || got.Locked || !bytes.Equal(got.Data, body) {
			t.Fatalf("%s does not read back byte for byte after the sweep (%v)", name, err)
		}
	}
}

func TestSweepSealsLegacyRowsResumablyAndOnce(t *testing.T) {
	st := newTestStore(t)
	a := newContentFixture(t, st, "usr_sa", false)
	b := newContentFixture(t, st, "usr_sb", false) // never runs setup
	want := legacyCorpus(t, a, 40)
	legacyCorpus(t, b, 5)
	beforeA, beforeB := snapshot(t, st, a.cal.ID), snapshot(t, st, b.cal.ID)
	used := countedUsage(t, st)
	setUp(t, a)
	if n, err := st.Content().LegacyRows(context.Background()); err != nil || n != len(want) {
		t.Fatalf("legacy rows before the sweep = %d (%v), want %d", n, err, len(want))
	}

	// Interrupt it at a random row of a random batch: that batch rolls back whole.
	seed := time.Now().UnixNano()
	r := rand.New(rand.NewPCG(uint64(seed), 1))
	stopAt := 1 + r.IntN(len(want)-1)
	t.Logf("interrupt after row %d (seed %d)", stopAt, seed)
	seen := 0
	restore := store.SetSweepHookForTest(func(int) error {
		if seen++; seen == stopAt {
			return errors.New("killed")
		}
		return nil
	})
	var at store.SweepCursor
	committed := 0
	for {
		n, next, done, err := st.Content().SweepOnce(context.Background(), at, 7, feedKey)
		if err != nil {
			break
		}
		committed, at = committed+n, next
		if done {
			t.Fatal("the interrupt never fired")
		}
	}
	restore()
	left, err := st.Content().LegacyRows(context.Background())
	if err != nil || left != len(want)-committed {
		t.Fatalf("after the interrupt %d legacy rows, want %d: the dead batch was not rolled back whole", left, len(want)-committed)
	}
	if countedUsage(t, st) != used {
		t.Fatal("an interrupted sweep moved counted usage")
	}

	// Rerun to the end, then run it again: the second run is a no-op.
	if n := sweepAll(t, st); n != left {
		t.Fatalf("the rerun sealed %d, want %d", n, left)
	}
	if n, _ := st.Content().LegacyRows(context.Background()); n != 0 {
		t.Fatalf("%d legacy rows left in keyed calendars", n)
	}
	assertSealed(t, a, want, beforeA)
	once := snapshot(t, st, a.cal.ID)
	if n := sweepAll(t, st); n != 0 {
		t.Fatalf("a second sweep sealed %d rows", n)
	}
	for name, row := range snapshot(t, st, a.cal.ID) {
		if !bytes.Equal(row.data, once[name].data) || !bytes.Equal(row.sealed, once[name].sealed) || row.etag != once[name].etag {
			t.Fatalf("the second sweep rewrote %s", name)
		}
	}
	if countedUsage(t, st) != used {
		t.Fatalf("the sweep moved counted usage: %d, want %d", countedUsage(t, st), used)
	}

	// An account that never set up is untouched and reads as before.
	for name, row := range snapshot(t, st, b.cal.ID) {
		if row.sealed != nil || !bytes.Equal(row.data, beforeB[name].data) || row.etag != beforeB[name].etag {
			t.Fatalf("the sweep touched %s of an account that never set up", name)
		}
		got, err := st.Calendars().GetObject(b.unlocked(), b.cal.ID, name)
		if err != nil || got.Locked || !bytes.Equal(got.Data, beforeB[name].data) {
			t.Fatalf("%s of a never-unlocked account is not readable as before", name)
		}
	}
}

// A row a client rewrites after the sweep read it is left for the next pass, never overwritten
// with the older bytes.
func TestSweepSkipsARowChangedUnderIt(t *testing.T) {
	st := newTestStore(t)
	a := newContentFixture(t, st, "usr_sc", false)
	legacyCorpus(t, a, 3)
	setUp(t, a)
	db, q := store.DBForTest(st)
	// The change lands between the sweep's read and its write: the write is conditional on
	// the ETag it read.
	restore := store.SetSweepReadHookForTest(func() {
		if _, err := db.Exec(q(`UPDATE calendar_objects SET etag = 'changed' WHERE calendar_id = ? AND name = 'ev002.ics'`), a.cal.ID); err != nil {
			t.Error(err)
		}
	})
	n, _, _, err := st.Content().SweepOnce(context.Background(), store.SweepCursor{}, 10, feedKey)
	restore()
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 || snapshot(t, st, a.cal.ID)["ev002.ics"].sealed != nil {
		t.Fatalf("sealed %d; the changed row was overwritten", n)
	}
}

// An unlocked rewrite of one swept object drops only that object's worker key: the objects
// swept beside it still open byte for byte, and no worker key is left without an object.
func TestSweptNeighboursSurviveARewrite(t *testing.T) {
	st := newTestStore(t)
	a := newContentFixture(t, st, "usr_sn", false)
	want := legacyCorpus(t, a, 2)
	setUp(t, a)
	sweepAll(t, st)
	if _, err := a.put(t, a.unlocked(), "ev000.ics", strings.ReplaceAll(sealedEvent, "UID:ev1", "UID:ev000")); err != nil {
		t.Fatal(err)
	}
	got, err := st.Calendars().GetObject(a.unlocked(), a.cal.ID, "ev001.ics")
	if err != nil || got.Locked || !bytes.Equal(got.Data, want["ev001.ics"]) {
		t.Fatalf("a neighbour of a rewritten object no longer opens: locked=%v %v", got.Locked, err)
	}
	keys := countRows(t, st, `SELECT COUNT(*) FROM worker_keys`)
	swept := countRows(t, st, `SELECT COUNT(*) FROM calendar_objects WHERE sealed IS NOT NULL`) - 1 // ev000 is now the holder's own base
	if keys != swept {
		t.Fatalf("%d worker keys for %d swept objects", keys, swept)
	}
}

// A subscribed calendar is left to its feed: the sweep does not seal it, so a refresh that
// rewrites its rows never sits beside a stale sealed base, and reads return the refresh.
// Subscribed calendars are sealed like any other once they have a key (CE3, feed refresh): the
// sweep seals a feed's legacy rows, a refresh writes each changed object as a sealed base, a
// row stored in the wrong format is written again, and an unchanged feed rewrites nothing.
// The ETag is an HMAC under the feed key, never the plaintext's digest.
func TestSubscribedCalendarsAreSealed(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	f := newContentFixture(t, st, "usr_sf", false)
	cs := st.Calendars()
	db, q := store.DBForTest(st)
	feed := &store.Calendar{ID: "cal_sf_feed", OwnerKind: "user", OwnerID: f.user, Slug: "feed", Name: "Feed"}
	if err := cs.CreateSubscribedCalendar(ctx, feed, &store.Subscription{URLSealed: "sealed", URLHost: "example.com", NextCheckAt: time.Now()}, 0, 0); err != nil {
		t.Fatal(err)
	}
	body := func(summary string) []byte {
		return []byte(strings.ReplaceAll(sealedEvent, "Secret board meeting", summary))
	}
	refresh := func(data []byte, key []byte) error {
		return cs.ReplaceSubscriptionObjects(ctx, feed.ID, []*store.CalendarObject{{Name: "ev1.ics", Component: "VEVENT", UID: "ev1", Data: data, FirstStart: 1}}, store.OwnerLimits{}, key)
	}
	seq := func() int64 {
		var n int64
		if err := db.QueryRow(q(`SELECT seq FROM calendars WHERE id = ?`), feed.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	plainETag := func(data []byte) string {
		sum := sha256.Sum256(data)
		return hex.EncodeToString(sum[:])
	}
	sealedAs := func(want []byte) storedRow {
		t.Helper()
		r := rowOf(t, st, feed.ID, "ev1.ics")
		if r.sealed == nil {
			t.Fatal("the feed's row is not sealed")
		}
		noPlaintext(t, r.data)
		if r.etag == plainETag(want) {
			t.Fatal("a sealed feed row keeps the plaintext's SHA-256 as its ETag")
		}
		o, err := cs.GetObject(f.unlocked(), feed.ID, "ev1.ics")
		if err != nil || o.Locked || !bytes.Equal(o.Data, want) {
			t.Fatalf("unlocked read: %v locked=%v %q", err, o != nil && o.Locked, o.Data)
		}
		return r
	}

	// Before setup the feed is stored as today.
	first := body("Secret board meeting")
	if err := refresh(first, nil); err != nil {
		t.Fatal(err)
	}
	if r := rowOf(t, st, feed.ID, "ev1.ics"); r.sealed != nil || r.etag != plainETag(first) {
		t.Fatal("an account without a key changed how its feed is stored")
	}
	setUp(t, f)
	if n, _ := st.Content().LegacyRows(ctx); n != 1 {
		t.Fatalf("legacy rows after setup: %d, want the feed's row", n)
	}

	// The sweep seals it with the ETag a refresh gives, so an unchanged refresh changes nothing.
	if sweepAll(t, st) != 1 {
		t.Fatal("the sweep did not seal the feed's row")
	}
	swept := sealedAs(first)
	at := seq()
	if err := refresh(first, feedKey); err != nil {
		t.Fatal(err)
	}
	if r := rowOf(t, st, feed.ID, "ev1.ics"); seq() != at || !bytes.Equal(r.sealed, swept.sealed) || r.etag != swept.etag {
		t.Fatal("an unchanged feed rewrote its swept row")
	}

	// A changed object is written as a new sealed base.
	second := body("Merger with Acme agenda")
	if err := refresh(second, feedKey); err != nil {
		t.Fatal(err)
	}
	changed := sealedAs(second)
	if changed.etag == swept.etag || seq() == at {
		t.Fatal("a changed feed object kept its ETag or logged no change")
	}

	// A row in the wrong format is written again on the next refresh: a legacy row even with the
	// right ETag, and plaintext beside parts with its SHA-256 ETag, as an older refresh left it.
	if _, err := db.Exec(q(`UPDATE calendar_objects SET data = ?, sealed = NULL WHERE calendar_id = ? AND name = ?`), second, feed.ID, "ev1.ics"); err != nil {
		t.Fatal(err)
	}
	if err := refresh(second, feedKey); err != nil {
		t.Fatal(err)
	}
	sealedAs(second)
	if _, err := db.Exec(q(`UPDATE calendar_objects SET data = ?, etag = ? WHERE calendar_id = ? AND name = ?`), second, plainETag(second), feed.ID, "ev1.ics"); err != nil {
		t.Fatal(err)
	}
	if err := refresh(second, feedKey); err != nil {
		t.Fatal(err)
	}
	sealedAs(second)
	if n, _ := st.Content().LegacyRows(ctx); n != 0 {
		t.Fatalf("legacy rows left: %d", n)
	}

	// Without the feed key a keyed calendar's refresh is refused, never stored in plaintext.
	if err := refresh(body("Third"), nil); err == nil {
		t.Fatal("a keyed feed refresh without the feed key succeeded")
	}
	sealedAs(second)
}

// A read that loaded a swept object just before a write replaced its base (and dropped the
// worker key the old base named) reads the object again instead of reporting it locked.
func TestReadAcrossAReplacedBase(t *testing.T) {
	st := newTestStore(t)
	f := newContentFixture(t, st, "usr_rb", false)
	legacyCorpus(t, f, 2)
	setUp(t, f)
	if sweepAll(t, st) == 0 {
		t.Fatal("nothing swept")
	}
	// ev001.ics is replaced by an object with another UID, which its parts are bound to.
	for name, uid := range map[string]string{"ev000.ics": "ev000", "ev001.ics": "other"} {
		stale, err := st.Calendars().GetObject(context.Background(), f.cal.ID, name)
		if err != nil || stale.Sealed == nil {
			t.Fatalf("swept object: %v", err)
		}
		edited := strings.ReplaceAll(strings.ReplaceAll(sealedEvent, "UID:ev1", "UID:"+uid), "Room 42", "Room 43")
		if _, err := f.put(t, f.unlocked(), name, edited); err != nil {
			t.Fatal(err)
		}
		if err := store.OpenObjectForTest(f.unlocked(), st, stale); err != nil || stale.Locked || string(stale.Data) != edited || stale.UID != uid {
			t.Fatalf("%s: read across a replaced base: %v locked=%v", name, err, stale.Locked)
		}
	}
}

// A feed subscribed after setup is keyed by its first refresh: the first import and every
// refresh after it are sealed, never stored in plaintext beside an account key.
func TestFeedSubscribedAfterSetupIsSealed(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	f := newContentFixture(t, st, "usr_sf2", true)
	cs := st.Calendars()
	feed := &store.Calendar{ID: "cal_sf2_feed", OwnerKind: "user", OwnerID: f.user, Slug: "feed", Name: "Feed"}
	if err := cs.CreateSubscribedCalendar(ctx, feed, &store.Subscription{URLSealed: "sealed", URLHost: "example.com", NextCheckAt: time.Now()}, 0, 0); err != nil {
		t.Fatal(err)
	}
	for _, summary := range []string{"Secret board meeting", "Merger with Acme agenda"} {
		data := []byte(strings.ReplaceAll(sealedEvent, "Secret board meeting", summary))
		if err := cs.ReplaceSubscriptionObjects(ctx, feed.ID, []*store.CalendarObject{{Name: "ev1.ics", Component: "VEVENT", UID: "ev1", Data: data, FirstStart: 1}}, store.OwnerLimits{}, feedKey); err != nil {
			t.Fatal(err)
		}
		r := rowOf(t, st, feed.ID, "ev1.ics")
		if r.sealed == nil {
			t.Fatalf("%q: a feed subscribed after setup is stored in plaintext", summary)
		}
		noPlaintext(t, r.data)
		o, err := cs.GetObject(f.unlocked(), feed.ID, "ev1.ics")
		if err != nil || o.Locked || !bytes.Equal(o.Data, data) {
			t.Fatalf("unlocked read: %v %q", err, o.Data)
		}
	}
}
