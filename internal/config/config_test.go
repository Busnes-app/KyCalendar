package config_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/config"
)

func TestConfigLoadDefaults(t *testing.T) {
	t.Setenv("KY_DATA_DIR", t.TempDir())
	cfg, err := config.LoadFromEnv()
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if cfg.Server.Port != 8080 {
		t.Errorf("expected default port 8080, got %d", cfg.Server.Port)
	}
	if cfg.Database.Driver != "sqlite" {
		t.Errorf("expected default driver sqlite, got %s", cfg.Database.Driver)
	}
	if cfg.Captcha.Provider != "pow" {
		t.Errorf("expected default captcha provider pow, got %s", cfg.Captcha.Provider)
	}
	if cfg.Backup.Dir != "" {
		t.Errorf("expected empty default backup dir (sealed local copies off), got %q", cfg.Backup.Dir)
	}
}

// postgresEnv is the least a PostgreSQL replica must be given.
func postgresEnv(t *testing.T) {
	t.Helper()
	t.Setenv("KY_DATA_DIR", t.TempDir())
	t.Setenv("KY_DB_DRIVER", "postgres")
	t.Setenv("KY_DB_DSN", "postgres://user:pass@localhost:5432/testdb")
	t.Setenv("KY_SESSION_SECRET", strings.Repeat("s", 32))
	t.Setenv("KY_ENCRYPTION_KEY", strings.Repeat("ab", 32))
	t.Setenv("KYCALENDAR_SCHEDULING", "false")
}

func TestConfigLoadFromEnvOverrides(t *testing.T) {
	postgresEnv(t)
	t.Setenv("KY_PORT", "9090")
	t.Setenv("KY_APP_NAME", "CustomBusnesApp")
	t.Setenv("KY_CAPTCHA_PROVIDER", "none")

	cfg, err := config.LoadFromEnv()
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if cfg.Server.Port != 9090 {
		t.Errorf("expected port 9090, got %d", cfg.Server.Port)
	}
	if cfg.Database.Driver != "postgres" {
		t.Errorf("expected driver postgres, got %s", cfg.Database.Driver)
	}
	if cfg.Database.DSN != "postgres://user:pass@localhost:5432/testdb" {
		t.Errorf("expected custom DSN, got %s", cfg.Database.DSN)
	}
	if cfg.Server.AppName != "CustomBusnesApp" {
		t.Errorf("expected custom app name, got %s", cfg.Server.AppName)
	}
	if cfg.Captcha.Provider != "none" {
		t.Errorf("expected captcha provider none, got %s", cfg.Captcha.Provider)
	}
}

func TestEncryptionKeyPersistsAcrossLoads(t *testing.T) {
	t.Setenv("KY_DATA_DIR", t.TempDir())
	t.Setenv("KY_ENCRYPTION_KEY", "")
	a, err := config.LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	b, err := config.LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Security.EncryptionKey) != 32 || !bytes.Equal(a.Security.EncryptionKey, b.Security.EncryptionKey) {
		t.Fatal("encryption key was not persisted between loads")
	}
}

func TestEncryptionKeyFromEnvMustBe32Bytes(t *testing.T) {
	t.Setenv("KY_DATA_DIR", t.TempDir())
	t.Setenv("KY_ENCRYPTION_KEY", "deadbeef")
	if _, err := config.LoadFromEnv(); err == nil {
		t.Fatal("8-byte key accepted")
	}
}

func TestDepositIntervalFromEnv(t *testing.T) {
	t.Setenv("KY_DATA_DIR", t.TempDir())
	for _, tc := range []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"", 24 * time.Hour, true},
		{"90m", 90 * time.Minute, true},
		{"15m", 15 * time.Minute, true},
		{"0", 0, true},
		{"1s", 0, false},
		{"14m", 0, false},
		{"-1h", 0, false},
		{"daily", 0, false},
	} {
		t.Setenv("KYCALENDAR_BACKUP_DEPOSIT_INTERVAL", tc.in)
		cfg, err := config.LoadFromEnv()
		if (err == nil) != tc.ok {
			t.Errorf("%q: err=%v, want ok=%v", tc.in, err, tc.ok)
			continue
		}
		if tc.ok && cfg.Backup.DepositInterval != tc.want {
			t.Errorf("%q: got %v, want %v", tc.in, cfg.Backup.DepositInterval, tc.want)
		}
	}
}

