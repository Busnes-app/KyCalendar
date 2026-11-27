// Package contentkey holds content encryption's keys and sealed formats
// (docs/CONTENT-ENCRYPTION-FORMAT.md; design: docs/superpowers/specs/2026-11-22-content-encryption-design.md):
// account and calendar keys, unlock slots, worker keys and the sealed parts of a calendar object.
// It has no store, HTTP or iCalendar code, and nothing calls it until build step CE2.
package contentkey

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"

	"github.com/Busnes-app/ky-primitives/xwing"
)

const (
	// SeedBytes is the length of every key seed: an account's, a calendar's or a worker key.
	SeedBytes = xwing.SeedBytes
	nonceSize = 12
)

// ErrOpen is every failure to open sealed bytes: a wrong key, a wrong binding or tampering.
var ErrOpen = errors.New("contentkey: cannot open")

// bind is the AAD for one use: each field length-prefixed, so no two field lists share bytes.
func bind(fields ...string) []byte {
	var b []byte
	for _, f := range fields {
		b = binary.BigEndian.AppendUint32(b, uint32(len(f)))
		b = append(b, f...)
	}
	return b
}

func derive(secret, salt []byte, info string) ([SeedBytes]byte, error) {
	var k [SeedBytes]byte
	out, err := hkdf.Key(sha256.New, secret, salt, info, SeedBytes)
	copy(k[:], out)
	return k, err
}

func random() ([SeedBytes]byte, error) {
	var k [SeedBytes]byte
	_, err := rand.Read(k[:])
	return k, err
}

// sealAES is nonce ‖ AES-256-GCM(key, aad, plaintext), with a random nonce.
func sealAES(key [SeedBytes]byte, aad, plaintext []byte) ([]byte, error) {
	g, err := gcm(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, nonceSize, nonceSize+len(plaintext)+g.Overhead())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return g.Seal(nonce, nonce, plaintext, aad), nil
}

func openAES(key [SeedBytes]byte, aad, sealed []byte) ([]byte, error) {
	g, err := gcm(key)
	if err != nil {
		return nil, err
	}
	if len(sealed) < nonceSize+g.Overhead() {
		return nil, ErrOpen
	}
	out, err := g.Open(nil, sealed[:nonceSize], sealed[nonceSize:], aad)
	if err != nil {
		return nil, ErrOpen
	}
	return out, nil
}

func gcm(key [SeedBytes]byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// sealTo preserves content v1's enc ‖ ciphertext framing. The shared helper owns the
// fixed X-Wing/HKDF-SHA256/AES-256-GCM suite; only its leading version byte is omitted.
func sealTo(pub xwing.PublicKey, info string, aad, plaintext []byte) ([]byte, error) {
	sealed, err := xwing.Seal(pub, []byte(info), aad, plaintext)
	if err != nil {
		return nil, err
	}
	return sealed[1:], nil
}

func openWith(priv xwing.PrivateKey, info string, aad, sealed []byte) ([]byte, error) {
	// Restore the helper's framing in a new buffer, leaving the caller's bytes untouched.
	framed := make([]byte, 1, 1+len(sealed))
	framed[0] = xwing.Version
	out, err := xwing.Open(priv, []byte(info), aad, append(framed, sealed...))
	if err != nil {
		return nil, ErrOpen
	}
	return out, nil
}
