package config

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Busnes-app/ky-primitives/applysetup"
	"github.com/Busnes-app/ky-primitives/keyfile"
	"github.com/Busnes-app/kycalendar/internal/crypto"
)

// Config encapsulates all runtime configuration for kycalendar.
type Config struct {
	InstallerSSO *applysetup.SSO `json:"-"`
	Server       ServerConfig    `json:"server"`
	Database     DatabaseConfig  `json:"database"`
	Security     SecurityConfig  `json:"security"`
	SSO          SSOConfig       `json:"sso"`
	SCIM         SCIMConfig      `json:"scim"`
	Backup       BackupConfig    `json:"backup"`
	Captcha      CaptchaConfig   `json:"captcha"`
	Calendar     CalendarConfig  `json:"calendar"`
}

// CalendarConfig bounds per-user calendar storage, and MaxBytesTotal (0 = off) every user's.
type CalendarConfig struct {
	MaxObjectsPerUser   int   `json:"max_objects_per_user"`
	MaxCalendarsPerUser int   `json:"max_calendars_per_user"`
	MaxBytesPerUser     int64 `json:"max_bytes_per_user"`
	MaxBytesTotal       int64 `json:"max_bytes_total"`
	// Scheduling is server-side scheduling (RFC 6638), implicit iTIP on every write:
	// SchedulingOn, SchedulingOff, or SchedulingAuto (unset), which is on exactly when a KyPost
	// link is up at startup. Off, CalDAV behaves exactly as it did before scheduling existed.
	Scheduling string `json:"scheduling"`
	// KyPost carries invitation mail for people outside the instance: its https base URL and
	// the shared syncauth secret, read from KYCALENDAR_KYPOST_SECRET_FILE. Empty URL: the link
	// comes from KyIdentity when it is the sign-in provider.
	KyPostURL          string `json:"kypost_url,omitempty"`
	KyPostSecret       []byte `json:"-"`
	KyPostAllowPrivate bool   `json:"kypost_allow_private"` // a LAN address; loopback stays refused
}

// ServerConfig defines HTTP and network settings.
type ServerConfig struct {
	Host         string        `json:"host"`
	Port         int           `json:"port"`
	AppURL       string        `json:"app_url"`
	AppName      string        `json:"app_name"`
	ReadTimeout  time.Duration `json:"read_timeout"`
	WriteTimeout time.Duration `json:"write_timeout"`
	Environment  string        `json:"environment"`
}

// DatabaseConfig holds connection settings for pluggable storage (SQLite, PostgreSQL, MySQL).
type DatabaseConfig struct {
	Driver          string        `json:"driver"` // "sqlite", "postgres", "mysql"
	DSN             string        `json:"dsn"`    // Connection string or file path
	DataDir         string        `json:"data_dir"`
	MaxOpenConns    int           `json:"max_open_conns"`
	MaxIdleConns    int           `json:"max_idle_conns"`
	ConnMaxLifetime time.Duration `json:"conn_max_lifetime"`
}

// SecurityConfig holds encryption keys, cookie secrets, and session settings.
type SecurityConfig struct {
	SessionSecret string `json:"session_secret"`
	EncryptionKey []byte `json:"-"` // 32 bytes for AES-256-GCM; never serialised
	CookieSecure  bool   `json:"cookie_secure"`
	CookieDomain  string `json:"cookie_domain"`
	SessionTTL    time.Duration
	// TrustedProxies is the parsed KY_TRUSTED_PROXIES allowlist. Only a request whose peer
	// address falls inside it may speak for a client other than itself.
	TrustedProxies []netip.Prefix `json:"-"`
}

// SSOConfig holds identity provider and federation parameters.
type SSOConfig struct {
	Enabled            bool   `json:"enabled"`
	KySignOnIssuer     string `json:"kysignon_issuer"`
	KySignOnClientID   string `json:"kysignon_client_id"`
	KySignOnSecret     string `json:"kysignon_secret"`
	KySignOnHMACSecret string `json:"kysignon_hmac_secret"`
	SAMLEntityID       string `json:"saml_entity_id"`
	SAMLMetadataURL    string `json:"saml_metadata_url"`
	AutoProvision      bool   `json:"auto_provision"`
}

// MinSCIMTokenLength is the shortest KY_SCIM_TOKEN accepted.
const MinSCIMTokenLength = 32

