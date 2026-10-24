package auth

import (
	"time"

	"github.com/Busnes-app/ky-primitives/totp"
	"github.com/Busnes-app/kycalendar/internal/crypto"
)

// totpLabel derives the key users.totp_secret_enc is sealed under from the deployment key.
const totpLabel = "kycalendar:totp"

// GenerateTOTPSecret returns a fresh base32 RFC 6238 secret.
func GenerateTOTPSecret() (string, error) { return totp.GenerateSecret() }

// GenerateTOTPURL returns the otpauth:// URI an authenticator app enrols from.
func GenerateTOTPURL(issuer, accountName, secret string) string {
	return totp.ProvisioningURI(issuer, accountName, secret)
}

// ValidateTOTP reports whether code is valid for secret now, and the counter it matched.
// The caller must spend the counter with UserStore.SpendTOTPCounter before trusting it.
func ValidateTOTP(secret, code string) (int64, bool) {
	return totp.Validate(secret, code, time.Now())
}

// SealTOTPSecret encrypts secret for users.totp_secret_enc under the deployment key's
// "kycalendar:totp" subkey (AES-256-GCM), as other stored secrets are sealed.
func SealTOTPSecret(key []byte, secret string) (string, error) {
	return crypto.EncryptAESGCM([]byte(secret), crypto.DeriveKey(key, totpLabel))
}

// OpenTOTPSecret reverses SealTOTPSecret.
func OpenTOTPSecret(key []byte, sealed string) (string, error) {
	b, err := crypto.DecryptAESGCM(sealed, crypto.DeriveKey(key, totpLabel))
	return string(b), err
}
