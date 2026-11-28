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
	u.once.Do(func() { u.account, u.err = u.open(ctx) })
	return u.account, u.err
}

type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// calendarKey opens version of calendarID's key through the request user's holder wrap.
func (u *Unlock) calendarKey(ctx context.Context, s *SQLStore, q rowQuerier, calendarID string, version uint32) (*contentkey.CalendarKey, error) {
	if u.account == nil || u.err != nil {
		return nil, ErrLocked
	}
	id := fmt.Sprintf("%s/%d", calendarID, version)
	u.mu.Lock()
	k := u.keys[id]
	u.mu.Unlock()
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
	k, err = contentkey.OpenCalendarSeed(u.account, calendarID, version, u.UserID, wrapped)
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

// sealedWrite is how a write stores its object: the bytes of data and the sealed columns.
type sealedWrite struct {
	data, sealed []byte
	version      int64
	plainSize    sql.NullInt64
}

// sealForWrite decides what o's write stores (E2, E4, E10). An unlocked holder seals the whole
// body as one new base. Any other writer to a sealed row changes readable properties only: its
// object's skeleton replaces data and the parts stay, so it can never put content in data or
// drop content it could not read. A worker's new object in a calendar with a key stays legacy
// until CE3 seals it to the calendar's public key; the sweep seals it later.
func (c *calendarStore) sealForWrite(ctx context.Context, tx *sql.Tx, o *CalendarObject) (sealedWrite, error) {
	w := sealedWrite{data: o.Data}
	var prevData, prevSealed []byte
	var prevSize sql.NullInt64
	var prevUID string
	err := tx.QueryRowContext(ctx, c.q(`SELECT uid, data, sealed, parts_version, plain_size FROM calendar_objects WHERE calendar_id = ? AND name = ?`),
		o.CalendarID, o.Name).Scan(&prevUID, &prevData, &prevSealed, &w.version, &prevSize)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return w, err
	}
	if o.UID == "" {
		o.UID = prevUID // an in-place rewrite keeps the object's UID
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
		return w, c.dropWorkerKeys(ctx, tx, o.CalendarID, prevSealed)
	}
	if prevSealed != nil && o.UID != prevUID && keyed {
		// A new object under the old name: the old parts are bound to the old UID.
		if err := c.dropWorkerKeys(ctx, tx, o.CalendarID, prevSealed); err != nil {
			return w, err
		}
		return c.workerBase(ctx, tx, o, w, version, pub)
	}
	if prevSealed != nil {
		skel, err := skeleton.Split(o.Data)
		if err != nil {
			return w, err
		}
		w.data, w.sealed = skel, prevSealed
		w.plainSize = sql.NullInt64{Int64: prevSize.Int64 + int64(len(skel)) - int64(len(prevData)), Valid: true}
		return w, nil
	}
	if keyed && unlockOf(ctx) == nil {
		return c.workerBase(ctx, tx, o, w, version, pub)
	}
	return w, nil
}

// workerBase seals a worker's whole new object to the calendar's public key (E4): a fresh
// worker key, wrapped to the current calendar key version, used for this one object and ended
// at once, so it opens nothing else. A delivered invitation, an inbound iMIP message, a booking
// or a feed object in a calendar with a key is never stored in plaintext.
func (c *calendarStore) workerBase(ctx context.Context, tx *sql.Tx, o *CalendarObject, w sealedWrite, version uint32, pub []byte) (sealedWrite, error) {
	skel, err := skeleton.Split(o.Data)
	if err != nil {
		return w, err
	}
	wk, err := contentkey.NewWorkerKey()
	if err != nil {
		return w, err
	}
	wrapped, err := wk.WrapFor(o.CalendarID, version, pub)
	if err != nil {
		return w, err
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, c.q(`INSERT INTO worker_keys (id, calendar_id, wrapped, created_at, ended_at, key_version) VALUES (?, ?, ?, ?, ?, ?)`),
		wk.ID, o.CalendarID, wrapped, now, now, version); err != nil {
		return w, err
	}
	w.version++
	part, err := wk.Seal(contentkey.Part{Kind: contentkey.PartBase, Seq: uint64(w.version)}, o.CalendarID, version, o.UID, o.Data)
	if err != nil {
		return w, err
	}
	if w.sealed, err = contentkey.EncodeParts([]contentkey.Part{part}); err != nil {
		return w, err
	}
	w.data, w.plainSize = skel, sql.NullInt64{Int64: int64(len(o.Data)), Valid: true}
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
	for _, p := range parts {
		if p.WorkerKey == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, c.q(`DELETE FROM worker_keys WHERE id = ? AND calendar_id = ?`), p.WorkerKey, calendarID); err != nil {
			return err
		}
	}
	return nil
}

