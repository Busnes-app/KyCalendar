package contentkey

import (
	"bytes"
	"crypto/hpke"
	"fmt"
	"strconv"
)

// KeyPair is an X-Wing key pair kept as its 32-byte seed: an account's key (E3), whose seed the
// unlock slots wrap.
type KeyPair struct {
	seed [SeedBytes]byte
	priv hpke.PrivateKey
}

// NewKeyPair makes a key pair from a fresh random seed.
func NewKeyPair() (*KeyPair, error) {
	seed, err := random()
	if err != nil {
		return nil, err
	}
	return KeyPairFromSeed(seed[:])
}

// KeyPairFromSeed rebuilds the key pair a seed names.
func KeyPairFromSeed(seed []byte) (*KeyPair, error) {
	if len(seed) != SeedBytes {
		return nil, fmt.Errorf("contentkey: seed must be %d bytes", SeedBytes)
	}
	priv, err := kem().NewPrivateKey(seed)
	if err != nil {
		return nil, err
	}
	k := &KeyPair{priv: priv}
	copy(k.seed[:], seed)
	return k, nil
}

// Seed is the 32 bytes the key pair is rebuilt from; it is the secret a slot wraps.
func (k *KeyPair) Seed() []byte { return bytes.Clone(k.seed[:]) }

// PublicKey is the serialized X-Wing public key, as account_keys and calendar_keys store it.
func (k *KeyPair) PublicKey() []byte { return k.priv.PublicKey().Bytes() }

func parsePublicKey(b []byte) (hpke.PublicKey, error) {
	pub, err := kem().NewPublicKey(b)
	if err != nil {
		return nil, fmt.Errorf("contentkey: not an X-Wing public key: %w", err)
	}
	return pub, nil
}

// CalendarKey is one calendar's key (E3): a 32-byte seed that gives an X-Wing key pair, to which
// a worker seals without any secret, and an AES-256-GCM key for writes made while unlocked.
type CalendarKey struct {
	calendarID string
	version    uint32 // a rotation adds a version and keeps the old ones (E3, E11)
	seed       [SeedBytes]byte
	aes        [SeedBytes]byte
	pair       *KeyPair
}

// NewCalendarKey makes version of calendarID's key from a fresh random seed.
func NewCalendarKey(calendarID string, version uint32) (*CalendarKey, error) {
	seed, err := random()
	if err != nil {
		return nil, err
	}
	return CalendarKeyFromSeed(calendarID, version, seed[:])
}

// CalendarKeyFromSeed rebuilds version of calendarID's key from its seed.
func CalendarKeyFromSeed(calendarID string, version uint32, seed []byte) (*CalendarKey, error) {
	if len(seed) != SeedBytes {
		return nil, fmt.Errorf("contentkey: seed must be %d bytes", SeedBytes)
	}
	kemSeed, err := derive(seed, nil, "kycalendar content v1 calendar kem")
	if err != nil {
		return nil, err
	}
	pair, err := KeyPairFromSeed(kemSeed[:])
	if err != nil {
		return nil, err
	}
	c := &CalendarKey{calendarID: calendarID, version: version, pair: pair}
	copy(c.seed[:], seed)
	if c.aes, err = derive(seed, nil, "kycalendar content v1 calendar aes"); err != nil {
		return nil, err
	}
	return c, nil
}

// Version is which version of the calendar's key this is.
func (c *CalendarKey) Version() uint32 { return c.version }

// PublicKey is the calendar's X-Wing public key, as calendar_keys stores it.
func (c *CalendarKey) PublicKey() []byte { return c.pair.PublicKey() }

// Matches reports whether stored is this calendar's own public key. An unlocked request checks it
// before sealing to the stored key (E13), so a swapped calendar_keys row is caught.
func (c *CalendarKey) Matches(stored []byte) bool { return bytes.Equal(c.PublicKey(), stored) }

// WrapSeed seals c's seed to holder's account public key, for calendar_key_holders (E13). The
// AAD binds the calendar, its key version and the holder, so a wrap moved to another row fails to
// open.
func (c *CalendarKey) WrapSeed(holderUserID string, holderPublicKey []byte) ([]byte, error) {
	pub, err := parsePublicKey(holderPublicKey)
	if err != nil {
		return nil, err
	}
	return sealTo(pub, "kycalendar content v1 calendar seed", bind(c.calendarID, strconv.FormatUint(uint64(c.version), 10), holderUserID), c.seed[:])
}

// OpenCalendarSeed opens a wrap WrapSeed made of version for holderUserID, with that holder's
// account key (the version of it the wrap was made to).
func OpenCalendarSeed(account *KeyPair, calendarID string, version uint32, holderUserID string, wrapped []byte) (*CalendarKey, error) {
	seed, err := openWith(account.priv, "kycalendar content v1 calendar seed", bind(calendarID, strconv.FormatUint(uint64(version), 10), holderUserID), wrapped)
	if err != nil {
		return nil, err
	}
	return CalendarKeyFromSeed(calendarID, version, seed)
}
