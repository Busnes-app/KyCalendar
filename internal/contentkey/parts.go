package contentkey

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
)

// PartKind is a part's place in a calendar object's sealed column (E4).
type PartKind byte

const (
	PartBase  PartKind = 1 // a whole body
	PartPatch PartKind = 2 // operations on named properties (E5)
)

// Part is one sealed part of a calendar object. Kind, KeepAlarms, Seq and Label are readable,
// so a worker can drop superseded parts without a key (E4, E5), and all four are bound into the
// sealed bytes' AAD. Seq is the object's parts_version when the part was written; it orders the
// parts and, unlike a position, survives removing an earlier one.
type Part struct {
	Kind       PartKind
	KeepAlarms bool   // a base that takes its alarms from the newest unmarked base before it
	Seq        uint64 // unique within the object
	Label      string // a patch's operation, property and component key; never a value
	WorkerKey  string // hex ID of the worker key that sealed it; "" for the calendar's AES key
	Sealed     []byte
}

func (p Part) aad(calendarID, uid string) []byte {
	keep := "0"
	if p.KeepAlarms {
		keep = "1"
	}
	return bind("kycalendar content v1 part", calendarID, uid, strconv.Itoa(int(p.Kind)), keep, strconv.FormatUint(p.Seq, 10), p.Label, p.WorkerKey)
}

func (p Part) check() error {
	switch {
	case p.Kind != PartBase && p.Kind != PartPatch:
		return fmt.Errorf("contentkey: part kind %d", p.Kind)
	case p.Kind == PartBase && p.Label != "":
		return errors.New("contentkey: a base has no label")
	case p.Kind == PartPatch && (p.Label == "" || p.KeepAlarms):
		return errors.New("contentkey: a patch needs a label and keeps no alarms")
	case len(p.Label) > 255:
		return errors.New("contentkey: label too long")
	case p.WorkerKey != "" && !canonicalKeyID(p.WorkerKey):
		// The AAD binds the ID as written and DecodeParts rebuilds it as lowercase hex, so any
		// other spelling would seal a part no read could open.
		return errors.New("contentkey: worker key ID must be 32 lowercase hex characters")
	}
	return nil
}

func canonicalKeyID(id string) bool {
	b, err := hex.DecodeString(id)
	return err == nil && len(b) == 16 && hex.EncodeToString(b) == id
}

// Seal seals plaintext as p under c's AES key: an unlocked write (E4).
func (c *CalendarKey) Seal(p Part, uid string, plaintext []byte) (Part, error) {
	p.WorkerKey = ""
	if err := p.check(); err != nil {
		return Part{}, err
	}
	sealed, err := sealAES(c.aes, p.aad(c.calendarID, uid), plaintext)
	p.Sealed = sealed
	return p, err
}

// Open opens p of object uid. A part a worker sealed needs that worker key's wrap, which
// workerKey returns by ID (worker_keys.wrapped).
func (c *CalendarKey) Open(p Part, uid string, workerKey func(id string) ([]byte, error)) ([]byte, error) {
	if err := p.check(); err != nil {
		return nil, err
	}
	key := c.aes
	if p.WorkerKey != "" {
		wrapped, err := workerKey(p.WorkerKey)
		if err != nil {
			return nil, err
		}
		w, err := c.OpenWorkerKey(p.WorkerKey, wrapped)
		if err != nil {
			return nil, err
		}
		key = w.key
	}
	return openAES(key, p.aad(c.calendarID, uid), p.Sealed)
}

// WorkerKey is the fresh AES key one worker run seals its parts under (E4). It is wrapped once
// per calendar to that calendar's public key and never stored unwrapped.
type WorkerKey struct {
	ID  string // 16 random bytes, hex
	key [SeedBytes]byte
}

// NewWorkerKey makes a worker run's key.
func NewWorkerKey() (*WorkerKey, error) {
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}
	key, err := random()
	return &WorkerKey{ID: hex.EncodeToString(id), key: key}, err
}

// WrapFor seals w to calendarID's public key, for worker_keys. No secret is needed.
func (w *WorkerKey) WrapFor(calendarID string, calendarPublicKey []byte) ([]byte, error) {
	pub, err := parsePublicKey(calendarPublicKey)
	if err != nil {
		return nil, err
	}
	return sealTo(pub, "kycalendar content v1 worker key", bind(calendarID, w.ID), w.key[:])
}

