// Package apppass makes and checks app passwords for native CalDAV clients.
//
// The secret is 256 random bits, so a fast hash is enough: there is nothing to
// brute-force offline. The id lets a login find its one row without scanning.
package apppass

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"strings"
)

var enc = base32.StdEncoding.WithPadding(base32.NoPadding)

func Generate() (id, token, hash string, err error) {
	idBytes := make([]byte, 10)
	secretBytes := make([]byte, 32)
	if _, err = rand.Read(idBytes); err != nil {
		return
	}
	if _, err = rand.Read(secretBytes); err != nil {
		return
	}
	id = strings.ToLower(enc.EncodeToString(idBytes))
	secret := strings.ToLower(enc.EncodeToString(secretBytes))
	return id, "kc_" + id + "_" + secret, hashSecret(secret), nil
}

func Parse(token string) (id, secret string, ok bool) {
	rest, ok := strings.CutPrefix(token, "kc_")
	if !ok {
		return "", "", false
	}
	id, secret, ok = strings.Cut(rest, "_")
	if !ok || id == "" || secret == "" {
		return "", "", false
	}
	return id, secret, true
}

func Matches(hash, secret string) bool {
	return subtle.ConstantTimeCompare([]byte(hash), []byte(hashSecret(secret))) == 1
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}
