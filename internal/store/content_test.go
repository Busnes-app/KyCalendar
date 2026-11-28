package store_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/contentkey"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/store"
)

var transitionKey = crypto.DeriveKey([]byte("0123456789abcdef0123456789abcdef"), "kycalendar:content-transition")

const sealedEvent = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\nBEGIN:VEVENT\r\nUID:ev1\r\nDTSTAMP:20261127T000000Z\r\nDTSTART:20261201T090000Z\r\nDTEND:20261201T100000Z\r\nSUMMARY:Secret board meeting\r\nDESCRIPTION:Merger with Acme\r\nLOCATION:Room 42\r\nX-VENDOR-NOTE:private\r\nATTENDEE;CN=Ana;PARTSTAT=NEEDS-ACTION:mailto:ana@example.com\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

// plaintextMarkers are content values a sealed row's data must never hold.
var plaintextMarkers = []string{"Secret board meeting", "Merger with Acme", "Room 42", "private"}

type contentFixture struct {
	st      store.Store
	user    string
	cal     *store.Calendar
	session store.Credential
}

func secret(t *testing.T, s string) []byte {
	t.Helper()
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

// newContentFixture is a user with a personal calendar; setup runs only when setUp is true.
func newContentFixture(t *testing.T, st store.Store, id string, setUp bool) contentFixture {
	t.Helper()
	ctx := context.Background()
	if err := st.Users().CreateUser(ctx, &store.User{ID: id, Username: id, Role: "user", Status: "active", SSOProvider: "local"}); err != nil {
		t.Fatal(err)
	}
	c := &store.Calendar{ID: "cal_" + id, OwnerKind: "user", OwnerID: id, Slug: "default", Name: "Calendar"}
	if err := st.Calendars().CreateCalendar(ctx, c, 0); err != nil {
		t.Fatal(err)
	}
	f := contentFixture{st: st, user: id, cal: c, session: store.Credential{UserID: id, Kind: contentkey.SlotSession, SlotID: "sess-" + id, Secret: secret(t, "session "+id)}}
	liveSession(t, st, id, f.session.SlotID, time.Hour)
	if setUp {
		_, code, err := contentkey.NewRecoveryCode()
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Content().Setup(ctx, id, store.Setup{Passphrase: "correct horse battery", Recovery: code, Session: f.session, Transition: transitionKey, Now: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// liveSession inserts a session row, so rotation and pruning treat its slot as a live credential.
func liveSession(t *testing.T, st store.Store, user, tokenHash string, ttl time.Duration) {
	t.Helper()
	db, q := store.DBForTest(st)
	if _, err := db.Exec(q(`INSERT INTO sessions (token_hash, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`), tokenHash, user, time.Now().UTC(), time.Now().Add(ttl).UTC()); err != nil {
		t.Fatal(err)
	}
}

func liveAppPassword(t *testing.T, st store.Store, user, id string) {
	t.Helper()
	if err := st.AppPasswords().Create(context.Background(), store.Seed, &store.AppPassword{ID: id, UserID: user, Label: id, Hash: "x", Scope: store.ScopeDAV}, 0); err != nil {
		t.Fatal(err)
	}
}

func (f contentFixture) unlocked() context.Context {
	return f.st.Content().WithUnlock(context.Background(), f.session)
}

func (f contentFixture) put(t *testing.T, ctx context.Context, name, body string) (*store.CalendarObject, error) {
	t.Helper()
	o := &store.CalendarObject{CalendarID: f.cal.ID, Name: name, Component: "VEVENT", UID: uidOf(body), Data: []byte(body), FirstStart: 1}
	_, err := f.st.Calendars().PutObject(ctx, o, "", false, store.OwnerLimits{})
	return o, err
}

func uidOf(body string) string {
	_, rest, _ := strings.Cut(body, "\r\nUID:")
	uid, _, _ := strings.Cut(rest, "\r\n")
	return uid
}

type storedRow struct {
	data, sealed []byte
	etag         string
	plainSize    *int64
}

func rowOf(t *testing.T, st store.Store, cal, name string) storedRow {
	t.Helper()
	db, q := store.DBForTest(st)
	var r storedRow
	if err := db.QueryRow(q(`SELECT data, sealed, etag, plain_size FROM calendar_objects WHERE calendar_id = ? AND name = ?`), cal, name).Scan(&r.data, &r.sealed, &r.etag, &r.plainSize); err != nil {
		t.Fatal(err)
	}
	return r
}

func noPlaintext(t *testing.T, data []byte) {
	t.Helper()
	for _, m := range plaintextMarkers {
		if bytes.Contains(data, []byte(m)) {
			t.Fatalf("stored data holds content %q:\n%s", m, data)
		}
	}
}

func TestUnlockedWriteSealsAndReadReturnsTheBytesSent(t *testing.T) {
	st := newTestStore(t)
	f := newContentFixture(t, st, "usr_ana", true)
	ctx := f.unlocked()
	if _, err := f.put(t, ctx, "ev1.ics", sealedEvent); err != nil {
		t.Fatal(err)
	}
	row := rowOf(t, st, f.cal.ID, "ev1.ics")
	if row.sealed == nil {
		t.Fatal("an unlocked write to a keyed calendar stored no sealed parts")
	}
	noPlaintext(t, row.data)
	if !bytes.Contains(row.data, []byte("DTSTART:20261201T090000Z")) || !bytes.Contains(row.data, []byte("mailto:ana@example.com")) {
		t.Fatalf("the skeleton lost readable properties:\n%s", row.data)
	}
	if row.plainSize == nil || *row.plainSize != int64(len(sealedEvent)) {
		t.Fatalf("plain_size = %v, want %d", row.plainSize, len(sealedEvent))
	}
	sum := sha256.Sum256(append(append([]byte{}, row.data...), row.sealed...))
	if row.etag != hex.EncodeToString(sum[:]) {
		t.Fatal("a sealed row's ETag is not SHA-256 of data then sealed")
	}

	got, err := st.Calendars().GetObject(ctx, f.cal.ID, "ev1.ics")
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Data) != sealedEvent || got.Locked {
		t.Fatalf("unlocked read = %q (locked %v), want the bytes sent", got.Data, got.Locked)
	}
	list, err := st.Calendars().ListObjects(ctx, f.cal.ID)
	if err != nil || len(list) != 1 || string(list[0].Data) != sealedEvent {
		t.Fatalf("unlocked list did not merge: %v", err)
	}

	// A worker reads the skeleton only, and is not told it is locked.
	worker, err := st.Calendars().GetObject(context.Background(), f.cal.ID, "ev1.ics")
	if err != nil {
		t.Fatal(err)
	}
	noPlaintext(t, worker.Data)
	if worker.Locked {
		t.Fatal("a worker read is marked locked")
	}

	// A session without its own slot cannot open it, and is told so (E10).
	other := st.Content().WithUnlock(context.Background(), store.Credential{UserID: f.user, Kind: contentkey.SlotSession, SlotID: "sess-other", Secret: secret(t, "other")})
	locked, err := st.Calendars().GetObject(other, f.cal.ID, "ev1.ics")
	if err != nil {
		t.Fatal(err)
	}
	if !locked.Locked || store.RefuseLocked(locked) == nil {
		t.Fatal("a credential without a slot read an encrypted object without being refused")
	}
	noPlaintext(t, locked.Data)
	if _, err := f.put(t, other, "ev2.ics", strings.ReplaceAll(sealedEvent, "UID:ev1", "UID:ev2")); !errors.Is(err, store.ErrLocked) {
		t.Fatalf("a locked credential wrote to an encrypted calendar: %v", err)
	}
}

func TestLegacyAccountsAreUnchanged(t *testing.T) {
	st := newTestStore(t)
	f := newContentFixture(t, st, "usr_bo", false)
	ctx := f.st.Content().WithUnlock(context.Background(), f.session)
	if _, err := f.put(t, ctx, "ev1.ics", sealedEvent); err != nil {
		t.Fatal(err)
	}
	row := rowOf(t, st, f.cal.ID, "ev1.ics")
	sum := sha256.Sum256([]byte(sealedEvent))
	if row.sealed != nil || string(row.data) != sealedEvent || row.plainSize != nil || row.etag != hex.EncodeToString(sum[:]) {
		t.Fatalf("an account without a key stored something other than today's row: sealed=%v etag=%s", row.sealed != nil, row.etag)
	}
	got, err := st.Calendars().GetObject(ctx, f.cal.ID, "ev1.ics")
	if err != nil || string(got.Data) != sealedEvent || got.Locked {
		t.Fatalf("legacy read changed: %v", err)
	}
}

func TestWorkersNeverStorePlaintextInAKeyedCalendar(t *testing.T) {
	st := newTestStore(t)
	f := newContentFixture(t, st, "usr_cy", true)
	worker := context.Background()

	// A worker's new object (a delivered invitation) is sealed to the calendar's public key.
	if _, err := f.put(t, worker, "inv.ics", sealedEvent); err != nil {
		t.Fatal(err)
	}
	row := rowOf(t, st, f.cal.ID, "inv.ics")
	if row.sealed == nil {
		t.Fatal("a worker's new object in a keyed calendar was stored in plaintext")
	}
	noPlaintext(t, row.data)
	got, err := st.Calendars().GetObject(f.unlocked(), f.cal.ID, "inv.ics")
	if err != nil || string(got.Data) != sealedEvent {
		t.Fatalf("the holder cannot read a worker's base: %v %q", err, got.Data)
	}

	// A worker rewriting it from what it reads (the skeleton, with a new PARTSTAT) changes the
	// readable properties only: the content survives and never reaches data.
	skel, err := st.Calendars().GetObject(worker, f.cal.ID, "inv.ics")
	if err != nil {
		t.Fatal(err)
	}
	replied := strings.Replace(string(skel.Data), "PARTSTAT=NEEDS-ACTION", "PARTSTAT=ACCEPTED", 1)
	replied = strings.Replace(replied, "END:VEVENT", "LOCATION:Leaked by a worker\r\nEND:VEVENT", 1)
	o := &store.CalendarObject{CalendarID: f.cal.ID, Name: "inv.ics", Component: "VEVENT", UID: "ev1", Data: []byte(replied), FirstStart: 1}
	if _, err := st.Calendars().PutObject(worker, o, "", false, store.OwnerLimits{}); err != nil {
		t.Fatal(err)
	}
	row = rowOf(t, st, f.cal.ID, "inv.ics")
	noPlaintext(t, row.data)
	if bytes.Contains(row.data, []byte("Leaked by a worker")) {
		t.Fatal("a worker put content into data")
	}
	got, err = st.Calendars().GetObject(f.unlocked(), f.cal.ID, "inv.ics")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Secret board meeting", "Merger with Acme", "PARTSTAT=ACCEPTED"} {
		if !bytes.Contains(got.Data, []byte(want)) {
			t.Fatalf("after a worker's readable change the merged object lacks %q:\n%s", want, got.Data)
		}
	}
}

func TestUsageCountsPlaintextSize(t *testing.T) {
	st := newTestStore(t)
	f := newContentFixture(t, st, "usr_di", true)
	if _, err := f.put(t, f.unlocked(), "ev1.ics", sealedEvent); err != nil {
		t.Fatal(err)
	}
	db, _ := store.DBForTest(st)
	var bytesUsed int64
	if err := db.QueryRow(`SELECT bytes FROM calendar_usage WHERE id = 1`).Scan(&bytesUsed); err != nil {
		t.Fatal(err)
	}
	if bytesUsed != int64(len(sealedEvent)) {
		t.Fatalf("calendar_usage = %d, want the plaintext size %d", bytesUsed, len(sealedEvent))
	}
	var org int64
	if err := db.QueryRow(`SELECT SUM(bytes) FROM org_usage`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	if org != int64(len(sealedEvent)) {
		t.Fatalf("org_usage = %d, want %d", org, len(sealedEvent))
	}
	// The same write, one byte over the cap, is refused exactly as a plaintext one is.
	o := &store.CalendarObject{CalendarID: f.cal.ID, Name: "ev1.ics", Component: "VEVENT", UID: "ev1", Data: []byte(sealedEvent), FirstStart: 1}
	if _, err := st.Calendars().PutObject(f.unlocked(), o, "", false, store.OwnerLimits{MaxTotalBytes: int64(len(sealedEvent))}); err != nil {
		t.Fatalf("a rewrite at the cap was refused: %v", err)
	}
	o.Data = []byte(strings.Replace(sealedEvent, "Room 42", "Room 420", 1))
	if _, err := st.Calendars().PutObject(f.unlocked(), o, "", false, store.OwnerLimits{MaxTotalBytes: int64(len(sealedEvent))}); !errors.Is(err, store.ErrQuotaExceeded) {
		t.Fatalf("a write one byte over the cap = %v", err)
	}
	if err := st.Calendars().DeleteObject(context.Background(), f.cal.ID, "ev1.ics", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT bytes FROM calendar_usage WHERE id = 1`).Scan(&bytesUsed); err != nil || bytesUsed != 0 {
		t.Fatalf("after delete calendar_usage = %d (%v)", bytesUsed, err)
	}
}

func TestAppPasswordsMoveOverDuringTheTransitionAndRotationEndsIt(t *testing.T) {
	st := newTestStore(t)
	f := newContentFixture(t, st, "usr_ed", true)
	if _, err := f.put(t, f.unlocked(), "ev1.ics", sealedEvent); err != nil {
		t.Fatal(err)
	}
	app := func(id string) context.Context {
		return st.Content().WithUnlock(context.Background(), store.Credential{UserID: f.user, Kind: contentkey.SlotAppPassword, SlotID: id, Secret: secret(t, "app "+id), Transition: transitionKey})
	}
	read := func(ctx context.Context) *store.CalendarObject {
		t.Helper()
		o, err := st.Calendars().GetObject(ctx, f.cal.ID, "ev1.ics")
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	liveAppPassword(t, st, f.user, "ap1")
	liveAppPassword(t, st, f.user, "ap2")
	liveSession(t, st, f.user, "sess-expired", -time.Minute)
	// Two existing app passwords both keep syncing through the transition: the second still
	// unlocks after the first has its own slot.
	for _, id := range []string{"ap1", "ap2", "ap1"} {
		if o := read(app(id)); o.Locked || string(o.Data) != sealedEvent {
			t.Fatalf("app password %s refused during the transition", id)
		}
	}
	db, q := store.DBForTest(st)
	var tWrapped, tAccountWrap []byte
	if err := db.QueryRow(q(`SELECT wrapped, account_wrap FROM key_slots WHERE user_id = ? AND kind = 'transition'`), f.user).Scan(&tWrapped, &tAccountWrap); err != nil {
		t.Fatal(err)
	}
	expired := store.Credential{UserID: f.user, Kind: contentkey.SlotSession, SlotID: "sess-expired", Secret: secret(t, "expired")}
	if err := st.Content().Unlock(context.Background(), store.Credential{UserID: f.user, Kind: contentkey.SlotPassphrase, SlotID: "passphrase", Secret: []byte("correct horse battery")}, expired); err != nil {
		t.Fatal(err)
	}
	if n, err := st.Content().RotateDue(context.Background(), time.Now(), transitionKey); err != nil || n != 0 {
		t.Fatalf("rotated before the date: %d %v", n, err)
	}
	if n, err := st.Content().RotateDue(context.Background(), time.Now().Add(store.TransitionDays*24*time.Hour+time.Minute), transitionKey); err != nil || n != 1 {
		t.Fatalf("RotateDue = %d, %v", n, err)
	}
	// Moved-over credentials, the session and older content all still open.
	for _, ctx := range []context.Context{app("ap1"), app("ap2"), f.unlocked()} {
		if o := read(ctx); o.Locked || string(o.Data) != sealedEvent {
			t.Fatal("a credential with its own slot lost access at the rotation")
		}
	}
	// A credential that never moved over is refused now.
	if o := read(app("ap3")); !o.Locked {
		t.Fatal("an app password unused through the transition still unlocks")
	}
	// New content seals under version 2, which a copy of the transition slot cannot reach.
	if _, err := f.put(t, f.unlocked(), "ev2.ics", strings.ReplaceAll(sealedEvent, "UID:ev1", "UID:ev2")); err != nil {
		t.Fatal(err)
	}
	k, err := contentkey.SecretKEK(contentkey.SlotTransition, f.user, "transition", transitionKey)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := contentkey.OpenSlot(k, contentkey.SlotTransition, f.user, "transition", tWrapped)
	if err != nil {
		t.Fatal(err)
	}
	k0, err := contentkey.OpenAccount(pair, contentkey.SlotTransition, f.user, "transition", 1, tAccountWrap)
	if err != nil {
		t.Fatal(err)
	}
	var seeds = map[uint32][]byte{}
	rows, err := db.Query(q(`SELECT version, wrapped_seed FROM calendar_key_holders WHERE calendar_id = ? AND user_id = ?`), f.cal.ID, f.user)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var v uint32
		var w []byte
		if err := rows.Scan(&v, &w); err != nil {
			t.Fatal(err)
		}
		seeds[v] = w
	}
	rows.Close()
	if len(seeds) != 2 {
		t.Fatalf("calendar key versions after rotation = %d, want 2", len(seeds))
	}
	if _, err := contentkey.OpenCalendarSeed(k0, f.cal.ID, 2, f.user, seeds[2]); err == nil {
		t.Fatal("the transition-window account key opens the calendar key made at the rotation")
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM key_slots WHERE slot_id = 'sess-expired'`); n != 0 {
		t.Fatal("the rotation resealed the new account key to an expired session's slot")
	}
	if st, err := st.Content().Status(context.Background(), f.user, f.session.SlotID); err != nil || st.TransitionUntil != nil {
		t.Fatalf("status after the rotation still shows a transition: %+v %v", st, err)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM key_slots WHERE kind = 'transition'`); n != 0 {
		t.Fatalf("%d transition slots left after rotation", n)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM account_previous_keys`); n != 1 {
		t.Fatalf("previous account keys = %d, want 1", n)
	}
	// A repeat is a no-op.
	if err := st.Content().EndTransition(context.Background(), f.user, transitionKey); err != nil {
		t.Fatal(err)
	}
}

// The window closes at transition_until even when the rotation has not run yet.
func TestTransitionWindowClosesOnTime(t *testing.T) {
	st := newTestStore(t)
	f := newContentFixture(t, st, "usr_ew", true)
	if _, err := f.put(t, f.unlocked(), "ev1.ics", sealedEvent); err != nil {
		t.Fatal(err)
	}
	db, q := store.DBForTest(st)
	if _, err := db.Exec(q(`UPDATE account_keys SET transition_until = ? WHERE user_id = ?`), time.Now().Add(-time.Minute).UTC(), f.user); err != nil {
		t.Fatal(err)
	}
	late := st.Content().WithUnlock(context.Background(), store.Credential{UserID: f.user, Kind: contentkey.SlotAppPassword, SlotID: "late", Secret: secret(t, "late"), Transition: transitionKey})
	if o, err := st.Calendars().GetObject(late, f.cal.ID, "ev1.ics"); err != nil || !o.Locked {
		t.Fatalf("an app password reached the transition slot after its date: %v", err)
	}
}

// A swapped calendar_keys.public_key is refused for sealing (E13 check 2), so nothing is sealed
// to a key the operator chose.
func TestSwappedCalendarKeyIsRefused(t *testing.T) {
	st := newTestStore(t)
	f := newContentFixture(t, st, "usr_sw", true)
	other, err := contentkey.NewCalendarKey(f.cal.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	db, q := store.DBForTest(st)
	if _, err := db.Exec(q(`UPDATE calendar_keys SET public_key = ? WHERE calendar_id = ?`), other.PublicKey(), f.cal.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.put(t, f.unlocked(), "ev1.ics", sealedEvent); !errors.Is(err, store.ErrLocked) {
		t.Fatalf("a write sealed despite a swapped calendar key: %v", err)
	}
	// A worker would seal to the swapped key; it never gets the plaintext of an unlocked write.
}

func countRows(t *testing.T, st store.Store, query string) int {
	t.Helper()
	db, _ := store.DBForTest(st)
	var n int
	if err := db.QueryRow(query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestWebUnlockGivesTheSessionASlot(t *testing.T) {
	st := newTestStore(t)
	f := newContentFixture(t, st, "usr_fa", true)
	if _, err := f.put(t, f.unlocked(), "ev1.ics", sealedEvent); err != nil {
		t.Fatal(err)
	}
	next := store.Credential{UserID: f.user, Kind: contentkey.SlotSession, SlotID: "sess-2", Secret: secret(t, "second session")}
	wrong := store.Credential{UserID: f.user, Kind: contentkey.SlotPassphrase, SlotID: "passphrase", Secret: []byte("not the passphrase")}
	if err := st.Content().Unlock(context.Background(), wrong, next); !errors.Is(err, store.ErrLocked) {
		t.Fatalf("a wrong passphrase unlocked: %v", err)
	}
	right := store.Credential{UserID: f.user, Kind: contentkey.SlotPassphrase, SlotID: "passphrase", Secret: []byte("correct horse battery")}
	if err := st.Content().Unlock(context.Background(), right, next); err != nil {
		t.Fatal(err)
	}
	o, err := st.Calendars().GetObject(st.Content().WithUnlock(context.Background(), next), f.cal.ID, "ev1.ics")
	if err != nil || o.Locked || string(o.Data) != sealedEvent {
		t.Fatalf("the unlocked session cannot read: %v", err)
	}
	if st, err := st.Content().Status(context.Background(), f.user, "sess-2"); err != nil || !st.SetUp || !st.Unlocked || st.TransitionUntil == nil {
		t.Fatalf("status = %+v, %v", st, err)
	}
}

func TestGroupCalendarWaitsForEveryMember(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	a := newContentFixture(t, st, "usr_ga", false)
	b := newContentFixture(t, st, "usr_gb", false)
	db, q := store.DBForTest(st)
	gc := &store.Calendar{ID: "cal_team", OwnerKind: "group", OwnerID: "grp_g", Slug: "team", Name: "Team"}
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO groups (id, display_name, created_at, updated_at) VALUES ('grp_g', 'Team', ?, ?)`, []any{time.Now().UTC(), time.Now().UTC()}},
		{`INSERT INTO group_members (group_id, user_id) VALUES ('grp_g', ?), ('grp_g', ?)`, []any{a.user, b.user}},
	} {
		if _, err := db.Exec(q(stmt.sql), stmt.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Calendars().CreateCalendar(ctx, gc, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(q(`INSERT INTO calendar_grants (calendar_id, group_id, role) VALUES ('cal_team', 'grp_g', 'editor')`)); err != nil {
		t.Fatal(err)
	}
	setup := func(f contentFixture) {
		_, code, _ := contentkey.NewRecoveryCode()
		if err := st.Content().Setup(ctx, f.user, store.Setup{Passphrase: "correct horse battery", Recovery: code, Session: f.session, Transition: transitionKey, Now: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	setup(a)
	if n := countRows(t, st, `SELECT COUNT(*) FROM calendar_keys WHERE calendar_id = 'cal_team'`); n != 0 {
		t.Fatal("a group calendar got a key before every member ran setup")
	}
	// b has not run setup: b's writes and reads stay legacy and readable to b.
	o := &store.CalendarObject{CalendarID: gc.ID, Name: "t.ics", Component: "VEVENT", UID: "ev1", Data: []byte(sealedEvent), FirstStart: 1}
	if _, err := st.Calendars().PutObject(a.unlocked(), o, "", false, store.OwnerLimits{}); err != nil {
		t.Fatal(err)
	}
	if rowOf(t, st, gc.ID, "t.ics").sealed != nil {
		t.Fatal("a group calendar waiting for a member was sealed")
	}
	setup(b)
	if n := countRows(t, st, `SELECT COUNT(*) FROM calendar_key_holders WHERE calendar_id = 'cal_team'`); n != 2 {
		t.Fatalf("group calendar holders = %d, want 2", n)
	}
	if _, err := st.Calendars().PutObject(b.unlocked(), o, "", false, store.OwnerLimits{}); err != nil {
		t.Fatal(err)
	}
	for _, f := range []contentFixture{a, b} {
		got, err := st.Calendars().GetObject(f.unlocked(), gc.ID, "t.ics")
		if err != nil || got.Locked || string(got.Data) != sealedEvent {
			t.Fatalf("member %s cannot read the keyed group calendar: %v", f.user, err)
		}
	}
}

// TestRealClientObjectsRoundTrip: objects from real clients (Apple, Thunderbird, Tasks.org and
// the recurrence corpus) come back byte for byte from an encrypted account.
func TestRealClientObjectsRoundTrip(t *testing.T) {
	st := newTestStore(t)
	f := newContentFixture(t, st, "usr_ha", true)
	ctx := f.unlocked()
	var n int
	for _, glob := range []string{"../tasks/testdata/*.ics", "../calendar/testdata/recurrence/*.ics"} {
		files, _ := filepath.Glob(glob)
		for _, file := range files {
			body, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			uid := uidOf(strings.ReplaceAll(string(body), "\n", "\r\n"))
			if uid == "" || strings.Count(string(body), "\nUID:") != strings.Count(string(body), "\nUID:"+strings.TrimSpace(strings.TrimSuffix(uid, "\r"))) {
				continue
			}
			name := filepath.Base(file)
			o := &store.CalendarObject{CalendarID: f.cal.ID, Name: name, Component: "VEVENT", UID: strings.TrimSpace(uid), Data: body, FirstStart: 1}
			if _, err := st.Calendars().PutObject(ctx, o, "", false, store.OwnerLimits{}); err != nil {
				continue // not one storable object (a parse-failure fixture)
			}
			got, err := st.Calendars().GetObject(ctx, f.cal.ID, name)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got.Data, body) {
				t.Fatalf("%s did not round-trip:\n%s", name, got.Data)
			}
			if rowOf(t, st, f.cal.ID, name).sealed == nil {
				t.Fatalf("%s was not sealed", name)
			}
			n++
		}
	}
	if n < 10 {
		t.Fatalf("only %d fixtures round-tripped", n)
	}
}

// A calendar made after setup is keyed at its first write, whoever writes: a locked session is
// refused and a worker seals, so nothing reaches it in plaintext.
func TestNewCalendarAfterSetupNeverTakesPlaintext(t *testing.T) {
	st := newTestStore(t)
	f := newContentFixture(t, st, "usr_nc", true)
	ctx := context.Background()
	c2 := &store.Calendar{ID: "cal_nc2", OwnerKind: "user", OwnerID: f.user, Slug: "second", Name: "Second"}
	if err := st.Calendars().CreateCalendar(ctx, c2, 0); err != nil {
		t.Fatal(err)
	}
	put := func(ctx context.Context, name, uid string) error {
		o := &store.CalendarObject{CalendarID: c2.ID, Name: name, Component: "VEVENT", UID: uid, Data: []byte(strings.ReplaceAll(sealedEvent, "UID:ev1", "UID:"+uid)), FirstStart: 1}
		_, err := st.Calendars().PutObject(ctx, o, "", false, store.OwnerLimits{})
		return err
	}
	locked := st.Content().WithUnlock(ctx, store.Credential{UserID: f.user, Kind: contentkey.SlotSession, SlotID: "sess-none", Secret: secret(t, "none")})
	if err := put(locked, "a.ics", "a"); !errors.Is(err, store.ErrLocked) {
		t.Fatalf("a locked session wrote to a new calendar: %v", err)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM calendar_objects WHERE calendar_id = 'cal_nc2'`); n != 0 {
		t.Fatal("the refused write was stored")
	}
	if err := put(ctx, "b.ics", "b"); err != nil {
		t.Fatal(err)
	}
	noPlaintext(t, rowOf(t, st, c2.ID, "b.ics").data)
	if rowOf(t, st, c2.ID, "b.ics").sealed == nil {
		t.Fatal("a worker's write to a new calendar was not sealed")
	}
}

// Keying a calendar drops its plaintext task search rows at once (shared lists included).
func TestKeyingPurgesTaskSearchRows(t *testing.T) {
	st := newTestStore(t)
	f := newContentFixture(t, st, "usr_ts", false)
	ctx := context.Background()
	if _, err := st.Tasks().IndexTasks(ctx, f.cal.ID, []store.TaskDoc{{UID: "t1", ETag: "e", Body: "Secret board meeting"}}); err != nil {
		t.Fatal(err)
	}
	_, code, _ := contentkey.NewRecoveryCode()
	if err := st.Content().Setup(ctx, f.user, store.Setup{Passphrase: "correct horse battery", Recovery: code, Session: f.session, Transition: transitionKey, Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM task_search`); n != 0 {
		t.Fatalf("%d plaintext search rows survived keying", n)
	}
	if keyed, err := st.Tasks().IndexTasks(ctx, f.cal.ID, []store.TaskDoc{{UID: "t1", ETag: "e", Body: "Secret board meeting"}}); err != nil || !keyed {
		t.Fatalf("IndexTasks on a keyed calendar = %v %v", keyed, err)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM task_search`); n != 0 {
		t.Fatal("a keyed calendar was indexed")
	}
}

// A task search that read its list before keying cannot index it after: IndexTasks checks for
// a key under the calendar-key lock keying holds, so no plaintext row outlives keying.
func TestTaskIndexingCannotFollowKeying(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	doc := []store.TaskDoc{{UID: "t1", ETag: "e", Body: "Secret board meeting"}}

	// The search loaded its tasks, then setup keyed and purged the list, then the search indexes.
	f := newContentFixture(t, st, "usr_ix", true)
	if keyed, err := st.Tasks().IndexTasks(ctx, f.cal.ID, doc); err != nil || !keyed {
		t.Fatalf("IndexTasks after keying = %v %v", keyed, err)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM task_search`); n != 0 {
		t.Fatalf("%d plaintext search rows written after keying", n)
	}

	// The search checks while keying's transaction is still open: it must wait for its commit.
	g := newContentFixture(t, st, "usr_iy", false)
	commit, err := store.HoldLockForTest(st, "calendar-key:"+g.cal.ID)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		keyed bool
		err   error
	}
	done := make(chan result, 1)
	go func() {
		keyed, err := st.Tasks().IndexTasks(ctx, g.cal.ID, doc)
		done <- result{keyed, err}
	}()
	var early *result
	if st.Driver() == "postgres" { // SQLite's one connection already makes it wait
		for deadline := time.Now().Add(10 * time.Second); early == nil; {
			if waiting, err := store.WaitingOnLockForTest(st, "calendar-key:"+g.cal.ID); err != nil {
				t.Fatal(err)
			} else if waiting {
				break
			}
			select {
			case r := <-done:
				early = &r
			case <-time.After(5 * time.Millisecond):
			}
			if time.Now().After(deadline) {
				t.Fatal("IndexTasks neither finished nor waited for the lock")
			}
		}
	}
	if err := commit(`INSERT INTO calendar_keys (calendar_id, version, public_key) VALUES (?, 1, ?)`, g.cal.ID, []byte("pk")); err != nil {
		t.Fatal(err)
	}
	r := early
	if r == nil {
		got := <-done
		r = &got
	}
	if r.err != nil || !r.keyed {
		t.Fatalf("IndexTasks during keying = %v %v", r.keyed, r.err)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM task_search`); n != 0 {
		t.Fatalf("%d plaintext search rows written during keying", n)
	}
}

// A setup that fails while keying calendars leaves nothing behind, and the retry completes it.
func TestSetupFailingWhileKeyingRollsBack(t *testing.T) {
	st := newTestStore(t)
	f := newContentFixture(t, st, "usr_sf", false)
	ctx := context.Background()
	db, _ := store.DBForTest(st)
	fail, undo := `CREATE TRIGGER fail_keying BEFORE INSERT ON calendar_keys BEGIN SELECT RAISE(ABORT, 'keying failed'); END`, `DROP TRIGGER fail_keying`
	if st.Driver() == "postgres" {
		fail = `CREATE FUNCTION fail_keying() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'keying failed'; END $$;
CREATE TRIGGER fail_keying BEFORE INSERT ON calendar_keys FOR EACH ROW EXECUTE FUNCTION fail_keying()`
		undo = `DROP TRIGGER fail_keying ON calendar_keys; DROP FUNCTION fail_keying()`
	}
	if _, err := db.Exec(fail); err != nil {
		t.Fatal(err)
	}
	setup := func() error {
		_, code, _ := contentkey.NewRecoveryCode()
		return st.Content().Setup(ctx, f.user, store.Setup{Passphrase: "correct horse battery", Recovery: code, Session: f.session, Transition: transitionKey, Now: time.Now()})
	}
	if err := setup(); err == nil {
		t.Fatal("setup succeeded while keying failed")
	}
	if st2, err := st.Content().Status(ctx, f.user, f.session.SlotID); err != nil || st2.SetUp || st2.Unlocked {
		t.Fatalf("a failed setup left the account set up: %+v %v", st2, err)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM key_slots`); n != 0 {
		t.Fatalf("a failed setup left %d slots", n)
	}
	if _, err := db.Exec(undo); err != nil {
		t.Fatal(err)
	}
	if err := setup(); err != nil {
		t.Fatalf("retry after a failed setup: %v", err)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM calendar_keys WHERE calendar_id = '`+f.cal.ID+`'`); n != 1 {
		t.Fatalf("the retry left the calendar with %d keys", n)
	}
}

// A worker putting a different object under an existing sealed name seals it afresh.
func TestWorkerReplacingASealedNameWithANewUID(t *testing.T) {
	st := newTestStore(t)
	f := newContentFixture(t, st, "usr_ru", true)
	if _, err := f.put(t, f.unlocked(), "x.ics", sealedEvent); err != nil {
		t.Fatal(err)
	}
	next := strings.ReplaceAll(sealedEvent, "UID:ev1", "UID:ev9")
	if _, err := f.put(t, context.Background(), "x.ics", next); err != nil {
		t.Fatal(err)
	}
	got, err := st.Calendars().GetObject(f.unlocked(), f.cal.ID, "x.ics")
	if err != nil || got.Locked || string(got.Data) != next {
		t.Fatalf("after a new UID under the same name: locked=%v %v", got.Locked, err)
	}
}
