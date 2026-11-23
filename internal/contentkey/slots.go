package contentkey

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// SlotKind names what unlocks a slot (E6). The value is bound into every wrap, so a slot can
// never be opened as another kind.
type SlotKind string

const (
	SlotAppPassword SlotKind = "apppass"
	SlotMCP         SlotKind = "mcp"
	SlotSession     SlotKind = "session"
	SlotPasskey     SlotKind = "passkey" // the WebAuthn PRF output
	SlotRecovery    SlotKind = "recovery"
	SlotTransition  SlotKind = "transition" // the deployment key's content-transition purpose (E11)
	SlotPassphrase  SlotKind = "passphrase"
	SlotPassword    SlotKind = "password"
	SlotDevice      SlotKind = "device" // reserved for device-held keys (option 1)
)

// stretched reports whether k's secret is chosen by a person, so its KEK needs Argon2id.
func (k SlotKind) stretched() bool { return k == SlotPassphrase || k == SlotPassword }

// SecretKEK is the key that wraps userID's account seed in slot slotID, from a high-entropy
// secret: a 256-bit app password, MCP token or session token, a passkey's PRF output, a 128-bit
// recovery code or the transition key. A passphrase or password must use PassphraseKEK.
func SecretKEK(kind SlotKind, userID, slotID string, secret []byte) ([SeedBytes]byte, error) {
	if kind.stretched() || kind == "" {
		return [SeedBytes]byte{}, fmt.Errorf("contentkey: %q slots need PassphraseKEK", kind)
	}
	if len(secret) < 16 {
		return [SeedBytes]byte{}, fmt.Errorf("contentkey: a %s secret needs at least 128 bits", kind)
	}
	return derive(secret, bind(userID, slotID), "kycalendar content v1 slot "+string(kind))
}

// Argon2Params are a stretched slot's stored parameters (key_slots.params). Memory is in KiB.
type Argon2Params struct {
	Salt    []byte `json:"salt"`
	Time    uint32 `json:"time"`
	Memory  uint32 `json:"memory"`
	Threads uint8  `json:"threads"`
}

// NewArgon2Params are the suite's Argon2id costs (ky-primitives password: 64 MiB, 3 passes,
// 4 lanes) with a fresh 16-byte salt.
func NewArgon2Params() (Argon2Params, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return Argon2Params{}, err
	}
	return Argon2Params{Salt: salt, Time: 3, Memory: 64 * 1024, Threads: 4}, nil
}

// MinPassphrase is the shortest passphrase a slot accepts (E8).
const MinPassphrase = 12

// PassphraseKEK stretches a passphrase or password with Argon2id. Parameters outside the
// suite's bounds are refused, so a stored row cannot make an unlock cheap or exhaust memory.
func PassphraseKEK(p Argon2Params, passphrase string) ([SeedBytes]byte, error) {
	var k [SeedBytes]byte
	switch {
	case len(p.Salt) < 16:
		return k, fmt.Errorf("contentkey: salt too short")
	case p.Time < 1 || p.Time > 10, p.Memory < 8*1024 || p.Memory > 256*1024, p.Threads < 1 || p.Threads > 16:
		return k, fmt.Errorf("contentkey: Argon2id parameters out of bounds")
	}
	copy(k[:], argon2.IDKey([]byte(passphrase), p.Salt, p.Time, p.Memory, p.Threads, SeedBytes))
	return k, nil
}

// WrapAccountSeed seals userID's account seed under kek for slot slotID of kind.
func WrapAccountSeed(kek [SeedBytes]byte, kind SlotKind, userID, slotID string, account *KeyPair) ([]byte, error) {
	return sealAES(kek, bind("kycalendar content v1 slot", string(kind), userID, slotID), account.seed[:])
}

// OpenSlot opens a wrap WrapAccountSeed made, giving the account's key pair.
func OpenSlot(kek [SeedBytes]byte, kind SlotKind, userID, slotID string, wrapped []byte) (*KeyPair, error) {
	seed, err := openAES(kek, bind("kycalendar content v1 slot", string(kind), userID, slotID), wrapped)
	if err != nil {
		return nil, err
	}
	return KeyPairFromSeed(seed)
}

var codeEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewRecoveryCode is a fresh 128-bit recovery code (E9), as shown to the user (26 base32
// symbols in groups of four or five) and as the secret SecretKEK takes.
func NewRecoveryCode() (shown string, secret []byte, err error) {
	secret = make([]byte, 16)
	if _, err := rand.Read(secret); err != nil {
		return "", nil, err
	}
	s := codeEncoding.EncodeToString(secret) // 26 symbols
	return strings.Join([]string{s[0:4], s[4:8], s[8:12], s[12:16], s[16:21], s[21:26]}, "-"), secret, nil
}

// ParseRecoveryCode is the secret a typed recovery code names. Case, spaces and dashes do not
// matter.
func ParseRecoveryCode(typed string) ([]byte, error) {
	s := strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(typed))
	secret, err := codeEncoding.DecodeString(s)
	if err != nil || len(secret) != 16 {
		return nil, fmt.Errorf("contentkey: not a recovery code")
	}
	return secret, nil
}
