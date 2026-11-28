package store

// Content encryption at rest, build step CE2: keys, slots, sealing on write and merging on read
// (docs/superpowers/specs/2026-11-22-content-encryption-design.md). Formats are
// internal/contentkey's and internal/skeleton's; this file stores them.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-ical"

	"github.com/Busnes-app/kycalendar/internal/calendar"
	"github.com/Busnes-app/kycalendar/internal/contentkey"
	"github.com/Busnes-app/kycalendar/internal/skeleton"
	"github.com/Busnes-app/kycalendar/internal/store/migrations"
)

var (
	objBytes = migrations.ObjectBytes("calendar_objects")
	oBytes   = migrations.ObjectBytes("o")
)

// ErrLocked is a request whose credential cannot open the calendar's key (E10): it is refused,
// never served or allowed to write a skeleton.
var ErrLocked = errors.New("store: this calendar is encrypted and the credential cannot unlock it")

// ErrNoAccountKey is an account that has not run setup (E12).
var ErrNoAccountKey = errors.New("store: account has no content key")

// TransitionDays is how long existing app passwords and MCP tokens keep working through the
// transition slot (E11).
const TransitionDays = 14

// Credential is one request's unlock secret (E6). Transition is the deployment's
// content-transition key: set only for an app password or MCP token, which may open the
// account through the transition slot during the window.
type Credential struct {
	UserID     string
	Kind       contentkey.SlotKind
	SlotID     string
	Secret     []byte
	Transition []byte
}

// Unlock is a request's way to its account key, opened at most once per request. A context
// with none is a worker's or an anonymous request's: it reads skeletons and never seals (E4).
type Unlock struct {
	UserID string
	open   func(context.Context) (*contentkey.KeyPair, error)

	once    sync.Once
	account *contentkey.KeyPair
	err     error

	mu   sync.Mutex
	keys map[string]*contentkey.CalendarKey
}

type unlockKey struct{}

// WithUnlock attaches a request's credential to ctx.
func (s *SQLStore) WithUnlock(ctx context.Context, cred Credential) context.Context {
	return context.WithValue(ctx, unlockKey{}, &Unlock{UserID: cred.UserID, open: func(ctx context.Context) (*contentkey.KeyPair, error) {
		return s.openAccount(ctx, cred)
	}})
}

func unlockOf(ctx context.Context) *Unlock {
	u, _ := ctx.Value(unlockKey{}).(*Unlock)
	return u
}

// UnlockOf is ctx's request credential, nil for a worker or an anonymous request.
func UnlockOf(ctx context.Context) *Unlock { return unlockOf(ctx) }

// Account opens the request's account key. It reads the database, so it must not run inside an
// open SQLite transaction; calls in one use the key opened before it began.
func (u *Unlock) Account(ctx context.Context) (*contentkey.KeyPair, error) {
	u.once.Do(func() {
		a, err := u.open(ctx)
		u.mu.Lock()
		u.account, u.err = a, err
		u.mu.Unlock()
	})
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.account, u.err
}

type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// reopen opens the account key again, for a read that failed after a rotation replaced the key
// this request opened (E11). True when it changed. Outside any transaction only.
func (u *Unlock) reopen(ctx context.Context) bool {
	old, err := u.Account(ctx)
	if err != nil {
		return false
	}
	a, err := u.open(ctx)
	u.mu.Lock()
	defer u.mu.Unlock()
	if err != nil || bytes.Equal(a.PublicKey(), old.PublicKey()) {
		return false
	}
	u.account = a
	return true
}

// calendarKey opens version of calendarID's key through the request user's holder wrap.
func (u *Unlock) calendarKey(ctx context.Context, s *SQLStore, q rowQuerier, calendarID string, version uint32) (*contentkey.CalendarKey, error) {
	id := fmt.Sprintf("%s/%d", calendarID, version)
	u.mu.Lock()
	k, account, openErr := u.keys[id], u.account, u.err
	u.mu.Unlock()
	if account == nil || openErr != nil {
		return nil, ErrLocked
	}
	if k != nil {
		return k, nil
	}
	var wrapped []byte
	err := q.QueryRowContext(ctx, s.rebind(`SELECT wrapped_seed FROM calendar_key_holders WHERE calendar_id = ? AND version = ? AND user_id = ? AND NOT former`),
		calendarID, version, u.UserID).Scan(&wrapped)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrLocked
	} else if err != nil {
		return nil, err
	}
	k, err = contentkey.OpenCalendarSeed(account, calendarID, version, u.UserID, wrapped)
	if err != nil {
		return nil, ErrLocked
	}
	u.mu.Lock()
	if u.keys == nil {
		u.keys = map[string]*contentkey.CalendarKey{}
	}
	u.keys[id] = k
	u.mu.Unlock()
	return k, nil
}

// currentCalendarKey is the calendar's newest key version and public key; ok is false for a
// calendar without a key, whose rows stay legacy (E12).
func (s *SQLStore) currentCalendarKey(ctx context.Context, q rowQuerier, calendarID string) (version uint32, pub []byte, ok bool, err error) {
	err = q.QueryRowContext(ctx, s.rebind(`SELECT version, public_key FROM calendar_keys WHERE calendar_id = ? ORDER BY version DESC LIMIT 1`), calendarID).Scan(&version, &pub)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil, false, nil
	}
	return version, pub, err == nil, err
}

// objectETag is the SHA-256 of data, then sealed for a sealed row (Readers, ETags).
func objectETag(data, sealed []byte) string {
	h := sha256.New()
	h.Write(data)
	h.Write(sealed)
	return hex.EncodeToString(h.Sum(nil))
}

// ErrTooLarge is a write whose merged plaintext would pass calendar.MaxObjectSize: a worker's
// new base measured with the alarms it keeps (E4). It is refused as the same plaintext is.
var ErrTooLarge = errors.New("store: calendar object too large")

// ErrWorkerPatch is a patch a writer without the key may not make (E5): anything but a set of
// LOCATION or the room link. It is refused before anything is sealed.
var ErrWorkerPatch = errors.New("store: a worker may patch only LOCATION and the room link")

// maxWorkerPatches caps the patches written after an object's newest base (E4).
const maxWorkerPatches = 16

// WorkerContent is what a writer without the calendar's key changes in an existing sealed
// object's content (E4, E5), attached with WithWorkerContent. Without it, such a write changes
// readable properties only.
type WorkerContent struct {
	// Base: the written object's content replaces the stored content, as an organizer's update
	// replaces an attendee's copy. KeepAlarms keeps the alarms of the newest unmarked base.
	Base, KeepAlarms bool
	// Patch: operations on properties the writer never reads (skeleton.WorkerOp only).
	Patch []skeleton.Op
}

type workerContentKey struct{}

// WithWorkerContent attaches wc to the next object write in ctx.
func WithWorkerContent(ctx context.Context, wc *WorkerContent) context.Context {
	return context.WithValue(ctx, workerContentKey{}, wc)
}

func workerContentOf(ctx context.Context) *WorkerContent {
	wc, _ := ctx.Value(workerContentKey{}).(*WorkerContent)
	return wc
}