// SCIMConfig holds settings for RFC 7643/7644 inbound user provisioning.
type SCIMConfig struct {
	Enabled     bool   `json:"enabled"`
	BearerToken string `json:"bearer_token"`
}

// BackupConfig holds parameters for KyBackup capsules & recovery drills.
type BackupConfig struct {
	Dir string `json:"dir"`
	// Keep is how many capsule generations recoveryclient retains; it refuses values below 1.
	Keep int `json:"keep"`
	// DepositInterval is how often a paired instance seals and deposits a capsule to
	// KyRecovery. Zero disables the schedule; deposits then happen only on request.
	DepositInterval time.Duration `json:"deposit_interval"`
	// AllowPrivateRecovery admits private and CGNAT KyRecovery destinations (HTTPS still
	// required). Off by default: KyRecovery destinations must be public.
	AllowPrivateRecovery bool `json:"allow_private_recovery"`
}

// CaptchaConfig holds anti-abuse settings (PoW default, Turnstile, Friendly).
type CaptchaConfig struct {
	Provider      string `json:"provider"` // "pow" or "none"
	SiteKey       string `json:"site_key"`
	SecretKey     string `json:"secret_key"`
	DifficultyPoW int    `json:"difficulty_pow"`
}

// MinDepositInterval is the shortest schedule accepted: each run snapshots the whole database
// and uploads it, and KyRecovery admits 60 deposits per token per 15 minutes.
const MinDepositInterval = 15 * time.Minute

// DefaultAppName is the display name an unconfigured instance runs under. Capsules are sealed
// under backup.ServiceName, not this.
const DefaultAppName = "KyCalendar"

// SQLiteDSN is the DSN for the SQLite database at path, with the pragmas every opener needs.
func SQLiteDSN(path string) string {
	return path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)"
}