func TestBackupConfigFromEnv(t *testing.T) {
	t.Setenv("KY_DATA_DIR", t.TempDir())
	t.Setenv("KYCALENDAR_BACKUP_DIR", "/tmp/x")
	t.Setenv("KYCALENDAR_BACKUP_KEEP", "3")
	t.Setenv("KYCALENDAR_BACKUP_ALLOW_PRIVATE_RECOVERY", "true")
	cfg, err := config.LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Backup.Dir != "/tmp/x" || cfg.Backup.Keep != 3 || !cfg.Backup.AllowPrivateRecovery {
		t.Fatalf("%+v", cfg.Backup)
	}
}

func TestBackupKeepBelowOneIsRefused(t *testing.T) {
	t.Setenv("KY_DATA_DIR", t.TempDir())
	t.Setenv("KYCALENDAR_BACKUP_KEEP", "0")
	if _, err := config.LoadFromEnv(); err == nil || !strings.Contains(err.Error(), "KYCALENDAR_BACKUP_KEEP") {
		t.Fatalf("want KYCALENDAR_BACKUP_KEEP error, got %v", err)
	}
}

func TestCalendarMaxObjectsPerUser(t *testing.T) {
	t.Setenv("KY_DATA_DIR", t.TempDir())
	cfg, err := config.LoadFromEnv()
	if err != nil || cfg.Calendar.MaxObjectsPerUser != 20000 {
		t.Fatalf("default: %v %v", cfg, err)
	}
	t.Setenv("KY_CALENDAR_MAX_OBJECTS_PER_USER", "0")
	if _, err := config.LoadFromEnv(); err == nil || !strings.Contains(err.Error(), "KY_CALENDAR_MAX_OBJECTS_PER_USER") {
		t.Fatalf("want KY_CALENDAR_MAX_OBJECTS_PER_USER error, got %v", err)
	}
}

func TestCalendarQuotaDefaultsAndValidation(t *testing.T) {
	t.Setenv("KY_DATA_DIR", t.TempDir())
	cfg, err := config.LoadFromEnv()
	// One owner gets a tenth of the instance cap, so no single user fills the instance.
	if err != nil || cfg.Calendar.MaxCalendarsPerUser != 50 || cfg.Calendar.MaxBytesPerUser != 4<<20 || cfg.Calendar.MaxBytesTotal != 40<<20 {
		t.Fatalf("defaults: %+v %v", cfg, err)
	}
	t.Setenv("KY_CALENDAR_MAX_BYTES_TOTAL", strconv.Itoa(100<<20))
	if cfg, err := config.LoadFromEnv(); err != nil || cfg.Calendar.MaxBytesPerUser != 10<<20 {
		t.Fatalf("per-user default under a 100 MiB cap: %+v %v", cfg, err)
	}
	t.Setenv("KY_CALENDAR_MAX_BYTES_PER_USER", strconv.Itoa(30<<20))
	if cfg, err := config.LoadFromEnv(); err != nil || cfg.Calendar.MaxBytesPerUser != 30<<20 {
		t.Fatalf("explicit per-user cap: %+v %v", cfg, err)
	}
	for _, key := range []string{"KY_CALENDAR_MAX_CALENDARS_PER_USER", "KY_CALENDAR_MAX_BYTES_PER_USER"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "0")
			if _, err := config.LoadFromEnv(); err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("want %s error, got %v", key, err)
			}
		})
	}
}

