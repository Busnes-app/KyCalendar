package config

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/Busnes-app/kycalendar/internal/crypto"
)

// ReplicaConfigSetting is the server_settings row holding the shared fingerprint. It is never
// returned by the API or logged, and a restore deletes it.
const ReplicaConfigSetting = "replica_config_fingerprint"

// EncryptionKeyField names the encryption key's check value. Every other value is keyed by
// that key, so a differing key is reported alone.
const EncryptionKeyField = "KY_ENCRYPTION_KEY"

// replicaField classifies one Config field: its setting's name, and for a shared field the
// canonical value every PostgreSQL replica must agree on (nil: per host, may differ).
type replicaField struct {
	env   string
	value func(*Config) string
}

func perHost(env string) replicaField { return replicaField{env: env} }

// digest stands in for a secret file's content: replicas name their own paths.
func digest(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// canonicalList is a comma-separated list sorted and deduplicated, so two replicas listing the
// same networks in another order or spacing agree.
func canonicalList(raw string) string {
	var items []string
	for _, f := range strings.Split(raw, ",") {
		if f = strings.TrimSpace(f); f != "" {
			items = append(items, f)
		}
	}
	slices.Sort(items)
	return strings.Join(slices.Compact(items), ",")
}

// replicaFields classifies every Config field by its path. TestEveryConfigFieldIsClassified
// fails on a field missing here, or listed here and gone.
var replicaFields = map[string]replicaField{
	"InstallerSSO": {"installer SSO references", func(c *Config) string {
		if c.InstallerSSO == nil {
			return ""
		}
		b, _ := json.Marshal(c.InstallerSSO)
		return string(b)
	}},

	"Server.Host":         perHost("KY_HOST"),
	"Server.Port":         perHost("KY_PORT"),
	"Server.AppURL":       {"KY_APP_URL", func(c *Config) string { return c.Server.AppURL }},
	"Server.AppName":      {"KY_APP_NAME", func(c *Config) string { return c.Server.AppName }},
	"Server.ReadTimeout":  perHost("read timeout"),
	"Server.WriteTimeout": perHost("write timeout"),
	"Server.Environment":  {"KY_ENV", func(c *Config) string { return c.Server.Environment }},

	"Database.Driver":          perHost("KY_DB_DRIVER"),
	"Database.DSN":             perHost("KY_DB_DSN"),
	"Database.DataDir":         perHost("KY_DATA_DIR"),
	"Database.MaxOpenConns":    perHost("KY_DB_MAX_OPEN_CONNS"),
	"Database.MaxIdleConns":    perHost("KY_DB_MAX_IDLE_CONNS"),
	"Database.ConnMaxLifetime": perHost("connection lifetime"),

	"Security.SessionSecret": {"KY_SESSION_SECRET", func(c *Config) string { return c.Security.SessionSecret }},
	// EncryptionKey is the fingerprint's own key and its check value (ReplicaFingerprint).
	"Security.EncryptionKey":           perHost(EncryptionKeyField),
	"Security.CookieSecure":            {"KY_COOKIE_SECURE", func(c *Config) string { return strconv.FormatBool(c.Security.CookieSecure) }},
	"Security.CookieDomain":            {"KY_COOKIE_DOMAIN", func(c *Config) string { return c.Security.CookieDomain }},
	"Security.SessionTTL":              {"session lifetime", func(c *Config) string { return c.Security.SessionTTL.String() }},
	"Security.TrustedProxies":          perHost("KY_TRUSTED_PROXIES"),
	"Security.RateLimitExemptNetworks": {"KY_RATE_LIMIT_EXEMPT_NETWORKS", func(c *Config) string { return canonicalList(c.Security.RateLimitExemptNetworks) }},

	"SSO.Enabled":            {"KY_SSO_ENABLED", func(c *Config) string { return strconv.FormatBool(c.SSO.Enabled) }},
	"SSO.KySignOnIssuer":     {"KY_KYSIGNON_ISSUER", func(c *Config) string { return c.SSO.KySignOnIssuer }},
	"SSO.KySignOnClientID":   {"KY_KYSIGNON_CLIENT_ID", func(c *Config) string { return c.SSO.KySignOnClientID }},
	"SSO.KySignOnSecret":     {"KY_KYSIGNON_SECRET", func(c *Config) string { return c.SSO.KySignOnSecret }},
	"SSO.KySignOnHMACSecret": {"KY_KYSIGNON_HMAC_SECRET", func(c *Config) string { return c.SSO.KySignOnHMACSecret }},
	"SSO.SAMLEntityID":       {"KY_SAML_ENTITY_ID", func(c *Config) string { return c.SSO.SAMLEntityID }},
	"SSO.SAMLMetadataURL":    {"KY_SAML_METADATA_URL", func(c *Config) string { return c.SSO.SAMLMetadataURL }},
	"SSO.AutoProvision":      {"KY_SSO_AUTO_PROVISION", func(c *Config) string { return strconv.FormatBool(c.SSO.AutoProvision) }},

	"SCIM.Enabled":     {"KY_SCIM_ENABLED", func(c *Config) string { return strconv.FormatBool(c.SCIM.Enabled) }},
	"SCIM.BearerToken": {"KY_SCIM_TOKEN", func(c *Config) string { return c.SCIM.BearerToken }},

	"Backup.Dir":                  perHost("KYCALENDAR_BACKUP_DIR"),
	"Backup.Keep":                 perHost("KYCALENDAR_BACKUP_KEEP"),
	"Backup.DepositInterval":      {"KYCALENDAR_BACKUP_DEPOSIT_INTERVAL", func(c *Config) string { return c.Backup.DepositInterval.String() }},
	"Backup.AllowPrivateRecovery": {"KYCALENDAR_BACKUP_ALLOW_PRIVATE_RECOVERY", func(c *Config) string { return strconv.FormatBool(c.Backup.AllowPrivateRecovery) }},

	"Captcha.Provider":      {"KY_CAPTCHA_PROVIDER", func(c *Config) string { return c.Captcha.Provider }},
	"Captcha.SiteKey":       {"KY_CAPTCHA_SITE_KEY", func(c *Config) string { return c.Captcha.SiteKey }},
	"Captcha.SecretKey":     {"KY_CAPTCHA_SECRET_KEY", func(c *Config) string { return c.Captcha.SecretKey }},
	"Captcha.DifficultyPoW": {"KY_CAPTCHA_POW_DIFFICULTY", func(c *Config) string { return strconv.Itoa(c.Captcha.DifficultyPoW) }},

	"Calendar.MaxObjectsPerUser":      {"KY_CALENDAR_MAX_OBJECTS_PER_USER", func(c *Config) string { return strconv.Itoa(c.Calendar.MaxObjectsPerUser) }},
	"Calendar.MaxCalendarsPerUser":    {"KY_CALENDAR_MAX_CALENDARS_PER_USER", func(c *Config) string { return strconv.Itoa(c.Calendar.MaxCalendarsPerUser) }},
	"Calendar.MaxBytesPerUser":        {"KY_CALENDAR_MAX_BYTES_PER_USER", func(c *Config) string { return strconv.FormatInt(c.Calendar.MaxBytesPerUser, 10) }},
	"Calendar.MaxBytesTotal":          {"KY_CALENDAR_MAX_BYTES_TOTAL", func(c *Config) string { return strconv.FormatInt(c.Calendar.MaxBytesTotal, 10) }},
	"Calendar.Scheduling":             {"KYCALENDAR_SCHEDULING", func(c *Config) string { return c.Calendar.Scheduling }},
	"Calendar.KyPostURL":              {"KYCALENDAR_KYPOST_URL", func(c *Config) string { return c.Calendar.KyPostURL }},
	"Calendar.KyPostSecret":           {"KYCALENDAR_KYPOST_SECRET_FILE", func(c *Config) string { return digest(c.Calendar.KyPostSecret) }},
	"Calendar.KyPostAllowPrivate":     {"KYCALENDAR_KYPOST_ALLOW_PRIVATE", func(c *Config) string { return strconv.FormatBool(c.Calendar.KyPostAllowPrivate) }},
	"Calendar.KyMessagesURL":          {"KYCALENDAR_KYMESSAGES_URL", func(c *Config) string { return c.Calendar.KyMessagesURL }},
	"Calendar.KyMessagesSecret":       {"KYCALENDAR_KYMESSAGES_SECRET_FILE", func(c *Config) string { return digest(c.Calendar.KyMessagesSecret) }},
	"Calendar.KyMessagesAllowPrivate": {"KYCALENDAR_KYMESSAGES_ALLOW_PRIVATE", func(c *Config) string { return strconv.FormatBool(c.Calendar.KyMessagesAllowPrivate) }},

	"Tenancy.Multi":    {"KYCALENDAR_TENANCY", func(c *Config) string { return strconv.FormatBool(c.Tenancy.Multi) }},
	"Tenancy.StaffOrg": {"KYCALENDAR_STAFF_ORG_ID", func(c *Config) string { return c.Tenancy.StaffOrg }},
	// Cells compare as a set: another order is the same setting.
	"Tenancy.Cells": {"KY_CELLS", func(c *Config) string {
		b, _ := json.Marshal(slices.Sorted(slices.Values(c.Tenancy.Cells)))
		return string(b)
	}},

	"ObjectStorage.Endpoint":  {"KYCALENDAR_S3_ENDPOINT", func(c *Config) string { return c.ObjectStorage.Endpoint }},
	"ObjectStorage.Bucket":    {"KYCALENDAR_S3_BUCKET", func(c *Config) string { return c.ObjectStorage.Bucket }},
	"ObjectStorage.Region":    {"KYCALENDAR_S3_REGION", func(c *Config) string { return c.ObjectStorage.Region }},
	"ObjectStorage.AccessKey": {"KYCALENDAR_S3_ACCESS_KEY", func(c *Config) string { return c.ObjectStorage.AccessKey }},
	// Keyed like every value (ReplicaFingerprint), as KY_SESSION_SECRET is.
	"ObjectStorage.SecretKey": {"KYCALENDAR_S3_SECRET_KEY_FILE", func(c *Config) string { return c.ObjectStorage.SecretKey }},
}

// ReplicaFingerprint maps each shared setting's name to HMAC-SHA256 of its value under a key
// derived from the encryption key, so reading the database reveals no value, not even a
// low-entropy URL. The encryption key's own check value is under EncryptionKeyField.
func ReplicaFingerprint(c *Config) map[string]string {
	key := crypto.DeriveKey(c.Security.EncryptionKey, "kycalendar:replica-config")
	sum := func(name, value string) string {
		m := hmac.New(sha256.New, key)
		m.Write([]byte(name + "\x00" + value))
		return hex.EncodeToString(m.Sum(nil))
	}
	out := map[string]string{EncryptionKeyField: sum(EncryptionKeyField, "")}
	for _, f := range replicaFields {
		if f.value != nil {
			out[f.env] = sum(f.env, f.value(c))
		}
	}
	return out
}

// DiffFingerprint names the settings whose fingerprints differ, sorted, never their values. A
// differing encryption key is reported alone: every other comparison depends on it. A name
// only one side knows is no difference: an upgrade that adds a setting records it on first
// start (MergeFingerprint), and a removed one no longer matters.
func DiffFingerprint(stored, current map[string]string) []string {
	if !hmac.Equal([]byte(stored[EncryptionKeyField]), []byte(current[EncryptionKeyField])) {
		return []string{EncryptionKeyField}
	}
	var diff []string
	for name, v := range current {
		if old, ok := stored[name]; ok && !hmac.Equal([]byte(old), []byte(v)) {
			diff = append(diff, name)
		}
	}
	slices.Sort(diff)
	return diff
}

// MergeFingerprint adds the names stored lacks and reports whether it added any.
func MergeFingerprint(stored, current map[string]string) bool {
	added := false
	for name, v := range current {
		if _, ok := stored[name]; !ok {
			stored[name] = v
			added = true
		}
	}
	return added
}
