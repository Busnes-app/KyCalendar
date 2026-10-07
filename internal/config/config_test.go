package config_test

import (
	"bytes"
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

func TestConfigLoadFromEnvOverrides(t *testing.T) {
	t.Setenv("KY_DATA_DIR", t.TempDir())
	t.Setenv("KY_PORT", "9090")
	t.Setenv("KY_DB_DRIVER", "postgres")
	t.Setenv("KY_DB_DSN", "postgres://user:pass@localhost:5432/testdb")
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
	if err != nil || cfg.Calendar.MaxCalendarsPerUser != 50 || cfg.Calendar.MaxBytesPerUser != 16<<20 || cfg.Calendar.MaxBytesTotal != 40<<20 {
		t.Fatalf("defaults: %+v %v", cfg, err)
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

// Capsules are SQLite-only, so only SQLite needs the instance cap that keeps one under 64 MiB.
func TestCalendarInstanceCap(t *testing.T) {
	t.Setenv("KY_DATA_DIR", t.TempDir())
	t.Setenv("KY_DB_DRIVER", "postgres")
	cfg, err := config.LoadFromEnv()
	if err != nil || cfg.Calendar.MaxBytesTotal != 0 {
		t.Fatalf("postgres default: %+v %v", cfg, err)
	}
	t.Setenv("KY_CALENDAR_MAX_BYTES_TOTAL", "-1")
	if _, err := config.LoadFromEnv(); err == nil || !strings.Contains(err.Error(), "KY_CALENDAR_MAX_BYTES_TOTAL") {
		t.Fatalf("want KY_CALENDAR_MAX_BYTES_TOTAL error, got %v", err)
	}
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
