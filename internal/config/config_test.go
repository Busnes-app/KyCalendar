package config_test

import (
	"bytes"
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

// Capsules are SQLite-only, so only SQLite needs the instance cap that keeps one under 64 MiB.
func TestCalendarInstanceCap(t *testing.T) {
	t.Setenv("KY_DATA_DIR", t.TempDir())
	t.Setenv("KY_DB_DRIVER", "postgres")
	cfg, err := config.LoadFromEnv()
	if err != nil || cfg.Calendar.MaxBytesTotal != 0 || cfg.Calendar.MaxBytesPerUser != 16<<20 {
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

func TestKyPostConfigValidated(t *testing.T) {
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
		t.Run(name, func(t *testing.T) {
			t.Setenv("KY_DATA_DIR", t.TempDir())
			t.Setenv("KYCALENDAR_KYPOST_URL", c.url)
			t.Setenv("KYCALENDAR_KYPOST_SECRET_FILE", c.file)
			cfg, err := config.LoadFromEnv()
			if (err == nil) != c.ok {
				t.Fatalf("err %v, want ok=%v", err, c.ok)
			}
			if c.ok && c.url != "" && (cfg.Calendar.KyPostURL != "https://kypost.example" || len(cfg.Calendar.KyPostSecret) != 32) {
				t.Errorf("url %q, secret %d bytes", cfg.Calendar.KyPostURL, len(cfg.Calendar.KyPostSecret))
			}
		})
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