// LoadFromEnv initializes a Config struct populated from environment variables with sensible defaults.
func LoadFromEnv() (*Config, error) {
	// The backup variables carry the product prefix (root AGENTS.md); a retired name left in
	// .env would otherwise switch backups off without a word.
	for _, old := range []string{"KY_BACKUP_DIR", "KY_BACKUP_KEEP", "KY_BACKUP_DEPOSIT_INTERVAL", "KY_BACKUP_ALLOW_PRIVATE_RECOVERY"} {
		if os.Getenv(old) != "" {
			return nil, fmt.Errorf("%s was renamed to KYCALENDAR_%s", old, strings.TrimPrefix(old, "KY_"))
		}
	}
	port := getEnvInt("KY_PORT", getEnvInt("PORT", 8080))
	// Loopback unless told otherwise; the image sets 0.0.0.0, where a container must listen.
	host := getEnv("KY_HOST", "127.0.0.1")
	appURL := getEnv("KY_APP_URL", fmt.Sprintf("http://localhost:%d", port))
	appName := getEnv("KY_APP_NAME", DefaultAppName)
	env := getEnv("KY_ENV", "development")
	if env == "production" && !strings.HasPrefix(strings.ToLower(appURL), "https://") && !getEnvBool("KY_ALLOW_INSECURE_HTTP", false) {
		return nil, fmt.Errorf("KY_APP_URL must be https:// in production (got %q); put a TLS proxy in front, or set KY_ALLOW_INSECURE_HTTP=true to accept plain HTTP", appURL)
	}

	driver := strings.ToLower(getEnv("KY_DB_DRIVER", "sqlite"))
	dataDir := getEnv("KY_DATA_DIR", "./data")
	defaultDSN := SQLiteDSN(dataDir + "/kycalendar.db")
	if driver == "postgres" || driver == "postgresql" {
		driver = "postgres"
		defaultDSN = "postgres://postgres:postgres@localhost:5432/ky_server?sslmode=disable"
	}
	dsn := getEnv("KY_DB_DSN", defaultDSN)

	sessionSecret := getEnv("KY_SESSION_SECRET", "")
	if env == "production" && sessionSecret == "" {
		return nil, fmt.Errorf("KY_SESSION_SECRET is required in production")
	}
	if sessionSecret == "" {
		sessionSecret = generateRandomHex(32)
	}
	if len(sessionSecret) < crypto.MinHMACKeyBytes {
		return nil, fmt.Errorf("KY_SESSION_SECRET must contain at least %d bytes", crypto.MinHMACKeyBytes)
	}
	kysignonHMAC := getEnv("KY_KYSIGNON_HMAC_SECRET", "")
	if kysignonHMAC != "" && len(kysignonHMAC) < crypto.MinHMACKeyBytes {
		return nil, fmt.Errorf("KY_KYSIGNON_HMAC_SECRET must contain at least %d bytes", crypto.MinHMACKeyBytes)
	}

	encryptionKey, ok, err := keyfile.FromEnv("KY_ENCRYPTION_KEY", 32)
	if err != nil {
		return nil, fmt.Errorf("KY_ENCRYPTION_KEY: %w", err)
	}
	if !ok {
		encryptionKey, err = keyfile.LoadOrCreate(filepath.Join(dataDir, "encryption.key"), 32)
		if err != nil {
			return nil, fmt.Errorf("encryption key: %w", err)
		}
	}

	depositInterval, err := getEnvDuration("KYCALENDAR_BACKUP_DEPOSIT_INTERVAL", 24*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("KYCALENDAR_BACKUP_DEPOSIT_INTERVAL: %w", err)
	}
	if depositInterval != 0 && depositInterval < MinDepositInterval {
		return nil, fmt.Errorf("KYCALENDAR_BACKUP_DEPOSIT_INTERVAL: %s is below the %s minimum (0 disables)", depositInterval, MinDepositInterval)
	}

	backupKeep := getEnvInt("KYCALENDAR_BACKUP_KEEP", 7)
	if backupKeep < 1 {
		return nil, fmt.Errorf("KYCALENDAR_BACKUP_KEEP: must be at least 1, got %d", backupKeep)
	}

	calCfg := CalendarConfig{
		MaxObjectsPerUser:   getEnvInt("KY_CALENDAR_MAX_OBJECTS_PER_USER", 20000),
		MaxCalendarsPerUser: getEnvInt("KY_CALENDAR_MAX_CALENDARS_PER_USER", 50),
		KyPostAllowPrivate:  getEnvBool("KYCALENDAR_KYPOST_ALLOW_PRIVATE", false),
	}
	if calCfg.Scheduling, err = schedulingMode(getEnv("KYCALENDAR_SCHEDULING", "")); err != nil {
		return nil, err
	}
	if rawURL, secretFile := getEnv("KYCALENDAR_KYPOST_URL", ""), getEnv("KYCALENDAR_KYPOST_SECRET_FILE", ""); rawURL != "" || secretFile != "" {
		if calCfg.KyPostURL, err = KyPostURL(rawURL); err != nil {
			return nil, fmt.Errorf("KYCALENDAR_KYPOST_URL: %w", err)
		}
		if secretFile == "" {
			return nil, fmt.Errorf("KYCALENDAR_KYPOST_URL needs KYCALENDAR_KYPOST_SECRET_FILE")
		}
		if calCfg.KyPostSecret, err = kypostSecret(secretFile); err != nil {
			return nil, err
		}
	}
	// The SQLite snapshot is one capsule member, capped at 64 MiB; leave room for everything else.
	defaultTotal := 0
	if driver != "postgres" {
		defaultTotal = 40 << 20
	}
	if calCfg.MaxBytesTotal = int64(getEnvInt("KY_CALENDAR_MAX_BYTES_TOTAL", defaultTotal)); calCfg.MaxBytesTotal < 0 {
		return nil, fmt.Errorf("KY_CALENDAR_MAX_BYTES_TOTAL must not be negative")
	}
	// Under an instance cap one owner (a user or a group calendar) gets a tenth of it by default,
	// so no single owner can fill the instance for everyone.
	defaultPerUser := int64(16 << 20)
	if calCfg.MaxBytesTotal > 0 {
		defaultPerUser = min(defaultPerUser, max(calCfg.MaxBytesTotal/10, 1))
	}
	calCfg.MaxBytesPerUser = int64(getEnvInt("KY_CALENDAR_MAX_BYTES_PER_USER", int(defaultPerUser)))
	for key, v := range map[string]int64{
		"KY_CALENDAR_MAX_OBJECTS_PER_USER":   int64(calCfg.MaxObjectsPerUser),
		"KY_CALENDAR_MAX_CALENDARS_PER_USER": int64(calCfg.MaxCalendarsPerUser),
		"KY_CALENDAR_MAX_BYTES_PER_USER":     calCfg.MaxBytesPerUser,
	} {
		if v <= 0 {
			return nil, fmt.Errorf("%s must be positive", key)
		}
	}

	// Only PoW is verified server-side; accepting another name would disable the check silently.
	if captcha := getEnv("KY_CAPTCHA_PROVIDER", "pow"); captcha != "pow" && captcha != "none" {
		return nil, fmt.Errorf("KY_CAPTCHA_PROVIDER: %q is not verified by this server; use pow or none", captcha)
	}

	trustedProxies, err := ParseTrustedProxies(getEnv("KY_TRUSTED_PROXIES", ""))
	if err != nil {
		return nil, fmt.Errorf("KY_TRUSTED_PROXIES: %w", err)
	}

	// SCIM is on only with a token the operator chose and gave the IdP.
	scimToken := getEnv("KY_SCIM_TOKEN", "")
	scimOn := getEnv("KY_SCIM_ENABLED", "") == "" || getEnvBool("KY_SCIM_ENABLED", true)
	if scimToken == "" && getEnvBool("KY_SCIM_ENABLED", false) {
		return nil, fmt.Errorf("KY_SCIM_ENABLED is true but KY_SCIM_TOKEN is not set")
	}
	if scimToken != "" && len(scimToken) < MinSCIMTokenLength {
		return nil, fmt.Errorf("KY_SCIM_TOKEN must be at least %d characters", MinSCIMTokenLength)
	}

	cfg := &Config{
		Server: ServerConfig{
			Host:         host,
			Port:         port,
			AppURL:       strings.TrimRight(appURL, "/"),
			AppName:      appName,
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 15 * time.Second,
			Environment:  env,
		},
		Database: DatabaseConfig{
			Driver:          driver,
			DSN:             dsn,
			DataDir:         dataDir,
			MaxOpenConns:    getEnvInt("KY_DB_MAX_OPEN_CONNS", 25),
			MaxIdleConns:    getEnvInt("KY_DB_MAX_IDLE_CONNS", 5),
			ConnMaxLifetime: 15 * time.Minute,
		},
		Security: SecurityConfig{
			SessionSecret:  sessionSecret,
			EncryptionKey:  encryptionKey,
			CookieSecure:   getEnvBool("KY_COOKIE_SECURE", env == "production" || strings.HasPrefix(strings.ToLower(appURL), "https://")),
			CookieDomain:   getEnv("KY_COOKIE_DOMAIN", ""),
			SessionTTL:     7 * 24 * time.Hour,
			TrustedProxies: trustedProxies,
		},
		SSO: SSOConfig{
			Enabled:            getEnvBool("KY_SSO_ENABLED", true),
			KySignOnIssuer:     getEnv("KY_KYSIGNON_ISSUER", ""),
			KySignOnClientID:   getEnv("KY_KYSIGNON_CLIENT_ID", ""),
			KySignOnSecret:     getEnv("KY_KYSIGNON_SECRET", ""),
			KySignOnHMACSecret: kysignonHMAC,
			SAMLEntityID:       getEnv("KY_SAML_ENTITY_ID", ""),
			SAMLMetadataURL:    getEnv("KY_SAML_METADATA_URL", ""),
			AutoProvision:      getEnvBool("KY_SSO_AUTO_PROVISION", true),
		},
		SCIM: SCIMConfig{
			Enabled:     scimToken != "" && scimOn,
			BearerToken: scimToken,
		},
		Backup: BackupConfig{
			Dir:                  getEnv("KYCALENDAR_BACKUP_DIR", ""),
			Keep:                 backupKeep,
			DepositInterval:      depositInterval,
			AllowPrivateRecovery: getEnvBool("KYCALENDAR_BACKUP_ALLOW_PRIVATE_RECOVERY", false),
		},
		Captcha: CaptchaConfig{
			Provider:      getEnv("KY_CAPTCHA_PROVIDER", "pow"),
			SiteKey:       getEnv("KY_CAPTCHA_SITE_KEY", ""),
			SecretKey:     getEnv("KY_CAPTCHA_SECRET_KEY", ""),
			DifficultyPoW: getEnvInt("KY_CAPTCHA_POW_DIFFICULTY", 4),
		},
		Calendar: calCfg,
	}

	return cfg, nil
}

