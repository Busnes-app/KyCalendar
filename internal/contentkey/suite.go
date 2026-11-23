// Package contentkey holds content encryption's keys and sealed formats
// (docs/CONTENT-ENCRYPTION-FORMAT.md; design: docs/superpowers/specs/2026-11-22-content-encryption-design.md):
// account and calendar keys, unlock slots, worker keys and the sealed parts of a calendar object.
// It has no store, HTTP or iCalendar code, and nothing calls it until build step CE2.
package contentkey

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hpke"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
)

// The suite is fixed for format version 1 and named by no field: X-Wing (ML-KEM-768 with X25519)
// with HKDF-SHA256 and AES-256-GCM, as the suite's capsules use. Every info string carries the
// version, so a ciphertext lifted into another use or version fails to open.
func kem() hpke.KEM   { return hpke.MLKEM768X25519() }
func kdf() hpke.KDF   { return hpke.HKDFSHA256() }
func aead() hpke.AEAD { return hpke.AES256GCM() }

const (
	// SeedBytes is the length of every key seed: an account's, a calendar's or a worker key.
	SeedBytes = 32
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

// sealTo is enc ‖ HPKE ciphertext to pub, base mode, one message per context.
func sealTo(pub hpke.PublicKey, info string, aad, plaintext []byte) ([]byte, error) {
	enc, s, err := hpke.NewSender(pub, kdf(), aead(), []byte(info))
	if err != nil {
		return nil, err
	}
	ct, err := s.Seal(aad, plaintext)
	if err != nil {
		return nil, err
	}
	return append(enc, ct...), nil
}

func openWith(priv hpke.PrivateKey, info string, aad, sealed []byte) ([]byte, error) {
	n := encSize()
	if len(sealed) < n {
		return nil, ErrOpen
	}
	r, err := hpke.NewRecipient(sealed[:n], priv, kdf(), aead(), []byte(info))
	if err != nil {
		return nil, ErrOpen
	}
	out, err := r.Open(aad, sealed[n:])
	if err != nil {
		return nil, ErrOpen
	}
	return out, nil
}

// encSize is the X-Wing encapsulated key's length: ML-KEM-768 ciphertext plus an X25519 point.
func encSize() int { return 1088 + 32 }