// Both drivers seal their database into a capsule, so both default to the instance cap that
// keeps one tenant from outgrowing it (owner choice O1); an explicit value, 0 included, wins.
func TestCalendarInstanceCap(t *testing.T) {
	for name, setup := range map[string]func(*testing.T){
		"sqlite":   func(t *testing.T) { t.Setenv("KY_DATA_DIR", t.TempDir()) },
		"postgres": postgresEnv,
	} {
		t.Run(name, func(t *testing.T) {
			setup(t)
			cfg, err := config.LoadFromEnv()
			if err != nil || cfg.Calendar.MaxBytesTotal != 40<<20 || cfg.Calendar.MaxBytesPerUser != 4<<20 {
				t.Fatalf("default: %+v %v", cfg, err)
			}
			t.Setenv("KY_CALENDAR_MAX_BYTES_TOTAL", "0")
			if cfg, err := config.LoadFromEnv(); err != nil || cfg.Calendar.MaxBytesTotal != 0 || cfg.Calendar.MaxBytesPerUser != 16<<20 {
				t.Fatalf("explicit 0: %+v %v", cfg, err)
			}
			t.Setenv("KY_CALENDAR_MAX_BYTES_TOTAL", strconv.Itoa(300<<20))
			if cfg, err := config.LoadFromEnv(); err != nil || cfg.Calendar.MaxBytesTotal != 300<<20 {
				t.Fatalf("explicit cap: %+v %v", cfg, err)
			}
			t.Setenv("KY_CALENDAR_MAX_BYTES_TOTAL", "-1")
			if _, err := config.LoadFromEnv(); err == nil || !strings.Contains(err.Error(), "KY_CALENDAR_MAX_BYTES_TOTAL") {
				t.Fatalf("want KY_CALENDAR_MAX_BYTES_TOTAL error, got %v", err)
			}
		})
	}
}