func getEnv(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok && strings.TrimSpace(val) != "" {
		return strings.TrimSpace(val)
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	if val, ok := os.LookupEnv(key); ok {
		if intVal, err := strconv.Atoi(strings.TrimSpace(val)); err == nil {
			return intVal
		}
	}
	return defaultVal
}

func getEnvBool(key string, defaultVal bool) bool {
	if val, ok := os.LookupEnv(key); ok {
		lower := strings.ToLower(strings.TrimSpace(val))
		return lower == "true" || lower == "1" || lower == "yes" || lower == "on"
	}
	return defaultVal
}

// getEnvDuration parses a Go duration such as "24h" or "90m". Negative is refused; "0" disables.
func getEnvDuration(key string, defaultVal time.Duration) (time.Duration, error) {
	val, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(val) == "" {
		return defaultVal, nil
	}
	d, err := time.ParseDuration(strings.TrimSpace(val))
	if err != nil {
		return 0, err
	}
	if d < 0 {
		return 0, fmt.Errorf("%s is negative", val)
	}
	return d, nil
}

func generateRandomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ParseTrustedProxies parses a comma-separated list of IPs and CIDR blocks into prefixes.
// A bare IP becomes a single-address prefix. An unparsable entry is a startup error: a
// silently dropped proxy would make the server ignore its X-Forwarded-For and lump every
// client behind it into one rate-limit bucket.
func ParseTrustedProxies(raw string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, field := range strings.Split(raw, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		if strings.Contains(field, "/") {
			prefix, err := netip.ParsePrefix(field)
			if err != nil {
				return nil, fmt.Errorf("invalid CIDR %q: %w", field, err)
			}
			// Checked after unmapping: a mapped form like ::ffff:0.0.0.0/96 has Bits() == 96
			// here but means 0.0.0.0/0 once rewritten, and would otherwise slip the guard.
			masked := unmapPrefix(prefix).Masked()
			if masked.Bits() == 0 {
				return nil, fmt.Errorf("trusted proxy %q trusts every address; list the proxy's own address or subnet instead", field)
			}
			out = append(out, masked)
			continue
		}
		addr, err := netip.ParseAddr(field)
		if err != nil {
			return nil, fmt.Errorf("invalid IP %q: %w", field, err)
		}
		addr = addr.Unmap()
		out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return out, nil
}

// unmapPrefix rewrites an IPv4-mapped IPv6 prefix as the plain IPv4 one it means. Peers are
// unmapped before the allowlist is consulted, and netip.Prefix.Contains never matches across
// families, so a mapped entry would otherwise match nothing at all.
func unmapPrefix(p netip.Prefix) netip.Prefix {
	if !p.Addr().Is4In6() || p.Bits() < 96 {
		return p
	}
	return netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
}

// Scheduling modes; see CalendarConfig.Scheduling.
const (
	SchedulingAuto = "auto"
	SchedulingOn   = "on"
	SchedulingOff  = "off"
)

// schedulingMode reads KYCALENDAR_SCHEDULING: unset, empty or "auto" is automatic. Anything
// unrecognised fails startup rather than quietly choosing a mode.
func schedulingMode(v string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "auto":
		return SchedulingAuto, nil
	case "true", "1", "yes", "on":
		return SchedulingOn, nil
	case "false", "0", "no", "off":
		return SchedulingOff, nil
	}
	return "", fmt.Errorf("KYCALENDAR_SCHEDULING must be true, false or auto (unset), got %q", v)
}

// KyPostURL checks a KyPost base URL, from the environment or from KyIdentity, and returns it
// without a trailing slash.
func KyPostURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("must be https://host with no path or credentials")
	}
	return strings.TrimSuffix(raw, "/"), nil
}

// kypostSecret reads the shared KyPost secret: at least 32 bytes once trimmed.
func kypostSecret(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("KYCALENDAR_KYPOST_SECRET_FILE: %w", err)
	}
	secret := bytes.TrimSpace(raw)
	if len(secret) < 32 {
		return nil, fmt.Errorf("KYCALENDAR_KYPOST_SECRET_FILE: the secret must be at least 32 bytes")
	}
	return secret, nil
}