// sealedWrite is how a write stores its object: the bytes of data and the sealed columns.
type sealedWrite struct {
	data, sealed []byte
	version      int64
	plainSize    sql.NullInt64
	sizes        skeleton.Sizes // content_sizes (E4)
	alarms       int64          // alarm_bytes (E4)
	// skeletonOnly: a writer without the key over a sealed object, so messages it plans carry
	// only readable properties and patch (scheduling_outbox.worker_patch).
	skeletonOnly bool
	patch        []skeleton.Op
	dropKeys     []string     // worker keys to delete once this write is chosen
	dropped      string       // why a worker's content part was dropped: audited
	fallback     *sealedWrite // the same write without its patch, for a quota that refuses it
}

// counted is the size the write counts against quotas: the merged plaintext (E4).
func (w sealedWrite) counted() int64 {
	if w.plainSize.Valid {
		return w.plainSize.Int64
	}
	return int64(len(w.data))
}

func (w sealedWrite) sizesJSON() string {
	if len(w.sizes) == 0 {
		return "{}"
	}
	b, _ := json.Marshal(w.sizes)
	return string(b)
}

// measured is w with raw's sizes and alarms, the plaintext in hand (E4).
func (w sealedWrite) measured(raw []byte) sealedWrite {
	w.sizes, w.alarms, _ = skeleton.Measure(raw)
	return w
}

// prevObject is the stored row a write replaces.
type prevObject struct {
	uid          string
	data, sealed []byte
	size         sql.NullInt64
	sizes        skeleton.Sizes
	alarms       int64
}

// sealForWrite decides what o's write stores (E2, E4, E10). An unlocked holder seals the whole
// body as one new base. Any other writer to a sealed row changes readable properties only
// (its object's skeleton replaces data and the parts stay, so it can never put content in data
// or drop content it could not read), unless it brings WorkerContent: a new base, or a patch. A
// worker's new object in a calendar with a key is a base sealed to the calendar's public key.
func (c *calendarStore) sealForWrite(ctx context.Context, tx *sql.Tx, o *CalendarObject) (sealedWrite, error) {
	w := sealedWrite{data: o.Data}
	var prev prevObject
	var sizes string
	err := tx.QueryRowContext(ctx, c.q(`SELECT uid, data, sealed, parts_version, plain_size, content_sizes, alarm_bytes FROM calendar_objects WHERE calendar_id = ? AND name = ?`),
		o.CalendarID, o.Name).Scan(&prev.uid, &prev.data, &prev.sealed, &w.version, &prev.size, &sizes, &prev.alarms)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return w, err
	}
	_ = json.Unmarshal([]byte(sizes), &prev.sizes)
	if o.UID == "" {
		o.UID = prev.uid // an in-place rewrite keeps the object's UID
	}
	if err := c.store.keyCalendarTx(ctx, tx, o.CalendarID); err != nil {
		return w, err
	}
	version, pub, keyed, err := c.store.currentCalendarKey(ctx, tx, o.CalendarID)
	if err != nil {
		return w, err
	}
	if u := unlockOf(ctx); keyed && u != nil {
		k, err := u.calendarKey(ctx, c.store, tx, o.CalendarID, version)
		if err != nil {
			return w, err
		}
		if !k.Matches(pub) { // a swapped calendar_keys row (E13 check 2)
			return w, ErrLocked
		}
		skel, err := skeleton.Split(o.Data)
		if err != nil {
			return w, err
		}
		w.version++
		part, err := k.Seal(contentkey.Part{Kind: contentkey.PartBase, Seq: uint64(w.version)}, o.UID, o.Data)
		if err != nil {
			return w, err
		}
		if w.sealed, err = contentkey.EncodeParts([]contentkey.Part{part}); err != nil {
			return w, err
		}
		w.data, w.plainSize = skel, sql.NullInt64{Int64: int64(len(o.Data)), Valid: true}
		return w.measured(o.Data), c.dropWorkerKeys(ctx, tx, o.CalendarID, prev.sealed)
	}
	if prev.sealed != nil && o.UID != prev.uid && keyed {
		// A new object under the old name: the old parts are bound to the old UID.
		if err := c.dropWorkerKeys(ctx, tx, o.CalendarID, prev.sealed); err != nil {
			return w, err
		}
		return c.workerBase(ctx, tx, o, w, version, pub)
	}
	if prev.sealed != nil {
		readable, err := readableOnly(o, w, prev)
		if err != nil || !keyed {
			return readable, err
		}
		switch wc := workerContentOf(ctx); {
		case wc != nil && wc.Base:
			return c.workerSupersede(ctx, tx, o, w, prev, wc.KeepAlarms, version, pub)
		case wc != nil && len(wc.Patch) > 0:
			readable.patch = wc.Patch
			return c.workerPatch(ctx, tx, o, readable, prev, wc.Patch, version, pub)
		}
		return readable, nil
	}
	if keyed && unlockOf(ctx) == nil {
		return c.workerBase(ctx, tx, o, w, version, pub)
	}
	return w, nil
}

// readableOnly is a keyless write over a sealed row that changes readable properties alone: its
// skeleton replaces data and the parts stay. plain_size moves with the skeleton, and grows by
// the master's content for each override the writer added, which inherits it (E2, E4).
func readableOnly(o *CalendarObject, w sealedWrite, prev prevObject) (sealedWrite, error) {
	skel, err := skeleton.Split(o.Data)
	if err != nil {
		return w, err
	}
	size := prev.size.Int64 + int64(len(skel)) - int64(len(prev.data))
	added, err := skeleton.AddedOverrides(prev.data, skel)
	if err != nil {
		return w, err
	}
	for comp, n := range added {
		size += int64(n) * prev.sizes[skeleton.InheritLabel(comp)]
	}
	w.data, w.sealed, w.skeletonOnly = skel, prev.sealed, true
	w.plainSize = sql.NullInt64{Int64: size, Valid: true}
	w.sizes, w.alarms = prev.sizes, prev.alarms
	return w, nil
}

// sealWorker seals plaintext as part p of o under a fresh worker key wrapped to the calendar's
// current key version, used for this one part and ended at once, so it opens nothing else (E4).
func (c *calendarStore) sealWorker(ctx context.Context, tx *sql.Tx, o *CalendarObject, p contentkey.Part, plaintext []byte, version uint32, pub []byte) (contentkey.Part, error) {
	wk, err := contentkey.NewWorkerKey()
	if err != nil {
		return p, err
	}
	wrapped, err := wk.WrapFor(o.CalendarID, version, pub)
	if err != nil {
		return p, err
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, c.q(`INSERT INTO worker_keys (id, calendar_id, wrapped, created_at, ended_at, key_version) VALUES (?, ?, ?, ?, ?, ?)`),
		wk.ID, o.CalendarID, wrapped, now, now, version); err != nil {
		return p, err
	}
	return wk.Seal(p, o.CalendarID, version, o.UID, plaintext)
}