// OpenWorkerKey opens worker key id's wrap with c.
func (c *CalendarKey) OpenWorkerKey(id string, wrapped []byte) (*WorkerKey, error) {
	key, err := openWith(c.pair.priv, "kycalendar content v1 worker key", bind(c.calendarID, id), wrapped)
	if err != nil {
		return nil, err
	}
	w := &WorkerKey{ID: id}
	if len(key) != SeedBytes {
		return nil, ErrOpen
	}
	copy(w.key[:], key)
	return w, nil
}

// Seal seals plaintext as p of object uid in calendarID under w: a worker write (E4).
func (w *WorkerKey) Seal(p Part, calendarID, uid string, plaintext []byte) (Part, error) {
	p.WorkerKey = w.ID
	if err := p.check(); err != nil {
		return Part{}, err
	}
	sealed, err := sealAES(w.key, p.aad(calendarID, uid), plaintext)
	p.Sealed = sealed
	return p, err
}

// partsMagic starts an encoded part list, naming format version 1.
var partsMagic = []byte("KCP1")

// maxParts bounds a decoded list: two bases plus 16 patches (E4), with room to spare.
const maxParts = 64

// EncodeParts is the sealed column's bytes: "KCP1", a count, then each part as kind, flags,
// sequence, label, worker key ID and sealed bytes, each variable field length-prefixed.
func EncodeParts(parts []Part) ([]byte, error) {
	if len(parts) > maxParts {
		return nil, fmt.Errorf("contentkey: %d parts", len(parts))
	}
	b := append([]byte{}, partsMagic...)
	b = binary.BigEndian.AppendUint16(b, uint16(len(parts)))
	seen := map[uint64]bool{}
	for _, p := range parts {
		if err := p.check(); err != nil {
			return nil, err
		}
		if seen[p.Seq] {
			// DecodeParts refuses it, so storing it would make the object unreadable.
			return nil, fmt.Errorf("contentkey: sequence number %d used twice", p.Seq)
		}
		seen[p.Seq] = true
		id, err := hex.DecodeString(p.WorkerKey)
		if err != nil || (len(id) != 0 && len(id) != 16) {
			return nil, errors.New("contentkey: worker key ID")
		}
		flags := byte(0)
		if p.KeepAlarms {
			flags = 1
		}
		b = append(b, byte(p.Kind), flags)
		b = binary.BigEndian.AppendUint64(b, p.Seq)
		b = append(b, byte(len(p.Label)))
		b = append(b, p.Label...)
		b = append(b, byte(len(id)))
		b = append(b, id...)
		b = binary.BigEndian.AppendUint32(b, uint32(len(p.Sealed)))
		b = append(b, p.Sealed...)
	}
	return b, nil
}

var errParts = errors.New("contentkey: malformed part list")

// DecodeParts reads EncodeParts' bytes. Anything malformed, truncated or trailing is refused.
func DecodeParts(b []byte) ([]Part, error) {
	if len(b) < 6 || string(b[:4]) != string(partsMagic) {
		return nil, errParts
	}
	n := int(binary.BigEndian.Uint16(b[4:6]))
	if n > maxParts {
		return nil, errParts
	}
	b = b[6:]
	take := func(k int) ([]byte, bool) {
		if k < 0 || len(b) < k {
			return nil, false
		}
		out := b[:k]
		b = b[k:]
		return out, true
	}
	parts := make([]Part, 0, n)
	seen := map[uint64]bool{}
	for range n {
		head, ok := take(11)
		if !ok {
			return nil, errParts
		}
		p := Part{Kind: PartKind(head[0]), KeepAlarms: head[1] == 1, Seq: binary.BigEndian.Uint64(head[2:10])}
		if head[1] > 1 || seen[p.Seq] {
			return nil, errParts
		}
		seen[p.Seq] = true
		label, ok := take(int(head[10]))
		if !ok {
			return nil, errParts
		}
		p.Label = string(label)
		idLen, ok := take(1)
		if !ok || (idLen[0] != 0 && idLen[0] != 16) {
			return nil, errParts
		}
		id, ok := take(int(idLen[0]))
		if !ok {
			return nil, errParts
		}
		p.WorkerKey = hex.EncodeToString(id)
		size, ok := take(4)
		if !ok {
			return nil, errParts
		}
		sealed, ok := take(int(binary.BigEndian.Uint32(size)))
		if !ok {
			return nil, errParts
		}
		p.Sealed = append([]byte(nil), sealed...)
		if p.check() != nil {
			return nil, errParts
		}
		parts = append(parts, p)
	}
	if len(b) != 0 {
		return nil, errParts
	}
	return parts, nil
}