// Each PostgreSQL guard refuses with the setting's name, before any key file is created.
func TestPostgresStartupGuards(t *testing.T) {
	for name, tc := range map[string]struct{ key, value, want string }{
		"missing DSN":             {"KY_DB_DSN", "", "KY_DB_DSN"},
		"built-in password":       {"KY_DB_DSN", "postgres://postgres:postgres@db:5432/cal", "KY_DB_DSN"},
		"no session secret (dev)": {"KY_SESSION_SECRET", "", "KY_SESSION_SECRET"},
		"no encryption key":       {"KY_ENCRYPTION_KEY", "", "KY_ENCRYPTION_KEY"},
		"automatic scheduling":    {"KYCALENDAR_SCHEDULING", "auto", "KYCALENDAR_SCHEDULING"},
		"scheduling unset":        {"KYCALENDAR_SCHEDULING", "", "KYCALENDAR_SCHEDULING"},
		"three connections":       {"KY_DB_MAX_OPEN_CONNS", "3", "KY_DB_MAX_OPEN_CONNS"},
	} {
		t.Run(name, func(t *testing.T) {
			postgresEnv(t)
			t.Setenv("KY_ENV", "development")
			t.Setenv(tc.key, tc.value)
			_, err := config.LoadFromEnv()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want a %s refusal, got %v", tc.want, err)
			}
			if _, err := os.Stat(filepath.Join(os.Getenv("KY_DATA_DIR"), "encryption.key")); !os.IsNotExist(err) {
				t.Fatalf("a refused start created a key file: %v", err)
			}
		})
	}
	t.Run("existing key file", func(t *testing.T) {
		postgresEnv(t)
		t.Setenv("KY_ENCRYPTION_KEY", "")
		if err := os.WriteFile(filepath.Join(os.Getenv("KY_DATA_DIR"), "encryption.key"), []byte(strings.Repeat("cd", 32)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := config.LoadFromEnv(); err != nil {
			t.Fatalf("an existing key file is a shared key: %v", err)
		}
	})
	t.Run("four connections", func(t *testing.T) {
		postgresEnv(t)
		t.Setenv("KY_DB_MAX_OPEN_CONNS", "4")
		if _, err := config.LoadFromEnv(); err != nil {
			t.Fatal(err)
		}
	})
}

// An HTTPS deployment gets Secure cookies (and HSTS) without also having to set KY_ENV.
func TestCookieSecureFollowsHTTPSAppURL(t *testing.T) {
	t.Setenv("KY_DATA_DIR", t.TempDir())
	for url, want := range map[string]bool{"https://cal.example.com": true, "HTTPS://cal.example.com": true, "http://localhost:8080": false} {
		t.Setenv("KY_APP_URL", url)
		cfg, err := config.LoadFromEnv()
		if err != nil || cfg.Security.CookieSecure != want {
			t.Errorf("%s: CookieSecure=%v err=%v, want %v", url, cfg != nil && cfg.Security.CookieSecure, err, want)
		}
	}
	t.Setenv("KY_APP_URL", "https://cal.example.com")
	t.Setenv("KY_COOKIE_SECURE", "false")
	if cfg, _ := config.LoadFromEnv(); cfg.Security.CookieSecure {
		t.Error("an explicit KY_COOKIE_SECURE=false must win")
	}
}

// Only the PoW provider is verified server-side; any other name would silently turn the check off.
func TestUnverifiedCaptchaProviderFailsStartup(t *testing.T) {
	t.Setenv("KY_DATA_DIR", t.TempDir())
	for _, p := range []string{"turnstile", "friendly", "POW "} {
		t.Setenv("KY_CAPTCHA_PROVIDER", p)
		if _, err := config.LoadFromEnv(); err == nil || !strings.Contains(err.Error(), "KY_CAPTCHA_PROVIDER") {
			t.Errorf("%q: want KY_CAPTCHA_PROVIDER error, got %v", p, err)
		}
	}
}

func TestBackupEnvUsesProductPrefix(t *testing.T) {
	t.Setenv("KY_DATA_DIR", t.TempDir())
	t.Setenv("KYCALENDAR_BACKUP_DIR", "/backups")
	t.Setenv("KYCALENDAR_BACKUP_KEEP", "3")
	t.Setenv("KYCALENDAR_BACKUP_DEPOSIT_INTERVAL", "1h")
	t.Setenv("KYCALENDAR_BACKUP_ALLOW_PRIVATE_RECOVERY", "true")
	cfg, err := config.LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	b := cfg.Backup
	if b.Dir != "/backups" || b.Keep != 3 || b.DepositInterval != time.Hour || !b.AllowPrivateRecovery {
		t.Fatalf("backup config = %+v", b)
	}
}

func TestRetiredBackupEnvIsRefused(t *testing.T) {
	for _, name := range []string{"KY_BACKUP_DIR", "KY_BACKUP_KEEP", "KY_BACKUP_DEPOSIT_INTERVAL", "KY_BACKUP_ALLOW_PRIVATE_RECOVERY"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("KY_DATA_DIR", t.TempDir())
			t.Setenv(name, "1")
			_, err := config.LoadFromEnv()
			want := "KYCALENDAR_" + strings.TrimPrefix(name, "KY_")
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("err = %v, want it to name %s", err, want)
			}
		})
	}
}

func TestEmptyRetiredBackupEnvIsAccepted(t *testing.T) {
	t.Setenv("KY_DATA_DIR", t.TempDir())
	t.Setenv("KY_BACKUP_DIR", "")
	if _, err := config.LoadFromEnv(); err != nil {
		t.Fatalf("empty retired name refused: %v", err)
	}
}