// workerBase seals a worker's whole new object to the calendar's public key (E4). A delivered
// invitation, an inbound iMIP message, a booking or a feed object in a calendar with a key is
// never stored in plaintext.
func (c *calendarStore) workerBase(ctx context.Context, tx *sql.Tx, o *CalendarObject, w sealedWrite, version uint32, pub []byte) (sealedWrite, error) {
	skel, err := skeleton.Split(o.Data)
	if err != nil {
		return w, err
	}
	w.version++
	part, err := c.sealWorker(ctx, tx, o, contentkey.Part{Kind: contentkey.PartBase, Seq: uint64(w.version)}, o.Data, version, pub)
	if err != nil {
		return w, err
	}
	if w.sealed, err = contentkey.EncodeParts([]contentkey.Part{part}); err != nil {
		return w, err
	}
	w.data, w.plainSize = skel, sql.NullInt64{Int64: int64(len(o.Data)), Valid: true}
	return w.measured(o.Data), nil
}

// workerSupersede writes a worker's new base over a sealed object (E4): an unmarked base
// replaces every part; a keep-alarms base (an organizer's update to an attendee's copy) keeps
// only the newest unmarked base, whose alarms it shows. Its plain_size counts its own content
// and the kept alarms, as today's plaintext copy holds them; past calendar.MaxObjectSize the
// write is refused (ErrTooLarge), never stored as a bare skeleton.
func (c *calendarStore) workerSupersede(ctx context.Context, tx *sql.Tx, o *CalendarObject, w sealedWrite, prev prevObject, keepAlarms bool, version uint32, pub []byte) (sealedWrite, error) {
	parts, err := contentkey.DecodeParts(prev.sealed)
	if err != nil {
		return w, err
	}
	var kept []contentkey.Part
	if keepAlarms {
		for _, p := range parts {
			if p.Kind == contentkey.PartBase && !p.KeepAlarms && (len(kept) == 0 || p.Seq > kept[0].Seq) {
				kept = []contentkey.Part{p}
			}
		}
	}
	for _, p := range parts {
		if p.WorkerKey != "" && (len(kept) == 0 || p.Seq != kept[0].Seq) {
			w.dropKeys = append(w.dropKeys, p.WorkerKey)
		}
	}
	skel, err := skeleton.Split(o.Data)
	if err != nil {
		return w, err
	}
	w = w.measured(o.Data)
	size := int64(len(o.Data))
	if len(kept) > 0 {
		size += prev.alarms - w.alarms // its own alarms are skeletons; the kept base's are counted
		w.alarms = prev.alarms
	}
	if size > calendar.MaxObjectSize {
		return w, ErrTooLarge
	}
	w.version++
	part, err := c.sealWorker(ctx, tx, o, contentkey.Part{Kind: contentkey.PartBase, KeepAlarms: len(kept) > 0, Seq: uint64(w.version)}, o.Data, version, pub)
	if err != nil {
		return w, err
	}
	if w.sealed, err = contentkey.EncodeParts(append(kept, part)); err != nil {
		return w, err
	}
	w.data, w.plainSize = skel, sql.NullInt64{Int64: size, Valid: true}
	return w, nil
}

// workerPatch appends ops as a patch to a sealed object (E4, E5) on top of readable, the same
// write's readable change. A patch with an earlier patch's label replaces it. Past 16 patches
// since the newest base, or past calendar.MaxObjectSize, the patch is dropped (readable stays)
// and audited; fallback drops it when the quota refuses it.
func (c *calendarStore) workerPatch(ctx context.Context, tx *sql.Tx, o *CalendarObject, readable sealedWrite, prev prevObject, ops []skeleton.Op, version uint32, pub []byte) (sealedWrite, error) {
	for _, op := range ops {
		if !skeleton.WorkerOp(op) {
			return readable, ErrWorkerPatch
		}
	}
	plaintext, err := skeleton.EncodePatch(ops)
	if err != nil {
		return readable, err
	}
	parts, err := contentkey.DecodeParts(prev.sealed)
	if err != nil {
		return readable, err
	}
	label := skeleton.Label(ops)
	var newest uint64
	for _, p := range parts {
		if p.Kind == contentkey.PartBase && p.Seq > newest {
			newest = p.Seq
		}
	}
	w := readable
	var kept []contentkey.Part
	patches := 0
	for _, p := range parts {
		if p.Kind == contentkey.PartPatch && p.Label == label {
			w.dropKeys = append(w.dropKeys, p.WorkerKey) // superseded: a later link or location
			continue
		}
		if p.Kind == contentkey.PartPatch && p.Seq > newest {
			patches++
		}
		kept = append(kept, p)
	}
	drop := func(reason string) (sealedWrite, error) {
		readable.dropped = reason
		return readable, nil
	}
	if patches >= maxWorkerPatches {
		return drop("cap")
	}
	events, err := skeleton.Count(readable.data, ical.CompEvent)
	if err != nil {
		return readable, err
	}
	sizes := skeleton.Sizes{}
	for k, v := range prev.sizes {
		sizes[k] = v
	}
	size := readable.plainSize.Int64
	for _, op := range ops {
		key := skeleton.ConferenceLabel
		if op.Op != "conference" {
			key = skeleton.LocationLabel(strings.ToUpper(op.Component), op.RecurrenceID)
		}
		old, ok := sizes[key]
		if !ok && op.Op != "conference" && op.RecurrenceID != "" {
			old = sizes[skeleton.LocationLabel(strings.ToUpper(op.Component), "")] // an override shows its master's
		}
		add := skeleton.Adds(op, events)
		size += add - old
		sizes[key] = add
	}
	if size > calendar.MaxObjectSize {
		return drop("size")
	}
	w.version++
	part, err := c.sealWorker(ctx, tx, o, contentkey.Part{Kind: contentkey.PartPatch, Label: label, Seq: uint64(w.version)}, plaintext, version, pub)
	if err != nil {
		return readable, err
	}
	if w.sealed, err = contentkey.EncodeParts(append(kept, part)); err != nil {
		return readable, err
	}
	w.plainSize, w.sizes = sql.NullInt64{Int64: size, Valid: true}, sizes
	fb := readable
	fb.dropped, fb.dropKeys = "quota", []string{part.WorkerKey}
	w.fallback = &fb
	return w, nil
}

// dropWorkerKeys deletes the worker keys the parts in sealed name, once a new base replaced
// them: each worker key here seals one object's parts only.
func (c *calendarStore) dropWorkerKeys(ctx context.Context, tx *sql.Tx, calendarID string, sealed []byte) error {
	if sealed == nil {
		return nil
	}
	parts, err := contentkey.DecodeParts(sealed)
	if err != nil {
		return nil // unreadable parts name nothing we could match
	}
	var ids []string
	for _, p := range parts {
		ids = append(ids, p.WorkerKey)
	}
	return c.deleteWorkerKeys(ctx, tx, calendarID, ids)
}

func (c *calendarStore) deleteWorkerKeys(ctx context.Context, tx *sql.Tx, calendarID string, ids []string) error {
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, c.q(`DELETE FROM worker_keys WHERE id = ? AND calendar_id = ?`), id, calendarID); err != nil {
			return err
		}
	}
	return nil
}