// rewriteData is a worker's in-place rewrite of an object's data, under the rule sealForWrite
// applies to writers without a key.
func (c *calendarStore) rewriteData(ctx context.Context, tx *sql.Tx, calendarID, name string, data []byte) error {
	ctx = context.WithValue(ctx, unlockKey{}, (*Unlock)(nil)) // never the request's key: it may not hold this calendar
	w, err := c.sealForWrite(ctx, tx, &CalendarObject{CalendarID: calendarID, Name: name, Data: data})
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, c.q(`UPDATE calendar_objects SET data = ?, etag = ?, sealed = ?, parts_version = ?, plain_size = ?, modified_at = ? WHERE calendar_id = ? AND name = ?`),
		w.data, objectETag(w.data, w.sealed), w.sealed, w.version, w.plainSize, time.Now().UTC(), calendarID, name)
	return err
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
			o.Data = merged
		}
	}
	return nil
}

func (c *calendarStore) openObject(ctx context.Context, u *Unlock, o *CalendarObject) ([]byte, error) {
	parts, err := contentkey.DecodeParts(o.Sealed)
	if err != nil {
		return nil, err
	}
	layers := make([]skeleton.Layer, 0, len(parts))
	for _, p := range parts {
		k, err := u.calendarKey(ctx, c.store, c.store.db, o.CalendarID, p.KeyVersion)
		if err != nil {
			return nil, err
		}
		pt, err := k.Open(p, o.UID, func(id string) ([]byte, error) {
			var wrapped []byte
			err := c.store.db.QueryRowContext(ctx, c.q(`SELECT wrapped FROM worker_keys WHERE id = ? AND calendar_id = ?`), id, o.CalendarID).Scan(&wrapped)
			return wrapped, err
		})
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
	// slots in one transaction, then keys the calendars every holder can open (E12).
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
	// Keyed reports whether a calendar has a key.
	Keyed(ctx context.Context, calendarID string) (bool, error)
	// SetUpAccounts counts accounts with an account key.
	SetUpAccounts(ctx context.Context) (int, error)
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
	tx, err := c.s.lockedTx(ctx, "content:"+userID)
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
	if err := tx.Commit(); err != nil {
		return err
	}
	return c.EnsureCalendarKeys(ctx, userID, account)
}

// openAccount opens cred's account key (E6). An app password or MCP token without its own slot
// opens the account through the transition slot while the transition lasts, and gets its own
// slot then (E11). Anything else without a slot is ErrLocked.
func (s *SQLStore) openAccount(ctx context.Context, cred Credential) (*contentkey.KeyPair, error) {
	a, err := s.accountKey(ctx, s.db, cred.UserID)
	if err != nil {
		return nil, err
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
	if err != nil || version != a.version || !bytes.Equal(account.PublicKey(), a.public) {
		return nil, ErrLocked
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
	rows, err := c.s.db.QueryContext(ctx, c.s.rebind(`SELECT c.id FROM calendars c WHERE c.owner_kind = 'user' AND c.owner_id = ?
 AND NOT EXISTS (SELECT 1 FROM calendar_keys k WHERE k.calendar_id = c.id)
UNION SELECT g.calendar_id FROM calendar_grants g JOIN group_members m ON m.group_id = g.group_id JOIN calendars c ON c.id = g.calendar_id
 WHERE m.user_id = ? AND c.owner_kind = 'group' AND NOT EXISTS (SELECT 1 FROM calendar_keys k WHERE k.calendar_id = g.calendar_id)`), userID, userID)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := c.keyCalendar(ctx, id); err != nil {
			return err
		}
	}
	return nil
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
	_, err = tx.ExecContext(ctx, s.rebind(`DELETE FROM task_search WHERE calendar_id = ?`), calendarID)
	return err
}

// Keyed reports whether calendarID has a key: its tasks are then never indexed in plaintext.
func (c contentStore) Keyed(ctx context.Context, calendarID string) (bool, error) {
	_, _, keyed, err := c.s.currentCalendarKey(ctx, c.s.db, calendarID)
	return keyed, err
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

// MatchFold reports whether body holds every word, compared as task search compares them
// (case and accents folded). Encrypted accounts' tasks are searched this way, never indexed.
func MatchFold(body string, words []string) bool {
	b := fold(body)
	for _, w := range words {
		if !strings.Contains(b, fold(w)) {
			return false
		}
	}
	return true
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