// A bare binary is reachable only from this host until KY_HOST says otherwise.
func TestHostDefaultsToLoopback(t *testing.T) {
	t.Setenv("KY_DATA_DIR", t.TempDir())
	cfg, err := config.LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Host != "127.0.0.1" {
		t.Errorf("default KY_HOST %q, want 127.0.0.1", cfg.Server.Host)
	}
	t.Setenv("KY_HOST", "0.0.0.0")
	if cfg, err = config.LoadFromEnv(); err != nil || cfg.Server.Host != "0.0.0.0" {
		t.Errorf("explicit KY_HOST: %v", err)
	}
}

// Production refuses a plain-HTTP KY_APP_URL unless the operator opts out by name.
func TestProductionRequiresHTTPSAppURL(t *testing.T) {
	for _, tc := range []struct {
		url, allow string
		ok         bool
	}{
		{"", "", false}, // the default, http://localhost:8080
		{"http://cal.example.com", "", false},
		{"http://cal.example.com", "false", false},
		{"https://cal.example.com", "", true},
		{"HTTPS://cal.example.com", "", true},
		{"http://cal.example.com", "true", true},
	} {
		t.Run(tc.url+"|"+tc.allow, func(t *testing.T) {
			t.Setenv("KY_DATA_DIR", t.TempDir())
			t.Setenv("KY_ENV", "production")
			t.Setenv("KY_SESSION_SECRET", strings.Repeat("s", 64))
			t.Setenv("KY_APP_URL", tc.url)
			t.Setenv("KY_ALLOW_INSECURE_HTTP", tc.allow)
			_, err := config.LoadFromEnv()
			if (err == nil) != tc.ok {
				t.Fatalf("err %v, want ok=%v", err, tc.ok)
			}
			if err != nil && !strings.Contains(err.Error(), "KY_ALLOW_INSECURE_HTTP") {
				t.Errorf("refusal must name the opt-out: %v", err)
			}
		})
	}
	// Outside production plain HTTP stays the zero-config default.
	t.Setenv("KY_DATA_DIR", t.TempDir())
	t.Setenv("KY_APP_URL", "http://localhost:8080")
	if _, err := config.LoadFromEnv(); err != nil {
		t.Fatalf("development with http: %v", err)
	}
}

// SCIM is on only with an operator-chosen token: a random per-boot default is a token nobody
// knows, so it served no IdP and only widened the surface.
func TestSCIMNeedsAnExplicitToken(t *testing.T) {
	token := strings.Repeat("k", 32)
	for _, tc := range []struct {
		name, enabled, token string
		want, wantErr        bool
	}{
		{"default is off", "", "", false, false},
		{"a token turns it on", "", token, true, false},
		{"explicitly on with a token", "true", token, true, false},
		{"explicitly off with a token", "false", token, false, false},
		{"explicitly on without a token", "true", "", false, true},
		{"a short token", "", "short-token", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("KY_DATA_DIR", t.TempDir())
			t.Setenv("KY_SCIM_ENABLED", tc.enabled)
			t.Setenv("KY_SCIM_TOKEN", tc.token)
			cfg, err := config.LoadFromEnv()
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "KY_SCIM_TOKEN") {
					t.Fatalf("err = %v, want one naming KY_SCIM_TOKEN", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.SCIM.Enabled != tc.want || cfg.SCIM.BearerToken != tc.token {
				t.Fatalf("SCIM = enabled %v token %q, want %v %q", cfg.SCIM.Enabled, cfg.SCIM.BearerToken, tc.want, tc.token)
			}
		})
	}
}