// stored finishes a chosen write inside tx: the worker keys it replaced go, and a dropped content
// part is audited with the object only, never the content (E4).
func (c *calendarStore) stored(ctx context.Context, tx *sql.Tx, calendarID, name string, w sealedWrite) error {
	if err := c.deleteWorkerKeys(ctx, tx, calendarID, w.dropKeys); err != nil {
		return err
	}
	if w.dropped == "" {
		return nil
	}
	var kind, owner string
	if err := tx.QueryRowContext(ctx, c.q(`SELECT owner_kind, owner_id FROM calendars WHERE id = ?`), calendarID).Scan(&kind, &owner); err != nil {
		return err
	}
	if kind != "user" {
		owner = "system"
	}
	return c.store.insertAudit(ctx, tx, AuditRecord{UserID: owner, Action: "content_part_dropped", Resource: "object:" + calendarID + "/" + name, Details: "reason=" + w.dropped})
}

// rewriteData is a worker's in-place rewrite of an object's data, under the rule sealForWrite
// applies to writers without a key.
func (c *calendarStore) rewriteData(ctx context.Context, tx *sql.Tx, calendarID, name string, data []byte) error {
	ctx = context.WithValue(ctx, unlockKey{}, (*Unlock)(nil)) // never the request's key: it may not hold this calendar
	ctx = WithWorkerContent(ctx, nil)
	w, err := c.sealForWrite(ctx, tx, &CalendarObject{CalendarID: calendarID, Name: name, Data: data})
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, c.q(`UPDATE calendar_objects SET data = ?, etag = ?, sealed = ?, parts_version = ?, plain_size = ?, content_sizes = ?, alarm_bytes = ?, modified_at = ? WHERE calendar_id = ? AND name = ?`),
		w.data, objectETag(w.data, w.sealed), w.sealed, w.version, w.plainSize, w.sizesJSON(), w.alarms, time.Now().UTC(), calendarID, name)
	if err != nil {
		return err
	}
	return c.stored(ctx, tx, calendarID, name, w)
}

// openObjects merges each sealed object for the request (E2). A request that cannot open one
// gets its skeleton marked Locked; a context without an Unlock gets the skeleton, as workers do.
// It reads the database, so callers close their rows first.
func (c *calendarStore) openObjects(ctx context.Context, list []*CalendarObject) error {
	u := unlockOf(ctx)
	for _, o := range list {
		if o.Sealed == nil || u == nil {
			continue
		}
		if _, err := u.Account(ctx); err != nil && !errors.Is(err, ErrLocked) && !errors.Is(err, ErrNoAccountKey) {
			return err
		}
		merged, err := c.openObject(ctx, u, o)
		switch {
		case errors.Is(err, ErrLocked):
			o.Locked = true
		case err != nil:
			return err
		default:
			if o.Sealed != nil {
				c.fold(ctx, u, o, merged)
			}
			o.Data = merged
		}
	}
	return nil
}

// foldHook, when set by a test, runs between a fold's read of the row and its write.
var foldHook func()

// fold replaces an object's parts with one base under the calendar's AES key once an unlocked
// read has merged them (E4), so patches and a second base never pile up on an object someone
// opens. It is best effort and changes neither the ETag nor what any reader is served. It
// writes only if the row still holds the parts, skeleton and parts_version it read, so a fold
// never puts stale content over a write that landed in between; the next read folds that.
func (c *calendarStore) fold(ctx context.Context, u *Unlock, o *CalendarObject, merged []byte) {
	parts, err := contentkey.DecodeParts(o.Sealed)
	if err != nil || len(parts) < 2 {
		return
	}
	version, pub, keyed, err := c.store.currentCalendarKey(ctx, c.store.db, o.CalendarID)
	if err != nil || !keyed {
		return
	}
	k, err := u.calendarKey(ctx, c.store, c.store.db, o.CalendarID, version)
	if err != nil || !k.Matches(pub) {
		return
	}
	var pv int64
	if err := c.store.db.QueryRowContext(ctx, c.q(`SELECT parts_version FROM calendar_objects WHERE calendar_id = ? AND name = ? AND sealed = ? AND data = ?`),
		o.CalendarID, o.Name, o.Sealed, o.Data).Scan(&pv); err != nil {
		return
	}
	// The newest part's sequence number is pv, so a later write's part (pv+1 on) stays newer.
	part, err := k.Seal(contentkey.Part{Kind: contentkey.PartBase, Seq: uint64(pv)}, o.UID, merged)
	if err != nil {
		return
	}
	sealed, err := contentkey.EncodeParts([]contentkey.Part{part})
	if err != nil {
		return
	}
	w := sealedWrite{}.measured(merged)
	if foldHook != nil {
		foldHook()
	}
	tx, err := c.store.beginTx(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, c.q(`UPDATE calendar_objects SET sealed = ?, plain_size = ?, content_sizes = ?, alarm_bytes = ?
WHERE calendar_id = ? AND name = ? AND parts_version = ? AND sealed = ? AND data = ?`),
		sealed, int64(len(merged)), w.sizesJSON(), w.alarms, o.CalendarID, o.Name, pv, o.Sealed, o.Data)
	if err != nil {
		return
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return
	}
	if c.dropWorkerKeys(ctx, tx, o.CalendarID, o.Sealed) != nil {
		return
	}
	if tx.Commit() == nil {
		o.Sealed = sealed
	}
}

func (c *calendarStore) openObject(ctx context.Context, u *Unlock, o *CalendarObject) ([]byte, error) {
	merged, err := c.openParts(ctx, u, o)
	if !errors.Is(err, errWorkerKeyGone) {
		return merged, err
	}
	// A write replaced the object after it was read and dropped the worker keys its old parts
	// named: read the object again, once.
	err = c.store.db.QueryRowContext(ctx, c.q(`SELECT uid, data, sealed, etag FROM calendar_objects WHERE calendar_id = ? AND name = ?`),
		o.CalendarID, o.Name).Scan(&o.UID, &o.Data, &o.Sealed, &o.ETag)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrLocked // deleted meanwhile
	} else if err != nil {
		return nil, err
	}
	if o.Sealed == nil {
		return o.Data, nil
	}
	merged, err = c.openParts(ctx, u, o)
	if errors.Is(err, errWorkerKeyGone) {
		return nil, ErrLocked
	}
	return merged, err
}

// errWorkerKeyGone: a part names a worker key that no longer exists.
var errWorkerKeyGone = errors.New("store: worker key gone")

func (c *calendarStore) openParts(ctx context.Context, u *Unlock, o *CalendarObject) ([]byte, error) {
	parts, err := contentkey.DecodeParts(o.Sealed)
	if err != nil {
		return nil, err
	}
	layers := make([]skeleton.Layer, 0, len(parts))
	for _, p := range parts {
		k, err := u.calendarKey(ctx, c.store, c.store.db, o.CalendarID, p.KeyVersion)
		if errors.Is(err, ErrLocked) && u.reopen(ctx) { // rotated since this request opened it
			k, err = u.calendarKey(ctx, c.store, c.store.db, o.CalendarID, p.KeyVersion)
		}
		if err != nil {
			return nil, err
		}
		gone := false
		pt, err := k.Open(p, o.UID, func(id string) ([]byte, error) {
			var wrapped []byte
			err := c.store.db.QueryRowContext(ctx, c.q(`SELECT wrapped FROM worker_keys WHERE id = ? AND calendar_id = ?`), id, o.CalendarID).Scan(&wrapped)
			gone = errors.Is(err, sql.ErrNoRows)
			return wrapped, err
		})
		if gone {
			return nil, errWorkerKeyGone
		}
		if err != nil {
			return nil, ErrLocked
		}
		layers = append(layers, skeleton.Layer{Base: p.Kind == contentkey.PartBase, KeepAlarms: p.KeepAlarms, Seq: p.Seq, Plaintext: pt})
	}
	return skeleton.Merge(o.Data, layers)
}

