package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Migration represents an incremental schema change step.
type Migration struct {
	Version  int
	Name     string
	SQLite   string
	Postgres string
}

var registry = []Migration{
	{
		Version: 1,
		Name:    "initial_schema",
		SQLite: `
CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    email TEXT NOT NULL DEFAULT '',
    display_name TEXT NOT NULL DEFAULT '',
    password_hash TEXT NOT NULL DEFAULT '',
    role TEXT NOT NULL DEFAULT 'user',
    status TEXT NOT NULL DEFAULT 'active',
    sso_provider TEXT NOT NULL DEFAULT 'local',
    sso_subject TEXT NOT NULL DEFAULT '',
    totp_secret_enc TEXT NOT NULL DEFAULT '',
    totp_enabled INTEGER NOT NULL DEFAULT 0,
    recovery_codes_hash TEXT NOT NULL DEFAULT '[]',
    push_device_id TEXT NOT NULL DEFAULT '',
    must_change_password INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    last_login_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_users_username ON users(username);
CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);
CREATE INDEX IF NOT EXISTS idx_users_sso ON users(sso_provider, sso_subject);

CREATE TABLE IF NOT EXISTS sessions (
    token_hash TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    user_agent TEXT NOT NULL DEFAULT '',
    ip_address TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL,
    expires_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON sessions(expires_at);

CREATE TABLE IF NOT EXISTS device_pairings (
    secret TEXT PRIMARY KEY,
    code TEXT NOT NULL UNIQUE,
    user_id TEXT NOT NULL DEFAULT '',
    device_name TEXT NOT NULL DEFAULT '',
    platform TEXT NOT NULL DEFAULT '',
    push_token TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    created_at DATETIME NOT NULL,
    expires_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_pairings_code ON device_pairings(code);
CREATE INDEX IF NOT EXISTS idx_pairings_expires ON device_pairings(expires_at);

CREATE TABLE IF NOT EXISTS groups (
    id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL UNIQUE,
    external_id TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS group_members (
    group_id TEXT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, user_id)
);

CREATE TABLE IF NOT EXISTS audit_records (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT NOT NULL DEFAULT '',
    action TEXT NOT NULL,
    resource TEXT NOT NULL DEFAULT '',
    details TEXT NOT NULL DEFAULT '',
    ip_address TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_audit_user_created ON audit_records(user_id, created_at);

CREATE TABLE IF NOT EXISTS server_settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL
);
`,
		Postgres: `
CREATE TABLE IF NOT EXISTS users (
    id VARCHAR(64) PRIMARY KEY,
    username VARCHAR(128) NOT NULL UNIQUE,
    email VARCHAR(255) NOT NULL DEFAULT '',
    display_name VARCHAR(255) NOT NULL DEFAULT '',
    password_hash TEXT NOT NULL DEFAULT '',
    role VARCHAR(32) NOT NULL DEFAULT 'user',
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    sso_provider VARCHAR(32) NOT NULL DEFAULT 'local',
    sso_subject VARCHAR(255) NOT NULL DEFAULT '',
    totp_secret_enc TEXT NOT NULL DEFAULT '',
    totp_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    recovery_codes_hash TEXT NOT NULL DEFAULT '[]',
    push_device_id VARCHAR(255) NOT NULL DEFAULT '',
    must_change_password BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    last_login_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_users_username ON users(username);
CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);
CREATE INDEX IF NOT EXISTS idx_users_sso ON users(sso_provider, sso_subject);

CREATE TABLE IF NOT EXISTS sessions (
    token_hash VARCHAR(64) PRIMARY KEY,
    user_id VARCHAR(64) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    user_agent TEXT NOT NULL DEFAULT '',
    ip_address VARCHAR(64) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON sessions(expires_at);

CREATE TABLE IF NOT EXISTS device_pairings (
    secret VARCHAR(64) PRIMARY KEY,
    code VARCHAR(16) NOT NULL UNIQUE,
    user_id VARCHAR(64) NOT NULL DEFAULT '',
    device_name VARCHAR(255) NOT NULL DEFAULT '',
    platform VARCHAR(32) NOT NULL DEFAULT '',
    push_token TEXT NOT NULL DEFAULT '',
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_pairings_code ON device_pairings(code);
CREATE INDEX IF NOT EXISTS idx_pairings_expires ON device_pairings(expires_at);

CREATE TABLE IF NOT EXISTS groups (
    id VARCHAR(64) PRIMARY KEY,
    display_name VARCHAR(255) NOT NULL UNIQUE,
    external_id VARCHAR(255) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS group_members (
    group_id VARCHAR(64) NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    user_id VARCHAR(64) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, user_id)
);

CREATE TABLE IF NOT EXISTS audit_records (
    id BIGSERIAL PRIMARY KEY,
    user_id VARCHAR(64) NOT NULL DEFAULT '',
    action VARCHAR(64) NOT NULL,
    resource VARCHAR(255) NOT NULL DEFAULT '',
    details TEXT NOT NULL DEFAULT '',
    ip_address VARCHAR(64) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_audit_user_created ON audit_records(user_id, created_at);

CREATE TABLE IF NOT EXISTS server_settings (
    key VARCHAR(128) PRIMARY KEY,
    value TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL
);
`,
	},
	{
		Version: 2,
		Name:    "one_time_auth_challenges",
		SQLite: `
CREATE TABLE IF NOT EXISTS mfa_challenges (
    token_hash TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_mfa_challenges_expires ON mfa_challenges(expires_at);
`,
		Postgres: `
CREATE TABLE IF NOT EXISTS mfa_challenges (
    token_hash VARCHAR(64) PRIMARY KEY,
    user_id VARCHAR(64) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_mfa_challenges_expires ON mfa_challenges(expires_at);
`,
	},
	{
		Version:  3,
		Name:     "totp_last_counter",
		SQLite:   `ALTER TABLE users ADD COLUMN totp_last_counter INTEGER NOT NULL DEFAULT 0;`,
		Postgres: `ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_last_counter BIGINT NOT NULL DEFAULT 0;`,
	},
	{
		Version: 4,
		Name:    "mfa_credential_snapshot",
		SQLite: `DELETE FROM mfa_challenges;
ALTER TABLE mfa_challenges ADD COLUMN password_hash TEXT NOT NULL DEFAULT '';`,
		Postgres: `DELETE FROM mfa_challenges;
ALTER TABLE mfa_challenges ADD COLUMN password_hash TEXT NOT NULL DEFAULT '';`,
	},
	{
		// Pairings live 90 seconds, so dropping rows on upgrade loses nothing. The anonymous
		// verify route no longer accepts the guessable 6-digit code; only the 24-byte QR secret.
		Version: 5,
		Name:    "pairing_secret_only",
		SQLite: `DROP TABLE IF EXISTS device_pairings;
CREATE TABLE device_pairings (
    secret TEXT PRIMARY KEY,
    user_id TEXT NOT NULL DEFAULT '',
    device_name TEXT NOT NULL DEFAULT '',
    platform TEXT NOT NULL DEFAULT '',
    push_token TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    created_at DATETIME NOT NULL,
    expires_at DATETIME NOT NULL,
    authenticated_at DATETIME NOT NULL
);
CREATE INDEX idx_pairings_expires ON device_pairings(expires_at);`,
		Postgres: `DROP TABLE IF EXISTS device_pairings;
CREATE TABLE device_pairings (
    secret VARCHAR(64) PRIMARY KEY,
    user_id VARCHAR(64) NOT NULL DEFAULT '',
    device_name VARCHAR(255) NOT NULL DEFAULT '',
    platform VARCHAR(32) NOT NULL DEFAULT '',
    push_token TEXT NOT NULL DEFAULT '',
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    authenticated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_pairings_expires ON device_pairings(expires_at);`,
	},
	{
		Version: 6,
		Name:    "calendars",
		SQLite: `
CREATE TABLE calendars (
    id TEXT PRIMARY KEY,
    owner_kind TEXT NOT NULL CHECK (owner_kind IN ('user', 'group')),
    owner_id TEXT NOT NULL,
    slug TEXT NOT NULL,
    name TEXT NOT NULL,
    color TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    seq INTEGER NOT NULL DEFAULT 0,
    min_sync_seq INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL,
    UNIQUE (owner_kind, owner_id, slug)
);
CREATE TABLE calendar_objects (
    calendar_id TEXT NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    uid TEXT NOT NULL,
    etag TEXT NOT NULL,
    data BLOB NOT NULL,
    first_start INTEGER NOT NULL,
    last_end INTEGER,
    modified_at DATETIME NOT NULL,
    PRIMARY KEY (calendar_id, name),
    UNIQUE (calendar_id, uid)
);
CREATE INDEX idx_calendar_objects_range ON calendar_objects(calendar_id, first_start);
CREATE TABLE calendar_changes (
    calendar_id TEXT NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
    seq INTEGER NOT NULL,
    name TEXT NOT NULL,
    deleted INTEGER NOT NULL,
    changed_at DATETIME NOT NULL,
    PRIMARY KEY (calendar_id, seq)
);
CREATE INDEX idx_calendar_changes_time ON calendar_changes(changed_at);
CREATE TABLE calendar_meta (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
INSERT INTO calendar_meta (key, value) VALUES ('sync_epoch', lower(hex(randomblob(8))));`,
		Postgres: `
CREATE TABLE calendars (
    id VARCHAR(64) PRIMARY KEY,
    owner_kind VARCHAR(16) NOT NULL CHECK (owner_kind IN ('user', 'group')),
    owner_id VARCHAR(64) NOT NULL,
    slug VARCHAR(64) NOT NULL,
    name TEXT NOT NULL,
    color VARCHAR(32) NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    seq BIGINT NOT NULL DEFAULT 0,
    min_sync_seq BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (owner_kind, owner_id, slug)
);
CREATE TABLE calendar_objects (
    calendar_id VARCHAR(64) NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    uid TEXT NOT NULL,
    etag VARCHAR(64) NOT NULL,
    data BYTEA NOT NULL,
    first_start BIGINT NOT NULL,
    last_end BIGINT,
    modified_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (calendar_id, name),
    UNIQUE (calendar_id, uid)
);
CREATE INDEX idx_calendar_objects_range ON calendar_objects(calendar_id, first_start);
CREATE TABLE calendar_changes (
    calendar_id VARCHAR(64) NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
    seq BIGINT NOT NULL,
    name TEXT NOT NULL,
    deleted INTEGER NOT NULL,
    changed_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (calendar_id, seq)
);
CREATE INDEX idx_calendar_changes_time ON calendar_changes(changed_at);
CREATE TABLE calendar_meta (
    key VARCHAR(64) PRIMARY KEY,
    value TEXT NOT NULL
);
INSERT INTO calendar_meta (key, value) VALUES ('sync_epoch', substr(md5(random()::text), 1, 16));`,
	},
	{
		Version: 7,
		Name:    "app_passwords",
		SQLite: `
CREATE TABLE app_passwords (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    label TEXT NOT NULL,
    hash TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    last_used_at DATETIME
);
CREATE INDEX idx_app_passwords_user ON app_passwords(user_id);`,
		Postgres: `
CREATE TABLE app_passwords (
    id VARCHAR(64) PRIMARY KEY,
    user_id VARCHAR(64) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    label VARCHAR(64) NOT NULL,
    hash VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    last_used_at TIMESTAMPTZ
);
CREATE INDEX idx_app_passwords_user ON app_passwords(user_id);`,
	}, {
		// Grants cascade with their calendar and their group: deleting a KyIdentity group
		// removes its access and leaves the calendar for an admin to re-grant or delete.
		Version: 8,
		Name:    "calendar_grants",
		SQLite: `
CREATE TABLE calendar_grants (
    calendar_id TEXT NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
    group_id TEXT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('reader', 'editor', 'manager')),
    PRIMARY KEY (calendar_id, group_id)
);
CREATE INDEX idx_calendar_grants_group ON calendar_grants(group_id);`,
		Postgres: `
CREATE TABLE calendar_grants (
    calendar_id VARCHAR(64) NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
    group_id VARCHAR(64) NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    role VARCHAR(16) NOT NULL CHECK (role IN ('reader', 'editor', 'manager')),
    PRIMARY KEY (calendar_id, group_id)
);
CREATE INDEX idx_calendar_grants_group ON calendar_grants(group_id);`,
	}, {
		// The retired webhook copied KyIdentity's global role; its admins re-prove the app role.
		Version:  9,
		Name:     "revoke_sso_admin_sessions",
		SQLite:   revokeSSOAdminSessions,
		Postgres: revokeSSOAdminSessions,
	}, {
		// Groups get an owner. SCIM was the only group writer before this, so every existing row
		// is SCIM's; rows inserted from here on default to local.
		Version:  10,
		Name:     "group_source",
		SQLite:   groupSource,
		Postgres: groupSource,
	},
}