// The KyPost and KyMessages links read the same three variables under their own prefix.
func TestPeerConfigValidated(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good")
	short := filepath.Join(dir, "short")
	if err := os.WriteFile(good, []byte("0123456789abcdef0123456789abcdef\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(short, []byte("too-short"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		url, file string
		ok        bool
	}{
		"both set":       {"https://kypost.example/", good, true},
		"neither set":    {"", "", true},
		"http":           {"http://kypost.example", good, false},
		"a path":         {"https://kypost.example/x", good, false},
		"credentials":    {"https://u:p@kypost.example", good, false},
		"no secret file": {"https://kypost.example", "", false},
		"missing file":   {"https://kypost.example", filepath.Join(dir, "none"), false},
		"short secret":   {"https://kypost.example", short, false},
		"secret, no url": {"", good, false},
	} {
		for _, peer := range []string{"KYPOST", "KYMESSAGES"} {
			t.Run(peer+" "+name, func(t *testing.T) {
				t.Setenv("KY_DATA_DIR", t.TempDir())
				t.Setenv("KYCALENDAR_"+peer+"_URL", c.url)
				t.Setenv("KYCALENDAR_"+peer+"_SECRET_FILE", c.file)
				cfg, err := config.LoadFromEnv()
				if (err == nil) != c.ok {
					t.Fatalf("err %v, want ok=%v", err, c.ok)
				}
				if !c.ok || c.url == "" {
					return
				}
				url, secret := cfg.Calendar.KyPostURL, cfg.Calendar.KyPostSecret
				if peer == "KYMESSAGES" {
					url, secret = cfg.Calendar.KyMessagesURL, cfg.Calendar.KyMessagesSecret
				}
				if url != "https://kypost.example" || len(secret) != 32 {
					t.Errorf("url %q, secret %d bytes", url, len(secret))
				}
			})
		}
	}
}

func TestSchedulingMode(t *testing.T) {
	for v, want := range map[string]string{"": config.SchedulingAuto, "auto": config.SchedulingAuto, "true": config.SchedulingOn, "ON": config.SchedulingOn, "1": config.SchedulingOn, "false": config.SchedulingOff, "no": config.SchedulingOff} {
		t.Setenv("KY_DATA_DIR", t.TempDir())
		t.Setenv("KYCALENDAR_SCHEDULING", v)
		cfg, err := config.LoadFromEnv()
		if err != nil {
			t.Fatalf("KYCALENDAR_SCHEDULING=%q: %v", v, err)
		}
		if cfg.Calendar.Scheduling != want {
			t.Errorf("KYCALENDAR_SCHEDULING=%q: %s, want %s", v, cfg.Calendar.Scheduling, want)
		}
	}
	t.Setenv("KYCALENDAR_SCHEDULING", "sometimes")
	if _, err := config.LoadFromEnv(); err == nil {
		t.Error("KYCALENDAR_SCHEDULING=sometimes accepted")
	}
}

// multiEnv is a complete multi-tenant environment (tenancy spec step 6).
func multiEnv(t *testing.T) map[string]string {
	t.Helper()
	secret := filepath.Join(t.TempDir(), "s3")
	if err := os.WriteFile(secret, []byte("s3-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		"KYCALENDAR_TENANCY": "multi", "KY_DB_DRIVER": "postgres", "KY_DB_DSN": "postgres://cal:pw@db/cal",
		"KY_SESSION_SECRET": strings.Repeat("s", 32), "KYCALENDAR_SCHEDULING": "true",
		"KY_ENCRYPTION_KEY": strings.Repeat("ab", 32), "KY_KYSIGNON_ISSUER": "https://id.example",
		"KYCALENDAR_S3_ENDPOINT": "https://s3.example", "KYCALENDAR_S3_BUCKET": "cal", "KYCALENDAR_S3_ACCESS_KEY": "ak",
		"KYCALENDAR_S3_SECRET_KEY_FILE": secret, "KYCALENDAR_STAFF_ORG_ID": "org-staff", "KY_CELLS": "us-east-2, us-east-3",
	}
}

// KYCALENDAR_TENANCY=multi needs everything the tenancy spec lists (D1, D2, D7, D15, Cells),
// and each missing piece refuses naming the setting, before any key file in KY_DATA_DIR is read
// or made. A complete configuration still meets the last gate (ErrMultiTenantGated); past it,
// it loads. Single-tenant refuses the multi-tenant settings.
func TestMultiTenantStartupRequirements(t *testing.T) {
	load := func(t *testing.T, env map[string]string, seed ...string) (*config.Config, error, []os.DirEntry) {
		t.Helper()
		dir := t.TempDir()
		for _, name := range seed {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.Repeat("ab", 32)), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		t.Setenv("KY_DATA_DIR", dir)
		for k := range multiEnv(t) {
			t.Setenv(k, "")
		}
		for k, v := range env {
			t.Setenv(k, v)
		}
		cfg, err := config.LoadFromEnv()
		files, _ := os.ReadDir(dir)
		return cfg, err, files
	}
	if _, err, files := load(t, multiEnv(t)); !errors.Is(err, config.ErrMultiTenantGated) || len(files) != 0 {
		t.Fatalf("a complete multi-tenant configuration passed the last gate: %v, %d files", err, len(files))
	}
	t.Cleanup(config.OpenMultiTenantGateForTest())
	cfg, err, files := load(t, multiEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Tenancy.Multi || cfg.Tenancy.StaffOrg != "org-staff" || fmt.Sprint(cfg.Tenancy.Cells) != "[us-east-2 us-east-3]" || len(files) != 0 {
		t.Fatalf("tenancy %+v, %d files in KY_DATA_DIR", cfg.Tenancy, len(files))
	}
	for name, c := range map[string]struct{ key, value, names string }{
		"SQLite":                 {"KY_DB_DRIVER", "sqlite", "PostgreSQL"},
		"no issuer":              {"KY_KYSIGNON_ISSUER", "", "KY_KYSIGNON_ISSUER"},
		"no auto-provisioning":   {"KY_SSO_AUTO_PROVISION", "false", "KY_SSO_AUTO_PROVISION"},
		"no encryption key":      {"KY_ENCRYPTION_KEY", "", "no key file in KY_DATA_DIR is read"},
		"a backup directory":     {"KYCALENDAR_BACKUP_DIR", "/backups", "KYCALENDAR_BACKUP_DIR"},
		"no object storage":      {"KYCALENDAR_S3_ENDPOINT", "", "object storage is required"},
		"no staff org":           {"KYCALENDAR_STAFF_ORG_ID", "", "KYCALENDAR_STAFF_ORG_ID"},
		"the default org":        {"KYCALENDAR_STAFF_ORG_ID", "00000000-0000-0000-0000-000000000001", "KYCALENDAR_STAFF_ORG_ID"},
		"no cells":               {"KY_CELLS", "", "KY_CELLS"},
		"a cell that is no slug": {"KY_CELLS", "us-east-2,US East", "KY_CELLS"},
		"a cell twice":           {"KY_CELLS", "us-east-2,us-east-2", "KY_CELLS"},
		"33 cells":               {"KY_CELLS", cells(33), "KY_CELLS"},
		"another mode":           {"KYCALENDAR_TENANCY", "Multi", "KYCALENDAR_TENANCY"},
	} {
		t.Run(name, func(t *testing.T) {
			env := multiEnv(t)
			env[c.key] = c.value
			var seed []string
			switch c.key {
			case "KY_DB_DRIVER":
				delete(env, "KY_DB_DSN")
			case "KYCALENDAR_S3_ENDPOINT": // none of the store, not a partial one
				for _, k := range []string{"BUCKET", "ACCESS_KEY", "SECRET_KEY_FILE"} {
					delete(env, "KYCALENDAR_S3_"+k)
				}
			case "KY_ENCRYPTION_KEY": // an encryption.key there must not stand in (D15)
				seed = []string{"encryption.key"}
			}
			_, err, files := load(t, env, seed...)
			if err == nil || !strings.Contains(err.Error(), c.names) {
				t.Fatalf("err %v, want a refusal naming %s", err, c.names)
			}
			if len(files) != len(seed) {
				t.Fatalf("a refused start left %d files in KY_DATA_DIR", len(files))
			}
		})
	}
	for _, key := range []string{"KYCALENDAR_STAFF_ORG_ID", "KY_CELLS"} {
		t.Run("single with "+key, func(t *testing.T) {
			if _, err, _ := load(t, map[string]string{key: multiEnv(t)[key]}); err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("err %v", err)
			}
		})
	}
	if cfg, err, _ := load(t, map[string]string{"KYCALENDAR_TENANCY": "single"}); err != nil || cfg.Tenancy.Multi {
		t.Fatalf("single: %v", err)
	}
}

// cells lists n distinct cell slugs.
func cells(n int) string {
	var out []string
	for i := range n {
		out = append(out, fmt.Sprintf("us-east-%d", i+1))
	}
	return strings.Join(out, ",")
}

func TestRateLimitExemptNetworksAreValidatedAtStartup(t *testing.T) {
	t.Setenv("KY_DATA_DIR", t.TempDir())
	t.Setenv("KY_RATE_LIMIT_EXEMPT_NETWORKS", " 203.0.113.9/24, 2001:db8:1::/48 ")
	cfg, err := config.LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Security.RateLimitExemptNetworks; got != "203.0.113.0/24,2001:db8:1::/48" {
		t.Fatalf("RateLimitExemptNetworks = %q", got)
	}
	for _, bad := range []string{"0.0.0.0/0", "10.0.0.0/8", "::/0", "2001:db8::/32", "127.0.0.1", "office"} {
		t.Setenv("KY_RATE_LIMIT_EXEMPT_NETWORKS", bad)
		if _, err := config.LoadFromEnv(); err == nil || !strings.Contains(err.Error(), "KY_RATE_LIMIT_EXEMPT_NETWORKS") {
			t.Errorf("%q: err = %v, want a startup failure naming the variable", bad, err)
		}
	}
}

// KYCALENDAR_S3_* (tenancy spec D15): all four or none, a secret from a file, an https
// endpoint; a partial or invalid store fails startup rather than falling back to a disk.
func TestObjectStorageConfig(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "s3")
	if err := os.WriteFile(secret, []byte("s3-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	full := map[string]string{"ENDPOINT": "https://s3.example:9000", "BUCKET": "cal", "ACCESS_KEY": "ak", "SECRET_KEY_FILE": secret}
	with := func(k, v string) map[string]string {
		m := map[string]string{}
		for a, b := range full {
			m[a] = b
		}
		m[k] = v
		return m
	}
	for name, c := range map[string]struct {
		env map[string]string
		ok  bool
	}{
		"none":           {map[string]string{}, true},
		"all":            {full, true},
		"no bucket":      {with("BUCKET", ""), false},
		"no access key":  {with("ACCESS_KEY", ""), false},
		"no secret file": {with("SECRET_KEY_FILE", ""), false},
		"missing file":   {with("SECRET_KEY_FILE", filepath.Join(dir, "none")), false},
		"region only":    {map[string]string{"REGION": "us-east-2"}, false},
		"http":           {with("ENDPOINT", "http://s3.example"), false},
		"a path":         {with("ENDPOINT", "https://s3.example/x?y"), false},
		"bucket slash":   {with("BUCKET", "a/b"), false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("KY_DATA_DIR", t.TempDir())
			for _, k := range []string{"ENDPOINT", "BUCKET", "REGION", "ACCESS_KEY", "SECRET_KEY_FILE"} {
				t.Setenv("KYCALENDAR_S3_"+k, c.env[k])
			}
			cfg, err := config.LoadFromEnv()
			if (err == nil) != c.ok {
				t.Fatalf("err %v, want ok=%v", err, c.ok)
			}
			if err != nil && strings.Contains(err.Error(), "s3-secret") {
				t.Fatal("the error carries the secret")
			}
			if c.ok && len(c.env) > 0 && (cfg.ObjectStorage.SecretKey != "s3-secret" || !cfg.ObjectStorage.Configured()) {
				t.Fatalf("object storage %+v", cfg.ObjectStorage.Configured())
			}
		})
	}
}