// Content is the store's content-key surface (E3, E6, E11, E12).
func (s *SQLStore) Content() ContentStore { return contentStore{s} }

// ContentStore holds account keys, slots and calendar keys.
type ContentStore interface {
	// WithUnlock attaches a request's credential, so its reads merge and its writes seal.
	WithUnlock(ctx context.Context, cred Credential) context.Context
	// Status is the account's setup state, and whether session slot sessionSlot exists; zero for
	// an account that has not run setup.
	Status(ctx context.Context, userID, sessionSlot string) (ContentStatus, error)
	// Setup creates userID's account key and its passphrase, recovery, transition and session
	// slots, and keys the calendars every holder can open (E12), in one transaction.
	Setup(ctx context.Context, userID string, s Setup) error
	// Unlock opens the account with a passphrase or recovery code and gives session its own
	// slot (Web flows, Unlock).
	Unlock(ctx context.Context, with Credential, session Credential) error
	// AddSlot gives a new credential (an app password made while unlocked) its own slot.
	AddSlot(ctx context.Context, account *contentkey.KeyPair, cred Credential) error
	// DeleteSlot removes a credential's slot with the credential.
	DeleteSlot(ctx context.Context, userID, slotID string) error
	// EnsureCalendarKeys keys every calendar userID can reach whose holders all have an account
	// key (E12).
	EnsureCalendarKeys(ctx context.Context, userID string, account *contentkey.KeyPair) error
	// RotateDue ends every transition that has passed (E11), and returns how many it rotated.
	RotateDue(ctx context.Context, now time.Time, transition []byte) (int, error)
	// EndTransition rotates userID now, at the user's request.
	EndTransition(ctx context.Context, userID string, transition []byte) error
	// PruneSlots drops the slots of credentials that no longer exist.
	PruneSlots(ctx context.Context) error
	// SweepOnce seals one batch of legacy rows in keyed calendars (content_sweep.go).
	SweepOnce(ctx context.Context, after SweepCursor, limit int) (int, SweepCursor, bool, error)
	// LegacyRows counts plaintext rows left in keyed calendars.
	LegacyRows(ctx context.Context) (int, error)
	// SetUpAccounts counts accounts with an account key.
	SetUpAccounts(ctx context.Context) (int, error)
	// PurgeFreed clears what deleted or replaced plaintext left in the database's files.
	PurgeFreed(ctx context.Context) error
}