const groupSource = `ALTER TABLE groups ADD COLUMN source TEXT NOT NULL DEFAULT 'local';
UPDATE groups SET source = 'scim';`

const revokeSSOAdminSessions = `DELETE FROM sessions WHERE user_id IN (SELECT id FROM users WHERE role = 'admin' AND sso_provider IN ('kysignon', 'scim'));`

// Run executes all pending migrations for the specified database driver.
func Run(ctx context.Context, db *sql.DB, driver string) error {
	driver = strings.ToLower(driver)
	if driver == "postgresql" {
		driver = "postgres"
	}

	// Create schema_migrations table
	initTableQuery := `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    applied_at DATETIME NOT NULL
);`
	if driver == "postgres" {
		initTableQuery = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    applied_at TIMESTAMPTZ NOT NULL
);`
	}

	if _, err := db.ExecContext(ctx, initTableQuery); err != nil {
		return fmt.Errorf("failed to init schema_migrations: %w", err)
	}

	for _, m := range registry {
		var exists int
		err := db.QueryRowContext(ctx, "SELECT COUNT(1) FROM schema_migrations WHERE version = $1", m.Version).Scan(&exists)
		if err != nil {
			// Try SQLite positional ? parameter if $1 failed
			err = db.QueryRowContext(ctx, "SELECT COUNT(1) FROM schema_migrations WHERE version = ?", m.Version).Scan(&exists)
			if err != nil {
				return fmt.Errorf("failed to check migration version %d: %w", m.Version, err)
			}
		}

		if exists > 0 {
			continue
		}

		ddl := m.SQLite
		if driver == "postgres" {
			ddl = m.Postgres
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("failed to begin migration tx for v%d: %w", m.Version, err)
		}

		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("failed executing migration v%d (%s): %w", m.Version, m.Name, err)
		}

		recordQuery := "INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)"
		if driver == "postgres" {
			recordQuery = "INSERT INTO schema_migrations (version, name, applied_at) VALUES ($1, $2, $3)"
		}

		if _, err := tx.ExecContext(ctx, recordQuery, m.Version, m.Name, time.Now().UTC()); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("failed to record migration v%d: %w", m.Version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit migration v%d: %w", m.Version, err)
		}
	}

	return nil
}