func (c contentStore) SetUpAccounts(ctx context.Context) (int, error) {
	var n int
	err := c.s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM account_keys`).Scan(&n)
	return n, err
}

// ContentStatus is an account's setup state.
type ContentStatus struct {
	SetUp           bool
	Unlocked        bool // the asking session has its own slot
	TransitionUntil *time.Time
}

// Setup is what the setup flow chose (Web flows, Setup).
type Setup struct {
	Passphrase string
	Recovery   []byte // contentkey.ParseRecoveryCode's secret
	Session    Credential
	Transition []byte // the deployment's content-transition key
	Now        time.Time
}

type contentStore struct{ s *SQLStore }

func (c contentStore) WithUnlock(ctx context.Context, cred Credential) context.Context {
	return c.s.WithUnlock(ctx, cred)
}

type accountRow struct {
	public          []byte
	version         uint32
	transitionUntil sql.NullTime
}

func (s *SQLStore) accountKey(ctx context.Context, q rowQuerier, userID string) (accountRow, error) {
	var a accountRow
	err := q.QueryRowContext(ctx, s.rebind(`SELECT public_key, key_version, transition_until FROM account_keys WHERE user_id = ?`), userID).Scan(&a.public, &a.version, &a.transitionUntil)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNoAccountKey
	}
	return a, err
}

func (c contentStore) Status(ctx context.Context, userID, sessionSlot string) (ContentStatus, error) {
	a, err := c.s.accountKey(ctx, c.s.db, userID)
	if errors.Is(err, ErrNoAccountKey) {
		return ContentStatus{}, nil
	} else if err != nil {
		return ContentStatus{}, err
	}
	st := ContentStatus{SetUp: true}
	var n, transition int
	if err := c.s.db.QueryRowContext(ctx, c.s.rebind(`SELECT COUNT(*) FROM key_slots WHERE user_id = ? AND kind = 'transition'`), userID).Scan(&transition); err != nil {
		return st, err
	}
	if transition == 0 {
		a.transitionUntil.Valid = false // ended: rotated, or ended early
	}
	if err := c.s.db.QueryRowContext(ctx, c.s.rebind(`SELECT COUNT(*) FROM key_slots WHERE user_id = ? AND slot_id = ? AND kind = 'session'`), userID, sessionSlot).Scan(&n); err != nil {
		return st, err
	}
	st.Unlocked = n > 0
	if a.transitionUntil.Valid {
		t := a.transitionUntil.Time
		st.TransitionUntil = &t
	}
	return st, nil
}

// kek is the key a slot's private seed is wrapped under (E6). A passphrase slot keeps its
// Argon2id parameters in params.
func kek(cred Credential, params string) ([contentkey.SeedBytes]byte, error) {
	if cred.Kind == contentkey.SlotPassphrase || cred.Kind == contentkey.SlotPassword {
		var p contentkey.Argon2Params
		if err := json.Unmarshal([]byte(params), &p); err != nil {
			return [contentkey.SeedBytes]byte{}, err
		}
		return contentkey.PassphraseKEK(p, string(cred.Secret))
	}
	return contentkey.SecretKEK(cred.Kind, cred.UserID, cred.SlotID, cred.Secret)
}

// slotRow makes cred's slot for account (E6): a fresh key pair whose seed is wrapped under the
// credential's key, with the account key sealed to it.
type slotRow struct {
	kind                   contentkey.SlotKind
	slotID, params         string
	wrapped, pub, accountW []byte
	version                uint32
}

func newSlot(cred Credential, account *contentkey.KeyPair, version uint32) (slotRow, error) {
	r := slotRow{kind: cred.Kind, slotID: cred.SlotID, version: version}
	if cred.Kind == contentkey.SlotPassphrase {
		if len([]rune(string(cred.Secret))) < contentkey.MinPassphrase {
			return r, fmt.Errorf("store: a passphrase needs at least %d characters", contentkey.MinPassphrase)
		}
		p, err := contentkey.NewArgon2Params()
		if err != nil {
			return r, err
		}
		b, err := json.Marshal(p)
		if err != nil {
			return r, err
		}
		r.params = string(b)
	}
	k, err := kek(cred, r.params)
	if err != nil {
		return r, err
	}
	pair, wrapped, err := contentkey.NewSlot(k, cred.Kind, cred.UserID, cred.SlotID)
	if err != nil {
		return r, err
	}
	r.wrapped, r.pub = wrapped, pair.PublicKey()
	r.accountW, err = contentkey.SealAccountToSlot(r.pub, cred.Kind, cred.UserID, cred.SlotID, version, account)
	return r, err
}

func (s *SQLStore) insertSlot(ctx context.Context, tx *sql.Tx, userID string, r slotRow, now time.Time) error {
	_, err := tx.ExecContext(ctx, s.rebind(`INSERT INTO key_slots (user_id, slot_id, kind, wrapped, params, created_at, public_key, account_wrap, account_key_version)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (user_id, slot_id) DO NOTHING`),
		userID, r.slotID, string(r.kind), r.wrapped, r.params, now, r.pub, r.accountW, r.version)
	return err
}

// Recovery and passphrase slots have one per account; the transition slot too.
const (
	recoverySlot   = "recovery"
	passphraseSlot = "passphrase"
	transitionSlot = "transition"
)

func (c contentStore) Setup(ctx context.Context, userID string, in Setup) error {
	if len(in.Transition) == 0 {
		return errors.New("store: setup needs the deployment's transition key")
	}
	account, err := contentkey.NewKeyPair()
	if err != nil {
		return err
	}
	creds := []Credential{
		{UserID: userID, Kind: contentkey.SlotPassphrase, SlotID: passphraseSlot, Secret: []byte(in.Passphrase)},
		{UserID: userID, Kind: contentkey.SlotRecovery, SlotID: recoverySlot, Secret: in.Recovery},
		{UserID: userID, Kind: contentkey.SlotTransition, SlotID: transitionSlot, Secret: in.Transition},
		in.Session,
	}
	var rows []slotRow
	for _, cr := range creds {
		cr.UserID = userID
		r, err := newSlot(cr, account, 1)
		if err != nil {
			return err
		}
		rows = append(rows, r)
	}
	// No lock up front: the insert's unique key refuses a second setup, and keyCalendarTx takes
	// the holders' locks, this account's among them, in its own order.
	tx, err := c.s.beginTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	until := in.Now.UTC().Add(TransitionDays * 24 * time.Hour)
	res, err := tx.ExecContext(ctx, c.s.rebind(`INSERT INTO account_keys (user_id, public_key, created_at, transition_until, key_version) VALUES (?, ?, ?, ?, 1) ON CONFLICT (user_id) DO NOTHING`),
		userID, account.PublicKey(), in.Now.UTC(), until)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return cmpErr(err, ErrAlreadyExists)
	}
	for _, r := range rows {
		if err := c.s.insertSlot(ctx, tx, userID, r, in.Now.UTC()); err != nil {
			return err
		}
	}
	// Keyed in the same transaction: a setup that fails leaves nothing behind, so a retry works.
	ids, err := c.s.unkeyedCalendars(ctx, tx, userID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := c.s.keyCalendarTx(ctx, tx, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// openAccount opens cred's account key (E6). An app password or MCP token without its own slot
// opens the account through the transition slot while the transition lasts, and gets its own
// slot then (E11). Anything else without a slot is ErrLocked.
// betweenAccountAndSlot, when set by a test, runs between openAccount's two reads.
var betweenAccountAndSlot func()

func (s *SQLStore) openAccount(ctx context.Context, cred Credential) (*contentkey.KeyPair, error) {
	a, err := s.accountKey(ctx, s.db, cred.UserID)
	if err != nil {
		return nil, err
	}
	if betweenAccountAndSlot != nil {
		betweenAccountAndSlot()
	}
	account, err := s.openSlot(ctx, cred, a)
	if !errors.Is(err, sql.ErrNoRows) {
		return account, err
	}
	if (cred.Kind != contentkey.SlotAppPassword && cred.Kind != contentkey.SlotMCP) || len(cred.Transition) == 0 ||
		!a.transitionUntil.Valid || !time.Now().Before(a.transitionUntil.Time) {
		return nil, ErrLocked
	}
	account, err = s.openSlot(ctx, Credential{UserID: cred.UserID, Kind: contentkey.SlotTransition, SlotID: transitionSlot, Secret: cred.Transition}, a)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrLocked // rotated between the two reads
	} else if err != nil {
		return nil, err
	}
	return account, contentStore{s}.AddSlot(ctx, account, cred)
}

// openSlot opens the account through cred's own slot; sql.ErrNoRows when it has none.
func (s *SQLStore) openSlot(ctx context.Context, cred Credential, a accountRow) (*contentkey.KeyPair, error) {
	var wrapped, accountWrap []byte
	var params string
	var version uint32
	err := s.db.QueryRowContext(ctx, s.rebind(`SELECT wrapped, params, account_wrap, account_key_version FROM key_slots WHERE user_id = ? AND slot_id = ? AND kind = ?`),
		cred.UserID, cred.SlotID, string(cred.Kind)).Scan(&wrapped, &params, &accountWrap, &version)
	if err != nil {
		return nil, err
	}
	k, err := kek(cred, params)
	if err != nil {
		return nil, ErrLocked
	}
	pair, err := contentkey.OpenSlot(k, cred.Kind, cred.UserID, cred.SlotID, wrapped)
	if err != nil {
		return nil, ErrLocked
	}
	account, err := contentkey.OpenAccount(pair, cred.Kind, cred.UserID, cred.SlotID, version, accountWrap)
	if err != nil {
		return nil, ErrLocked
	}
	if version != a.version || !bytes.Equal(account.PublicKey(), a.public) {
		// A rotation may have committed between reading a and the slot: check the current key once.
		if a, err = s.accountKey(ctx, s.db, cred.UserID); err != nil || version != a.version || !bytes.Equal(account.PublicKey(), a.public) {
			return nil, ErrLocked
		}
	}
	_, _ = s.db.ExecContext(ctx, s.rebind(`UPDATE key_slots SET last_used_at = ? WHERE user_id = ? AND slot_id = ?`), time.Now().UTC(), cred.UserID, cred.SlotID)
	return account, nil
}

func (c contentStore) Unlock(ctx context.Context, with, session Credential) error {
	account, err := c.s.openAccount(ctx, with)
	if err != nil {
		return err
	}
	if err := c.AddSlot(ctx, account, session); err != nil {
		return err
	}
	return c.EnsureCalendarKeys(ctx, session.UserID, account)
}

func (c contentStore) AddSlot(ctx context.Context, account *contentkey.KeyPair, cred Credential) error {
	tx, err := c.s.lockedTx(ctx, "content:"+cred.UserID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	a, err := c.s.accountKey(ctx, tx, cred.UserID)
	if err != nil {
		return err
	}
	if !bytes.Equal(a.public, account.PublicKey()) {
		return ErrLocked // rotated since the request opened it
	}
	r, err := newSlot(cred, account, a.version)
	if err != nil {
		return err
	}
	if err := c.s.insertSlot(ctx, tx, cred.UserID, r, time.Now().UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

func (c contentStore) DeleteSlot(ctx context.Context, userID, slotID string) error {
	_, err := c.s.db.ExecContext(ctx, c.s.rebind(`DELETE FROM key_slots WHERE user_id = ? AND slot_id = ? AND kind IN ('apppass', 'mcp', 'session')`), userID, slotID)
	return err
}

// holders are the accounts that can read calendarID today: a personal calendar's owner, or the
// active members of the groups granted a group calendar. Rooms have none.
func (s *SQLStore) holders(ctx context.Context, tx *sql.Tx, calendarID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, s.rebind(`SELECT c.owner_id FROM calendars c WHERE c.id = ? AND c.owner_kind = 'user'
UNION SELECT m.user_id FROM calendars c JOIN calendar_grants g ON g.calendar_id = c.id JOIN group_members m ON m.group_id = g.group_id
 JOIN users u ON u.id = m.user_id WHERE c.id = ? AND c.owner_kind = 'group' AND u.status = 'active' AND u.role = 'user'`), calendarID, calendarID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (c contentStore) EnsureCalendarKeys(ctx context.Context, userID string, account *contentkey.KeyPair) error {
	ids, err := c.s.unkeyedCalendars(ctx, c.s.db, userID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := c.keyCalendar(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// unkeyedCalendars are the calendars userID can reach that have no key yet.
func (s *SQLStore) unkeyedCalendars(ctx context.Context, q queryer, userID string) ([]string, error) {
	rows, err := q.QueryContext(ctx, s.rebind(`SELECT c.id FROM calendars c WHERE c.owner_kind = 'user' AND c.owner_id = ?
 AND NOT EXISTS (SELECT 1 FROM calendar_keys k WHERE k.calendar_id = c.id)
UNION SELECT g.calendar_id FROM calendar_grants g JOIN group_members m ON m.group_id = g.group_id JOIN calendars c ON c.id = g.calendar_id
 WHERE m.user_id = ? AND c.owner_kind = 'group' AND NOT EXISTS (SELECT 1 FROM calendar_keys k WHERE k.calendar_id = g.calendar_id)`), userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// keyCalendar gives calendarID its first key once every holder has an account key (E12),
// wrapped to each of them. It is exposed while any holder is in its transition (E11).
func (c contentStore) keyCalendar(ctx context.Context, calendarID string) error {
	tx, err := c.s.beginTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := c.s.keyCalendarTx(ctx, tx, calendarID); err != nil {
		return err
	}
	return tx.Commit()
}

// keyCalendarTx is keyCalendar inside tx. It needs only holders' public keys, so every write
// runs it: a calendar made after setup is keyed before anything is written to it, whoever
// writes. Keying drops the calendar's plaintext task search rows (Readers, task search).
func (s *SQLStore) keyCalendarTx(ctx context.Context, tx *sql.Tx, calendarID string) error {
	if _, _, keyed, err := s.currentCalendarKey(ctx, tx, calendarID); err != nil || keyed {
		return err
	}
	holders, err := s.holders(ctx, tx, calendarID)
	if err != nil || len(holders) == 0 {
		return err
	}
	// Each holder's rotation lock, then the calendar's, as EndTransition takes them: no holder's
	// account key changes between reading it and wrapping the seed to it.
	sort.Strings(holders)
	for _, h := range holders {
		if err := s.lockKey(ctx, tx, "content:"+h); err != nil {
			return err
		}
	}
	if err := s.lockKey(ctx, tx, "calendar-key:"+calendarID); err != nil {
		return err
	}
	if _, _, keyed, err := s.currentCalendarKey(ctx, tx, calendarID); err != nil || keyed {
		return err
	}
	if holders, err = s.holders(ctx, tx, calendarID); err != nil || len(holders) == 0 {
		return err
	}
	k, err := contentkey.NewCalendarKey(calendarID, 1)
	if err != nil {
		return err
	}
	if err := s.wrapVersion(ctx, tx, calendarID, k, holders, true); err != nil {
		if errors.Is(err, ErrNoAccountKey) {
			return nil // a member has not run setup: the calendar waits for them (E12)
		}
		return err
	}
	// FTS5 secure-delete (migration 44) takes the terms out of the index's segments too.
	_, err = tx.ExecContext(ctx, s.rebind(`DELETE FROM task_search WHERE calendar_id = ?`), calendarID)
	return err
}

// PurgeFreed truncates SQLite's WAL, whose older frames still hold the plaintext that keying
// deleted and the sweep replaced. PostgreSQL: nothing (dead tuples, see the design's Residual
// risks).
func (c contentStore) PurgeFreed(ctx context.Context) error {
	if c.s.driver == "postgres" {
		return nil
	}
	var busy, logFrames, done int
	if err := c.s.db.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logFrames, &done); err != nil {
		return err
	}
	if busy != 0 {
		return errors.New("store: WAL checkpoint was blocked by a reader")
	}
	return nil
}

// wrapVersion stores calendar key version k, wrapped to every holder's current account key. With
// all false, a holder without one is skipped: a rotation's new version reaches every holder
// who can open the calendar today, and a member who has not run setup is locked out anyway (E12).
func (s *SQLStore) wrapVersion(ctx context.Context, tx *sql.Tx, calendarID string, k *contentkey.CalendarKey, holders []string, all bool) error {
	exposed := false
	type wrap struct {
		user string
		seed []byte
	}
	var wraps []wrap
	for _, h := range holders {
		a, err := s.accountKey(ctx, tx, h)
		if errors.Is(err, ErrNoAccountKey) && !all {
			continue
		} else if err != nil {
			return err
		}
		var n int
		if err := tx.QueryRowContext(ctx, s.rebind(`SELECT COUNT(*) FROM key_slots WHERE user_id = ? AND kind = 'transition'`), h).Scan(&n); err != nil {
			return err
		}
		exposed = exposed || n > 0
		seed, err := k.WrapSeed(h, a.public)
		if err != nil {
			return err
		}
		wraps = append(wraps, wrap{h, seed})
	}
	if _, err := tx.ExecContext(ctx, s.rebind(`INSERT INTO calendar_keys (calendar_id, version, public_key, exposed) VALUES (?, ?, ?, ?)`),
		calendarID, k.Version(), k.PublicKey(), exposed); err != nil {
		return err
	}
	for _, w := range wraps {
		if _, err := tx.ExecContext(ctx, s.rebind(`INSERT INTO calendar_key_holders (calendar_id, version, user_id, wrapped_seed) VALUES (?, ?, ?, ?)`),
			calendarID, k.Version(), w.user, w.seed); err != nil {
			return err
		}
	}
	return nil
}

func (c contentStore) RotateDue(ctx context.Context, now time.Time, transition []byte) (int, error) {
	rows, err := c.s.db.QueryContext(ctx, c.s.rebind(`SELECT a.user_id FROM account_keys a WHERE a.transition_until <= ?
 AND EXISTS (SELECT 1 FROM key_slots k WHERE k.user_id = a.user_id AND k.kind = 'transition')`), now.UTC())
	if err != nil {
		return 0, err
	}
	var due []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		due = append(due, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, id := range due {
		if err := c.EndTransition(ctx, id, transition); err != nil {
			return n, fmt.Errorf("rotate %s: %w", id, err)
		}
		n++
	}
	return n, nil
}

// PruneSlots drops slots whose credential is gone: an expired or signed-out session's, a
// revoked app password's. They open nothing anyone still holds.
func (c contentStore) PruneSlots(ctx context.Context) error {
	_, err := c.s.db.ExecContext(ctx, c.s.rebind(`DELETE FROM key_slots WHERE (kind = 'session' AND NOT EXISTS (SELECT 1 FROM sessions x WHERE x.token_hash = key_slots.slot_id AND x.expires_at > ?))
 OR (kind IN ('apppass', 'mcp') AND slot_id NOT IN (SELECT id FROM app_passwords))`), time.Now().UTC())
	return err
}

// EndTransition is E11's rotation, in one transaction: open K0 through the transition slot for
// the last time, make K1 and seal it to every other slot, seal K0 to K1, rewrap the account's
// calendar seeds to K1, give every calendar it holds a new key version wrapped to every holder's
// current key, and delete the transition slot. A run that dies changes nothing.
func (c contentStore) EndTransition(ctx context.Context, userID string, transition []byte) error {
	s := c.s
	a, err := s.accountKey(ctx, s.db, userID)
	if err != nil {
		return err
	}
	k0, err := s.openSlot(ctx, Credential{UserID: userID, Kind: contentkey.SlotTransition, SlotID: transitionSlot, Secret: transition}, a)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // already rotated
	} else if err != nil {
		return err
	}
	// The account lock first, then the calendars it holds, read under it: a calendar keyed while
	// this waited is in the list, and keyCalendarTx takes the same locks in the same order.
	tx, err := s.lockedTx(ctx, "content:"+userID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	calendars, err := s.heldCalendars(ctx, tx, userID)
	if err != nil {
		return err
	}
	for _, id := range calendars {
		if err := s.lockKey(ctx, tx, "calendar-key:"+id); err != nil {
			return err
		}
	}
	if now, err := s.accountKey(ctx, tx, userID); err != nil || !bytes.Equal(now.public, a.public) {
		return cmpErr(err, nil) // rotated by another replica since
	}
	// Only live credentials get K1: an expired or signed-out session's slot, or a revoked app
	// password's, is deleted here rather than resealed.
	if _, err := tx.ExecContext(ctx, s.rebind(`DELETE FROM key_slots WHERE user_id = ? AND (
 (kind = 'session' AND NOT EXISTS (SELECT 1 FROM sessions x WHERE x.token_hash = key_slots.slot_id AND x.user_id = key_slots.user_id AND x.expires_at > ?))
 OR (kind IN ('apppass', 'mcp') AND NOT EXISTS (SELECT 1 FROM app_passwords p WHERE p.id = key_slots.slot_id AND p.user_id = key_slots.user_id)))`), userID, time.Now().UTC()); err != nil {
		return err
	}
	k1, err := contentkey.NewKeyPair()
	if err != nil {
		return err
	}
	v1 := a.version + 1
	if err := s.resealSlots(ctx, tx, userID, k1, v1); err != nil {
		return err
	}
	prev, err := contentkey.SealPreviousAccount(k1.PublicKey(), userID, a.version, k0)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, s.rebind(`INSERT INTO account_previous_keys (user_id, key_version, sealed_seed) VALUES (?, ?, ?)`), userID, a.version, prev); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, s.rebind(`UPDATE account_keys SET public_key = ?, key_version = ?, transition_until = NULL WHERE user_id = ?`), k1.PublicKey(), v1, userID); err != nil {
		return err
	}
	if err := s.rewrapSeeds(ctx, tx, userID, k0, k1); err != nil {
		return err
	}
	for _, id := range calendars {
		version, _, keyed, err := s.currentCalendarKey(ctx, tx, id)
		if err != nil || !keyed {
			if err != nil {
				return err
			}
			continue
		}
		holders, err := s.holders(ctx, tx, id)
		if err != nil {
			return err
		}
		k, err := contentkey.NewCalendarKey(id, version+1)
		if err != nil {
			return err
		}
		if err := s.wrapVersion(ctx, tx, id, k, holders, false); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// heldCalendars are the calendars userID holds a current wrap of.
func (s *SQLStore) heldCalendars(ctx context.Context, tx *sql.Tx, userID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, s.rebind(`SELECT DISTINCT calendar_id FROM calendar_key_holders WHERE user_id = ? AND NOT former ORDER BY calendar_id`), userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// resealSlots seals K1 to every slot but the transition slot, which is deleted (E11 steps 2, 5).
func (s *SQLStore) resealSlots(ctx context.Context, tx *sql.Tx, userID string, k1 *contentkey.KeyPair, v1 uint32) error {
	rows, err := tx.QueryContext(ctx, s.rebind(`SELECT slot_id, kind, public_key FROM key_slots WHERE user_id = ? AND kind <> 'transition'`), userID)
	if err != nil {
		return err
	}
	type slot struct {
		id, kind string
		pub      []byte
	}
	var slots []slot
	for rows.Next() {
		var sl slot
		if err := rows.Scan(&sl.id, &sl.kind, &sl.pub); err != nil {
			rows.Close()
			return err
		}
		slots = append(slots, sl)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, sl := range slots {
		w, err := contentkey.SealAccountToSlot(sl.pub, contentkey.SlotKind(sl.kind), userID, sl.id, v1, k1)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, s.rebind(`UPDATE key_slots SET account_wrap = ?, account_key_version = ? WHERE user_id = ? AND slot_id = ?`), w, v1, userID, sl.id); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, s.rebind(`DELETE FROM key_slots WHERE user_id = ? AND kind = 'transition'`), userID)
	return err
}

// rewrapSeeds moves every calendar seed wrapped to K0 over to K1, so older content opens with
// the current account key. Those versions stay exposed to a copy taken during the transition;
// only versions made from now on are not (E11).
func (s *SQLStore) rewrapSeeds(ctx context.Context, tx *sql.Tx, userID string, k0, k1 *contentkey.KeyPair) error {
	rows, err := tx.QueryContext(ctx, s.rebind(`SELECT calendar_id, version, wrapped_seed FROM calendar_key_holders WHERE user_id = ?`), userID)
	if err != nil {
		return err
	}
	type wrap struct {
		cal     string
		version uint32
		seed    []byte
	}
	var wraps []wrap
	for rows.Next() {
		var w wrap
		if err := rows.Scan(&w.cal, &w.version, &w.seed); err != nil {
			rows.Close()
			return err
		}
		wraps = append(wraps, w)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, w := range wraps {
		k, err := contentkey.OpenCalendarSeed(k0, w.cal, w.version, userID, w.seed)
		if err != nil {
			return fmt.Errorf("calendar %s version %d: %w", w.cal, w.version, err)
		}
		seed, err := k.WrapSeed(userID, k1.PublicKey())
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, s.rebind(`UPDATE calendar_key_holders SET wrapped_seed = ? WHERE calendar_id = ? AND version = ? AND user_id = ?`), seed, w.cal, w.version, userID); err != nil {
			return err
		}
	}
	return nil
}

// RefuseLocked is ErrLocked when the request could not open any of objs (E10).
func RefuseLocked(objs ...*CalendarObject) error {
	for _, o := range objs {
		if o != nil && o.Locked {
			return ErrLocked
		}
	}
	return nil
}
