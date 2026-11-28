package migrations

import (
	"context"
	"database/sql"
	"database/sql/driver"
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
	{
		Version:  11,
		Name:     "user_sso_issuer",
		SQLite:   userSSOIssuer,
		Postgres: userSSOIssuer,
	},
	{
		// One row holding the bytes of every calendar object, kept by triggers in the writing
		// transaction, so the instance cap is checked without summing every object. Triggers also
		// cover cascades (calendar and user deletion).
		Version: 12,
		Name:    "calendar_usage",
		SQLite: calendarUsageTable + `
CREATE TRIGGER calendar_usage_insert AFTER INSERT ON calendar_objects
BEGIN UPDATE calendar_usage SET bytes = bytes + LENGTH(NEW.data) WHERE id = 1; END;
CREATE TRIGGER calendar_usage_update AFTER UPDATE OF data ON calendar_objects
BEGIN UPDATE calendar_usage SET bytes = bytes - LENGTH(OLD.data) + LENGTH(NEW.data) WHERE id = 1; END;
CREATE TRIGGER calendar_usage_delete AFTER DELETE ON calendar_objects
BEGIN UPDATE calendar_usage SET bytes = bytes - LENGTH(OLD.data) WHERE id = 1; END;`,
		Postgres: calendarUsageTable + `
CREATE FUNCTION calendar_usage_track() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE d BIGINT;
BEGIN
    IF TG_OP = 'INSERT' THEN d := LENGTH(NEW.data);
    ELSIF TG_OP = 'UPDATE' THEN d := LENGTH(NEW.data) - LENGTH(OLD.data);
    ELSE d := -LENGTH(OLD.data);
    END IF;
    UPDATE calendar_usage SET bytes = bytes + d WHERE id = 1;
    RETURN NULL;
END $$;
CREATE TRIGGER calendar_usage_track AFTER INSERT OR UPDATE OF data OR DELETE ON calendar_objects
FOR EACH ROW EXECUTE FUNCTION calendar_usage_track();`,
	},
	{
		// Case-insensitive login lookups, per-request grant loading and per-user revocation.
		Version:  13,
		Name:     "lookup_indexes",
		SQLite:   lookupIndexes,
		Postgres: lookupIndexes,
	},
	{
		// App password scopes (CalDAV or MCP) and the events each MCP token created.
		Version: 14,
		Name:    "app_password_scopes",
		SQLite: `ALTER TABLE app_passwords ADD COLUMN scope TEXT NOT NULL DEFAULT 'dav';
CREATE TABLE mcp_events (
    token_id TEXT NOT NULL REFERENCES app_passwords(id) ON DELETE CASCADE,
    calendar_id TEXT NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
    uid TEXT NOT NULL,
    PRIMARY KEY (token_id, calendar_id, uid)
);`,
		Postgres: `ALTER TABLE app_passwords ADD COLUMN scope VARCHAR(16) NOT NULL DEFAULT 'dav';
CREATE TABLE mcp_events (
    token_id VARCHAR(64) NOT NULL REFERENCES app_passwords(id) ON DELETE CASCADE,
    calendar_id VARCHAR(64) NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
    uid TEXT NOT NULL,
    PRIMARY KEY (token_id, calendar_id, uid)
);`,
	},
	{
		// Calendars that mirror an ICS link. The row's presence makes the calendar read-only.
		Version: 15,
		Name:    "calendar_subscriptions",
		SQLite: `CREATE TABLE calendar_subscriptions (
    calendar_id TEXT PRIMARY KEY REFERENCES calendars(id) ON DELETE CASCADE,
    url_sealed TEXT NOT NULL,
    url_host TEXT NOT NULL,
    etag TEXT NOT NULL DEFAULT '',
    last_modified TEXT NOT NULL DEFAULT '',
    refreshed_at DATETIME,
    checked_at DATETIME,
    next_check_at DATETIME NOT NULL,
    failures INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_calendar_subscriptions_due ON calendar_subscriptions(next_check_at);`,
		Postgres: `CREATE TABLE calendar_subscriptions (
    calendar_id VARCHAR(64) PRIMARY KEY REFERENCES calendars(id) ON DELETE CASCADE,
    url_sealed TEXT NOT NULL,
    url_host VARCHAR(255) NOT NULL,
    etag TEXT NOT NULL DEFAULT '',
    last_modified TEXT NOT NULL DEFAULT '',
    refreshed_at TIMESTAMPTZ,
    checked_at TIMESTAMPTZ,
    next_check_at TIMESTAMPTZ NOT NULL,
    failures INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_calendar_subscriptions_due ON calendar_subscriptions(next_check_at);`,
	},
	{
		// Tasks: a calendar's component set and each object's component. Every existing calendar
		// and object is VEVENT, the only component accepted before this.
		Version:  16,
		Name:     "calendar_components",
		SQLite:   calendarComponents,
		Postgres: calendarComponents,
	},
	{
		// Scheduling: iTIP messages queued in the transaction of the write that produced them,
		// delivered by the outbox worker. dedupe_key makes a re-queued message a no-op.
		Version: 17,
		Name:    "scheduling_outbox",
		SQLite: `CREATE TABLE scheduling_outbox (
    id TEXT PRIMARY KEY,
    dedupe_key TEXT NOT NULL UNIQUE,
    sender_id TEXT NOT NULL,
    method TEXT NOT NULL,
    recipient TEXT NOT NULL,
    body BLOB NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at DATETIME NOT NULL,
    last_error TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL
);
CREATE INDEX idx_scheduling_outbox_due ON scheduling_outbox(status, next_attempt_at);`,
		Postgres: `CREATE TABLE scheduling_outbox (
    id VARCHAR(64) PRIMARY KEY,
    dedupe_key VARCHAR(128) NOT NULL UNIQUE,
    sender_id VARCHAR(64) NOT NULL,
    method VARCHAR(16) NOT NULL,
    recipient VARCHAR(320) NOT NULL,
    body BYTEA NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_scheduling_outbox_due ON scheduling_outbox(status, next_attempt_at);`,
	},
	{
		// sso_sid ties a session to the identity provider's login; sso_logouts fences a sid
		// back-channel logout ended, so a callback still in flight cannot revive it.
		Version: 18,
		Name:    "backchannel_logout",
		SQLite: `ALTER TABLE sessions ADD COLUMN sso_sid TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_sessions_sso_sid ON sessions(sso_sid) WHERE sso_sid <> '';
CREATE TABLE sso_logouts (
    sid TEXT PRIMARY KEY,
    expires_at DATETIME NOT NULL
);`,
		Postgres: `ALTER TABLE sessions ADD COLUMN sso_sid TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_sessions_sso_sid ON sessions(sso_sid) WHERE sso_sid <> '';
CREATE TABLE sso_logouts (
    sid TEXT PRIMARY KEY,
    expires_at TIMESTAMPTZ NOT NULL
);`,
	},
	{
		// sso_subject_logouts fences a subject's logins issued before cutoff that a subject-wide
		// back-channel logout ended, so a callback still in flight cannot revive them.
		Version: 19,
		Name:    "backchannel_subject_logout",
		SQLite: `CREATE TABLE sso_subject_logouts (
    issuer TEXT NOT NULL,
    subject TEXT NOT NULL,
    cutoff DATETIME NOT NULL,
    expires_at DATETIME NOT NULL,
    PRIMARY KEY (issuer, subject)
);`,
		Postgres: `CREATE TABLE sso_subject_logouts (
    issuer TEXT NOT NULL,
    subject TEXT NOT NULL,
    cutoff TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (issuer, subject)
);`,
	},
	{
		// An SSO session from before migration 18 has no sid, so no back-channel logout can
		// reach it, nor an SSO account's pending MFA challenge. Sign them out once; local
		// password sessions and sessions issued with a sid stay.
		Version:  20,
		Name:     "drop_unreachable_sso_sessions",
		SQLite:   dropUnreachableSSOSessions,
		Postgres: dropUnreachableSSOSessions,
	},
	{
		// A generic OIDC account's email was stored without checking email_verified. It is
		// who the person invites and answers as, so it goes; the next sign-in stores it again
		// when the provider says it is verified.
		Version:  21,
		Name:     "clear_unverified_oidc_emails",
		SQLite:   clearOIDCEmails,
		Postgres: clearOIDCEmails,
	},
	{
		// An SSO session whose ID token had no auth_time: the provider never said when the
		// person last gave their credentials, so it never counts for a step-up. Sessions from
		// before the column cannot say whether theirs had one, so every SSO session is unproven.
		Version:  22,
		Name:     "session_unproven",
		SQLite:   sessionUnproven,
		Postgres: sessionUnproven,
	},
	{Version: 23, Name: "invitation_sponsorship", SQLite: invitationSponsorship, Postgres: invitationSponsorship},
	{Version: 24, Name: "cancellation_reservations", SQLite: cancellationReservations, Postgres: cancellationReservations},
	{Version: 25, Name: "video_meetings", SQLite: videoMeetings("DATETIME"), Postgres: videoMeetings("TIMESTAMPTZ")},
	{Version: 26, Name: "meeting_guests", SQLite: meetingGuests("DATETIME"), Postgres: meetingGuests("TIMESTAMPTZ")},
	{Version: 27, Name: "booking_pages", SQLite: bookingPages("DATETIME"), Postgres: bookingPages("TIMESTAMPTZ")},
	{Version: 28, Name: "resources", SQLite: resources("DATETIME"), Postgres: resources("TIMESTAMPTZ")},
	{Version: 29, Name: "kypost_out_of_office", SQLite: kypostOutOfOffice, Postgres: kypostOutOfOffice},
	{Version: 30, Name: "presence_pushes", SQLite: presencePushes("DATETIME"), Postgres: presencePushes("TIMESTAMPTZ")},
	{Version: 31, Name: "suite_group_calendars", SQLite: suiteGroupCalendars, Postgres: suiteGroupCalendars},
	{Version: 32, Name: "tasks", SQLite: taskTables + taskSearchSQLite, Postgres: taskTables + taskSearchPostgres},
	{Version: 33, Name: "task_insights", SQLite: taskInsights, Postgres: taskInsights},
	{Version: 34, Name: "task_prefs", SQLite: taskPrefs, Postgres: taskPrefs},
	{Version: 35, Name: "resource_bookings", SQLite: resourceBookings("DATETIME"), Postgres: resourceBookings("TIMESTAMPTZ")},
	{Version: 36, Name: "replica_coordination", SQLite: replicaCoordination, Postgres: replicaCoordination},
	{Version: 37, Name: "org_model", SQLite: orgModel(false), Postgres: orgModel(true)},
	{Version: 38, Name: "content_encryption", SQLite: contentEncryption("BLOB", "DATETIME"), Postgres: contentEncryption("BYTEA", "TIMESTAMPTZ")},
	// export_only marks a session from a KyIdentity export_only token: it reaches only the
	// export routes, and only while its org is deletion_scheduled (tenancy spec, Export).
	{Version: 39, Name: "session_export_only", SQLite: sessionExportOnly, Postgres: sessionExportOnly},
	{Version: 40, Name: "org_storage_pool", SQLite: storagePool("CAST(unixepoch() AS INTEGER)"), Postgres: storagePool("CAST(EXTRACT(EPOCH FROM statement_timestamp()) AS BIGINT)")},
	{Version: 41, Name: "pool_refresh", SQLite: `ALTER TABLE orgs ADD COLUMN pool_refresh BOOLEAN NOT NULL DEFAULT FALSE;`, Postgres: `ALTER TABLE orgs ADD COLUMN pool_refresh BOOLEAN NOT NULL DEFAULT FALSE;`},
	// The org's cell (tenancy spec, Cells): KyIdentity's home_region, mirrored by the poll.
	{Version: 42, Name: "org_home_region", SQLite: `ALTER TABLE orgs ADD COLUMN home_region TEXT NOT NULL DEFAULT '';`, Postgres: `ALTER TABLE orgs ADD COLUMN home_region TEXT NOT NULL DEFAULT '';`},
	// Content encryption CE2 (docs/superpowers/specs/2026-11-22-content-encryption-design.md, Schema).
	{Version: 43, Name: "content_encryption_ce2", SQLite: contentEncryptionCE2("BLOB", false), Postgres: contentEncryptionCE2("BYTEA", true)},
	// FTS5 deletes remove terms from the index's segments instead of leaving them until a merge,
	// and the rebuild drops what earlier deletes left (CE2: keying deletes an account's rows).
	{Version: 44, Name: "task_search_secure_delete", SQLite: `INSERT INTO task_search(task_search, rank) VALUES ('secure-delete', 1);
INSERT INTO task_search(task_search) VALUES ('rebuild');`, Postgres: `SELECT 1;`},
	// CE3: a message planned from a sealed object's skeleton carries the writer's content patch,
	// so a recipient's copy keeps its content (NULL: the body is the sender's whole content).
	{Version: 45, Name: "outbox_worker_patch", SQLite: `ALTER TABLE scheduling_outbox ADD COLUMN worker_patch TEXT;`, Postgres: `ALTER TABLE scheduling_outbox ADD COLUMN worker_patch TEXT;`},
}

// storagePool is the product-side shared state of KyIdentity's reserved-byte grant.
// No network or startup mode is enabled by this additive migration.
func storagePool(now string) string {
	return `ALTER TABLE orgs ADD COLUMN pool_since BIGINT NOT NULL DEFAULT 0;
ALTER TABLE orgs ADD COLUMN pool_seq BIGINT NOT NULL DEFAULT 0 CHECK (pool_seq >= 0);
ALTER TABLE orgs ADD COLUMN pool_report_used BIGINT NOT NULL DEFAULT 0 CHECK (pool_report_used >= 0);
ALTER TABLE orgs ADD COLUMN pool_report_want BIGINT NOT NULL DEFAULT 0 CHECK (pool_report_want >= 0);
ALTER TABLE orgs ADD COLUMN pool_sent_at BIGINT NOT NULL DEFAULT 0;
ALTER TABLE orgs ADD COLUMN pool_applied_seq BIGINT NOT NULL DEFAULT 0;
ALTER TABLE orgs ADD COLUMN pool_ceiling BIGINT CHECK (pool_ceiling >= 0);
ALTER TABLE orgs ADD COLUMN pool_expires_at BIGINT NOT NULL DEFAULT 0;
ALTER TABLE orgs ADD COLUMN pool_stale_seq BIGINT NOT NULL DEFAULT 0;
ALTER TABLE orgs ADD COLUMN pool_inflight BOOLEAN NOT NULL DEFAULT FALSE;
UPDATE orgs SET pool_since = ` + now + `;`
}

// contentEncryption is content encryption at rest's schema (docs/superpowers/specs/
// 2026-11-22-content-encryption-design.md, Schema; build step CE1c), additive only: nothing reads
// or writes it until CE2, and no column a running reader uses changes. Every new table cascades
// from users or calendars, so an org purge removes it with them.
func contentEncryption(blob, ts string) string {
	return `CREATE TABLE account_keys (
 user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 public_key ` + blob + ` NOT NULL,
 created_at ` + ts + ` NOT NULL,
 transition_until ` + ts + `
);
CREATE TABLE key_slots (
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 slot_id TEXT NOT NULL,
 kind TEXT NOT NULL CHECK (kind IN ('apppass', 'mcp', 'session', 'passkey', 'recovery', 'transition', 'passphrase', 'password', 'device')),
 wrapped ` + blob + ` NOT NULL,
 params TEXT NOT NULL DEFAULT '',
 created_at ` + ts + ` NOT NULL,
 last_used_at ` + ts + `,
 PRIMARY KEY (user_id, slot_id)
);
CREATE TABLE calendar_keys (
 calendar_id TEXT PRIMARY KEY REFERENCES calendars(id) ON DELETE CASCADE,
 public_key ` + blob + ` NOT NULL
);
CREATE TABLE calendar_key_holders (
 calendar_id TEXT NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 wrapped_seed ` + blob + ` NOT NULL,
 former BOOLEAN NOT NULL DEFAULT FALSE,
 PRIMARY KEY (calendar_id, user_id)
);
CREATE INDEX calendar_key_holders_user ON calendar_key_holders(user_id);
CREATE TABLE pending_key_shares (
 calendar_id TEXT NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 queued_at ` + ts + ` NOT NULL,
 PRIMARY KEY (calendar_id, user_id)
);
CREATE TABLE worker_keys (
 id TEXT PRIMARY KEY,
 calendar_id TEXT NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
 wrapped ` + blob + ` NOT NULL,
 created_at ` + ts + ` NOT NULL,
 ended_at ` + ts + `
);
CREATE INDEX worker_keys_calendar ON worker_keys(calendar_id);
ALTER TABLE calendar_objects ADD COLUMN sealed ` + blob + `;
ALTER TABLE calendar_objects ADD COLUMN parts_version BIGINT NOT NULL DEFAULT 0;
ALTER TABLE calendar_objects ADD COLUMN plain_size BIGINT;
ALTER TABLE scheduling_outbox ADD COLUMN sealed ` + blob + `;
ALTER TABLE bookings ADD COLUMN sealed_hold ` + blob + `;
ALTER TABLE bookings ADD COLUMN public_key ` + blob + `;
ALTER TABLE bookings ADD COLUMN wrapped_seed ` + blob + `;
ALTER TABLE bookings ADD COLUMN sealed_copy ` + blob + `;`
}

// ObjectBytes is an object row's counted size (content encryption E4): its plaintext size when
// it is sealed, else the bytes of data. r is the row's name or alias (NEW, OLD, o).
func ObjectBytes(r string) string {
	return "(CASE WHEN " + r + ".sealed IS NULL THEN LENGTH(" + r + ".data) ELSE " + r + ".plain_size END)"
}

// contentEncryptionCE2 is CE2's migration: key versions (E3, E11), slot public keys and account
// wraps (E6), the sizes a worker change needs (E4), and the usage triggers counting plain_size
// for sealed rows. calendar_keys and calendar_key_holders are rebuilt with version in their keys:
// migration 38 made them, and nothing has written them since.
func contentEncryptionCE2(blob string, postgres bool) string {
	s := `ALTER TABLE account_keys ADD COLUMN key_version BIGINT NOT NULL DEFAULT 1;
CREATE TABLE account_previous_keys (
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 key_version BIGINT NOT NULL,
 sealed_seed ` + blob + ` NOT NULL,
 PRIMARY KEY (user_id, key_version)
);
ALTER TABLE key_slots ADD COLUMN public_key ` + blob + `;
ALTER TABLE key_slots ADD COLUMN account_wrap ` + blob + `;
ALTER TABLE key_slots ADD COLUMN account_key_version BIGINT NOT NULL DEFAULT 1;
DROP TABLE calendar_key_holders;
DROP TABLE calendar_keys;
CREATE TABLE calendar_keys (
 calendar_id TEXT NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
 version BIGINT NOT NULL,
 public_key ` + blob + ` NOT NULL,
 exposed BOOLEAN NOT NULL DEFAULT FALSE,
 PRIMARY KEY (calendar_id, version)
);
CREATE TABLE calendar_key_holders (
 calendar_id TEXT NOT NULL,
 version BIGINT NOT NULL,
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 wrapped_seed ` + blob + ` NOT NULL,
 former BOOLEAN NOT NULL DEFAULT FALSE,
 PRIMARY KEY (calendar_id, version, user_id),
 FOREIGN KEY (calendar_id, version) REFERENCES calendar_keys(calendar_id, version) ON DELETE CASCADE
);
CREATE INDEX calendar_key_holders_user ON calendar_key_holders(user_id);
ALTER TABLE worker_keys ADD COLUMN key_version BIGINT NOT NULL DEFAULT 1;
ALTER TABLE calendar_objects ADD COLUMN content_sizes TEXT NOT NULL DEFAULT '{}';
ALTER TABLE calendar_objects ADD COLUMN alarm_bytes BIGINT NOT NULL DEFAULT 0;
`
	n, o := ObjectBytes("NEW"), ObjectBytes("OLD")
	if postgres {
		return s + `CREATE OR REPLACE FUNCTION calendar_usage_track() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE d BIGINT;
BEGIN
    IF TG_OP = 'INSERT' THEN d := ` + n + `;
    ELSIF TG_OP = 'UPDATE' THEN d := ` + n + ` - ` + o + `;
    ELSE d := -` + o + `;
    END IF;
    UPDATE calendar_usage SET bytes = bytes + d WHERE id = 1;
    RETURN NULL;
END $$;
DROP TRIGGER calendar_usage_track ON calendar_objects;
CREATE TRIGGER calendar_usage_track AFTER INSERT OR UPDATE OF data, sealed, plain_size OR DELETE ON calendar_objects
FOR EACH ROW EXECUTE FUNCTION calendar_usage_track();
CREATE OR REPLACE FUNCTION org_usage_track() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN UPDATE org_usage SET bytes = bytes + ` + n + ` WHERE org_id = NEW.org_id;
    ELSIF TG_OP = 'UPDATE' THEN UPDATE org_usage SET bytes = bytes + ` + n + ` - ` + o + ` WHERE org_id = NEW.org_id;
    ELSE UPDATE org_usage SET bytes = bytes - ` + o + ` WHERE org_id = OLD.org_id;
    END IF;
    RETURN NULL;
END $$;
DROP TRIGGER org_usage_track ON calendar_objects;
CREATE TRIGGER org_usage_track AFTER INSERT OR UPDATE OF data, sealed, plain_size OR DELETE ON calendar_objects FOR EACH ROW EXECUTE FUNCTION org_usage_track();`
	}
	return s + `DROP TRIGGER calendar_usage_insert;
DROP TRIGGER calendar_usage_update;
DROP TRIGGER calendar_usage_delete;
CREATE TRIGGER calendar_usage_insert AFTER INSERT ON calendar_objects
BEGIN UPDATE calendar_usage SET bytes = bytes + ` + n + ` WHERE id = 1; END;
CREATE TRIGGER calendar_usage_update AFTER UPDATE OF data, sealed, plain_size ON calendar_objects
BEGIN UPDATE calendar_usage SET bytes = bytes - ` + o + ` + ` + n + ` WHERE id = 1; END;
CREATE TRIGGER calendar_usage_delete AFTER DELETE ON calendar_objects
BEGIN UPDATE calendar_usage SET bytes = bytes - ` + o + ` WHERE id = 1; END;
DROP TRIGGER org_usage_insert;
DROP TRIGGER org_usage_update;
DROP TRIGGER org_usage_delete;
CREATE TRIGGER org_usage_insert AFTER INSERT ON calendar_objects
BEGIN UPDATE org_usage SET bytes = bytes + ` + n + ` WHERE org_id = NEW.org_id; END;
CREATE TRIGGER org_usage_update AFTER UPDATE OF data, sealed, plain_size ON calendar_objects
BEGIN UPDATE org_usage SET bytes = bytes - ` + o + ` + ` + n + ` WHERE org_id = NEW.org_id; END;
CREATE TRIGGER org_usage_delete AFTER DELETE ON calendar_objects
BEGIN UPDATE org_usage SET bytes = bytes - ` + o + ` WHERE org_id = OLD.org_id; END;`
}

// DefaultOrgID is the one org of a single-tenant install (KyIdentity's default-org ID).
const DefaultOrgID = "00000000-0000-0000-0000-000000000001"

// OrgOwnedTables carry org_id (docs/superpowers/specs/2026-11-20-tenancy-design.md, Inventory).
// audit_records' is nullable: NULL is a platform row.
var OrgOwnedTables = []string{"users", "device_pairings", "groups", "group_members", "audit_records",
	"calendars", "calendar_objects", "calendar_grants", "scheduling_outbox", "invitation_byte_budget",
	"suite_group_calendars"}

// orgParents are the org-owned rows each table joins; its org_id must equal theirs.
var orgParents = []struct{ table, column, parent string }{
	{"group_members", "user_id", "users"}, {"group_members", "group_id", "groups"},
	{"calendar_grants", "calendar_id", "calendars"}, {"calendar_grants", "group_id", "groups"},
	{"suite_group_calendars", "calendar_id", "calendars"}, {"suite_group_calendars", "group_id", "groups"},
	{"calendar_objects", "calendar_id", "calendars"},
}

// orgModel: the org registry, org_id on every org-owned table, and the triggers that keep rows in
// one existing org, unchanged for life. Existing rows join the default org, created only when any
// org-owned table holds rows: an existing install is single-tenant, and PostgreSQL's foreign key
// must find the org for every row. A fresh database gets its org from store.ensureOrgs.
// The default stays on org_id: a multi-tenant install has no default org, so an insert that
// forgets org_id fails the existence check there. org_usage holds
// each org's object bytes, kept like calendar_usage, keyed by the object's own org_id so a
// cascaded calendar delete still finds it.
func orgModel(postgres bool) string {
	existing := make([]string, len(OrgOwnedTables))
	for i, t := range OrgOwnedTables {
		existing[i] = "EXISTS (SELECT 1 FROM " + t + ")"
	}
	populated := strings.Join(existing, " OR ")
	var b strings.Builder
	b.WriteString(`CREATE TABLE orgs (
 id TEXT PRIMARY KEY,
 status TEXT NOT NULL CHECK (status IN ('active', 'suspended', 'deletion_scheduled', 'deleted', 'stale')),
 revision BIGINT NOT NULL DEFAULT 0,
 enabled BOOLEAN NOT NULL DEFAULT TRUE,
 quotas_json TEXT NOT NULL DEFAULT '{}',
 seen_at BIGINT NOT NULL DEFAULT 0,
 purged_at BIGINT
);
CREATE TABLE org_usage (
 org_id TEXT PRIMARY KEY REFERENCES orgs(id),
 bytes BIGINT NOT NULL
);
INSERT INTO orgs (id, status) SELECT '` + DefaultOrgID + `', 'active' WHERE ` + populated + `;
INSERT INTO org_usage (org_id, bytes) SELECT '` + DefaultOrgID + `', COALESCE((SELECT SUM(LENGTH(data)) FROM calendar_objects), 0) WHERE ` + populated + `;
`)
	if postgres {
		b.WriteString(`CREATE FUNCTION org_id_fixed() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.org_id IS DISTINCT FROM OLD.org_id THEN RAISE EXCEPTION 'org_id cannot change'; END IF;
    RETURN NEW;
END $$;
`)
	}
	for _, t := range OrgOwnedTables {
		notNull := " NOT NULL"
		if t == "audit_records" {
			notNull = ""
		}
		if postgres {
			fmt.Fprintf(&b, "ALTER TABLE %s ADD COLUMN org_id TEXT%s DEFAULT '%s' REFERENCES orgs(id);\n", t, notNull, DefaultOrgID)
			fmt.Fprintf(&b, "CREATE TRIGGER %s_org_fixed BEFORE UPDATE OF org_id ON %s FOR EACH ROW EXECUTE FUNCTION org_id_fixed();\n", t, t)
			continue
		}
		// SQLite cannot add a REFERENCES column with a non-NULL default while foreign keys are on:
		// a trigger stands in for the foreign key.
		fmt.Fprintf(&b, "ALTER TABLE %s ADD COLUMN org_id TEXT%s DEFAULT '%s';\n", t, notNull, DefaultOrgID)
		fmt.Fprintf(&b, `CREATE TRIGGER %[1]s_org_exists BEFORE INSERT ON %[1]s
WHEN NEW.org_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM orgs WHERE id = NEW.org_id)
BEGIN SELECT RAISE(ABORT, 'org_id names no org'); END;
CREATE TRIGGER %[1]s_org_fixed BEFORE UPDATE OF org_id ON %[1]s
WHEN NEW.org_id IS NOT OLD.org_id
BEGIN SELECT RAISE(ABORT, 'org_id cannot change'); END;
`, t)
	}
	if postgres {
		b.WriteString(`ALTER TABLE users ADD CONSTRAINT users_org_id_id UNIQUE (org_id, id);
ALTER TABLE groups ADD CONSTRAINT groups_org_id_id UNIQUE (org_id, id);
ALTER TABLE calendars ADD CONSTRAINT calendars_org_id_id UNIQUE (org_id, id);
`)
		for _, p := range orgParents {
			fmt.Fprintf(&b, "ALTER TABLE %s ADD FOREIGN KEY (org_id, %s) REFERENCES %s(org_id, id) ON DELETE CASCADE;\n", p.table, p.column, p.parent)
		}
		b.WriteString(`CREATE FUNCTION calendar_owner_org() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.owner_kind = 'user' AND EXISTS (SELECT 1 FROM users WHERE id = NEW.owner_id AND org_id <> NEW.org_id) THEN
        RAISE EXCEPTION 'org_id differs from its owner''s';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER calendars_owner_org BEFORE INSERT OR UPDATE OF owner_kind, owner_id ON calendars FOR EACH ROW EXECUTE FUNCTION calendar_owner_org();
CREATE FUNCTION org_usage_track() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN UPDATE org_usage SET bytes = bytes + LENGTH(NEW.data) WHERE org_id = NEW.org_id;
    ELSIF TG_OP = 'UPDATE' THEN UPDATE org_usage SET bytes = bytes + LENGTH(NEW.data) - LENGTH(OLD.data) WHERE org_id = NEW.org_id;
    ELSE UPDATE org_usage SET bytes = bytes - LENGTH(OLD.data) WHERE org_id = OLD.org_id;
    END IF;
    RETURN NULL;
END $$;
CREATE TRIGGER org_usage_track AFTER INSERT OR UPDATE OF data OR DELETE ON calendar_objects FOR EACH ROW EXECUTE FUNCTION org_usage_track();`)
		return b.String()
	}
	// SQLite: the composite foreign keys as triggers. Nothing moves a row to another parent, so
	// inserts are the only writes that can join two orgs.
	for _, p := range orgParents {
		fmt.Fprintf(&b, `CREATE TRIGGER %[1]s_%[2]s_org BEFORE INSERT ON %[1]s
WHEN EXISTS (SELECT 1 FROM %[3]s WHERE id = NEW.%[2]s AND org_id IS NOT NEW.org_id)
BEGIN SELECT RAISE(ABORT, 'org_id differs from its parent''s'); END;
`, p.table, p.column, p.parent)
	}
	b.WriteString(`CREATE TRIGGER calendars_owner_org BEFORE INSERT ON calendars
WHEN NEW.owner_kind = 'user' AND EXISTS (SELECT 1 FROM users WHERE id = NEW.owner_id AND org_id IS NOT NEW.org_id)
BEGIN SELECT RAISE(ABORT, 'org_id differs from its owner''s'); END;
CREATE TRIGGER org_usage_insert AFTER INSERT ON calendar_objects
BEGIN UPDATE org_usage SET bytes = bytes + LENGTH(NEW.data) WHERE org_id = NEW.org_id; END;
CREATE TRIGGER org_usage_update AFTER UPDATE OF data ON calendar_objects
BEGIN UPDATE org_usage SET bytes = bytes - LENGTH(OLD.data) + LENGTH(NEW.data) WHERE org_id = NEW.org_id; END;
CREATE TRIGGER org_usage_delete AFTER DELETE ON calendar_objects
BEGIN UPDATE org_usage SET bytes = bytes - LENGTH(OLD.data) WHERE org_id = OLD.org_id; END;`)
	return b.String()
}

// replicaCoordination: state PostgreSQL replicas share, on database time in milliseconds.
// security_windows holds rate windows (expires_at is the window's generation),
// spent_challenges the used CAPTCHA salts, kypost_replay the verified KyPost event IDs (attempt:
// the request that wrote the record, so it alone can undo it). SQLite
// keeps all three in memory and leaves these tables empty.
const replicaCoordination = `CREATE TABLE security_windows (
 key TEXT PRIMARY KEY,
 hits INTEGER NOT NULL,
 expires_at BIGINT NOT NULL
);
CREATE INDEX idx_security_windows_expiry ON security_windows(expires_at);
CREATE TABLE spent_challenges (
 salt TEXT PRIMARY KEY,
 expires_at BIGINT NOT NULL
);
CREATE INDEX idx_spent_challenges_expiry ON spent_challenges(expires_at);
CREATE TABLE kypost_replay (
 event_id TEXT PRIMARY KEY,
 expires_at BIGINT NOT NULL,
 attempt TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_kypost_replay_expiry ON kypost_replay(expires_at);`

// suiteGroupCalendars: the group calendar each KyIdentity suite group has here, keyed on the
// KyIdentity group ID only (never its name or slug), with the local group that holds its
// members. archived makes the calendar read-only. A calendar an administrator deletes takes
// its row with it; reconcile then makes a new one for a group that is still active.
const suiteGroupCalendars = `CREATE TABLE suite_group_calendars (
 suite_group_id TEXT PRIMARY KEY,
 calendar_id TEXT NOT NULL UNIQUE REFERENCES calendars(id) ON DELETE CASCADE,
 group_id TEXT NOT NULL UNIQUE REFERENCES groups(id) ON DELETE CASCADE,
 archived BOOLEAN NOT NULL DEFAULT FALSE
);`

// resources: rooms and equipment an administrator defines. Tags is a JSON list. A resource is
// never deleted: disabling keeps its history. Groups who may book (audience groups) and
// approvers (approval approvers) are their own tables.
func resources(ts string) string {
	return `CREATE TABLE resources (
 id TEXT PRIMARY KEY,
 kind TEXT NOT NULL CHECK (kind IN ('room', 'equipment')),
 name TEXT NOT NULL,
 location TEXT NOT NULL DEFAULT '',
 capacity INTEGER NOT NULL DEFAULT 0,
 tags TEXT NOT NULL DEFAULT '[]',
 audience TEXT NOT NULL CHECK (audience IN ('everyone', 'groups')),
 approval TEXT NOT NULL CHECK (approval IN ('auto', 'approvers')),
 max_minutes INTEGER NOT NULL,
 horizon_days INTEGER NOT NULL,
 enabled BOOLEAN NOT NULL DEFAULT TRUE,
 created_at ` + ts + ` NOT NULL,
 updated_at ` + ts + ` NOT NULL
);
CREATE UNIQUE INDEX resources_name ON resources(lower(name));
CREATE TABLE resource_groups (
 resource_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
 group_id TEXT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
 PRIMARY KEY (resource_id, group_id)
);
CREATE INDEX resource_groups_group ON resource_groups(group_id);
CREATE TABLE resource_approvers (
 resource_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 PRIMARY KEY (resource_id, user_id)
);
CREATE INDEX resource_approvers_user ON resource_approvers(user_id);`
}

// kypostOutOfOffice: the out-of-office state KyPost last sent for a user (KyPost is the source
// of truth). revision only grows; times are unix seconds.
const kypostOutOfOffice = `CREATE TABLE kypost_out_of_office (
 user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 revision BIGINT NOT NULL,
 away BOOLEAN NOT NULL,
 start_at BIGINT NOT NULL,
 end_at BIGINT NOT NULL,
 all_day BOOLEAN NOT NULL,
 zone TEXT NOT NULL,
 tentative BOOLEAN NOT NULL,
 comment_local TEXT NOT NULL,
 comment_remote TEXT NOT NULL
);`

// bookingPages: a member's booking types, each with its public slug, and one row per hold or
// booking. A hold is status held until expires_at; a confirmed booking's event is an ordinary
// calendar object (calendar_id, object_name). Times are unix seconds.
func bookingPages(ts string) string {
	return `CREATE TABLE booking_types (
 id TEXT PRIMARY KEY,
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 slug TEXT NOT NULL UNIQUE,
 enabled BOOLEAN NOT NULL DEFAULT FALSE,
 title TEXT NOT NULL,
 duration_min INTEGER NOT NULL,
 zone TEXT NOT NULL,
 windows TEXT NOT NULL,
 buffer_before_min INTEGER NOT NULL DEFAULT 0,
 buffer_after_min INTEGER NOT NULL DEFAULT 0,
 notice_min INTEGER NOT NULL,
 horizon_days INTEGER NOT NULL,
 daily_cap INTEGER NOT NULL DEFAULT 0,
 calendar_id TEXT NOT NULL,
 meeting_room BOOLEAN NOT NULL DEFAULT FALSE,
 created_at ` + ts + ` NOT NULL,
 updated_at ` + ts + ` NOT NULL
);
CREATE INDEX booking_types_user ON booking_types(user_id);
CREATE TABLE bookings (
 id TEXT PRIMARY KEY,
 type_id TEXT NOT NULL REFERENCES booking_types(id) ON DELETE CASCADE,
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 status TEXT NOT NULL,
 start_at BIGINT NOT NULL,
 end_at BIGINT NOT NULL,
 expires_at BIGINT NOT NULL DEFAULT 0,
 email TEXT NOT NULL DEFAULT '',
 email_hash TEXT NOT NULL DEFAULT '',
 name TEXT NOT NULL DEFAULT '',
 note TEXT NOT NULL DEFAULT '',
 confirm_hash TEXT NOT NULL DEFAULT '',
 manage_hash TEXT NOT NULL DEFAULT '',
 calendar_id TEXT NOT NULL DEFAULT '',
 object_name TEXT NOT NULL DEFAULT '',
 uid TEXT NOT NULL,
 created_at ` + ts + ` NOT NULL
);
CREATE INDEX bookings_user_time ON bookings(user_id, status, start_at);
CREATE INDEX bookings_email ON bookings(email_hash, status);`
}

// meetingGuests: one row per attendee outside the organisation on a meeting, with the opaque
// key KyMessages knows them by and their guest link, sealed. guests_error is why KyMessages
// took no guests last time; refresh_at is when the guest windows are sent again.
func meetingGuests(ts string) string {
	return `CREATE TABLE meeting_guests (
 meeting_id TEXT NOT NULL REFERENCES video_meetings(id) ON DELETE CASCADE,
 email TEXT NOT NULL,
 guest_key TEXT NOT NULL,
 sealed_url TEXT NOT NULL DEFAULT '',
 PRIMARY KEY (meeting_id, email)
);
ALTER TABLE video_meetings ADD COLUMN guests_error TEXT NOT NULL DEFAULT '';
ALTER TABLE video_meetings ADD COLUMN refresh_at ` + ts + `;`
}

// videoMeetings: one row per KyMessages room, which is also its outbox. A write to the event
// bumps rev in the write's transaction; the worker syncs until done_rev catches up. A closed
// row is kept so its ID is never reused.
func videoMeetings(ts string) string {
	return `CREATE TABLE video_meetings (
 id TEXT PRIMARY KEY,
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 calendar_id TEXT NOT NULL,
 name TEXT NOT NULL,
 state TEXT NOT NULL,
 member_url TEXT NOT NULL DEFAULT '',
 synced TEXT NOT NULL DEFAULT '',
 rev BIGINT NOT NULL DEFAULT 1,
 done_rev BIGINT NOT NULL DEFAULT 0,
 attempts INTEGER NOT NULL DEFAULT 0,
 next_attempt_at ` + ts + ` NOT NULL,
 last_error TEXT NOT NULL DEFAULT '',
 created_at ` + ts + ` NOT NULL
);
CREATE UNIQUE INDEX video_meetings_object ON video_meetings(calendar_id, name) WHERE state = 'open';
CREATE INDEX video_meetings_due ON video_meetings(state, next_attempt_at);`
}

// taskTables: what KyCalendar keeps about tasks beyond CalDAV, never task text: each user's
// areas (JSON grouping of task lists), one row per web completion, and saved views. Times are
// unix seconds.
const taskTables = `CREATE TABLE task_areas (
 user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 data TEXT NOT NULL
);
CREATE TABLE task_completions (
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 calendar_id TEXT NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
 uid TEXT NOT NULL,
 completed_at BIGINT NOT NULL
);
CREATE INDEX task_completions_user ON task_completions(user_id, completed_at);
CREATE TABLE task_views (
 id TEXT PRIMARY KEY,
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 name TEXT NOT NULL,
 query TEXT NOT NULL,
 created_at BIGINT NOT NULL
);
CREATE INDEX task_views_user ON task_views(user_id);
`

// taskInsights: each completion names its occurrence, so one seen twice (web, then a phone's
// sync) is one row. The log is rebuilt so older rows, which may share a second, each get a
// distinct placeholder before the unique index exists. Goals are KyCalendar-only.
const taskInsights = `CREATE TABLE task_completions_v33 (
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 calendar_id TEXT NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
 uid TEXT NOT NULL,
 occurrence TEXT NOT NULL,
 completed_at BIGINT NOT NULL
);
INSERT INTO task_completions_v33 (user_id, calendar_id, uid, occurrence, completed_at)
 SELECT user_id, calendar_id, uid,
  'at:' || completed_at || ':' || ROW_NUMBER() OVER (PARTITION BY user_id, calendar_id, uid, completed_at ORDER BY completed_at),
  completed_at FROM task_completions;
DROP TABLE task_completions;
ALTER TABLE task_completions_v33 RENAME TO task_completions;
CREATE INDEX task_completions_user ON task_completions(user_id, completed_at);
CREATE UNIQUE INDEX task_completions_once ON task_completions(user_id, calendar_id, uid, occurrence);
CREATE TABLE task_goals (
 id TEXT PRIMARY KEY,
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 title TEXT NOT NULL,
 period TEXT NOT NULL,
 starts TEXT NOT NULL,
 ends TEXT NOT NULL,
 areas TEXT NOT NULL,
 lists TEXT NOT NULL,
 created_at BIGINT NOT NULL
);
CREATE INDEX task_goals_user ON task_goals(user_id);`

// taskPrefs: per-user task settings; no row means the defaults (flagged mail makes tasks).
const taskPrefs = `CREATE TABLE task_prefs (
 user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 kypost_flags BOOLEAN NOT NULL
);`

// The task search index (title, notes, tags) per calendar object, reconciled at search time.
// SQLite: FTS5, pruned daily of deleted calendars (a virtual table has no foreign key).
const taskSearchSQLite = `CREATE VIRTUAL TABLE task_search USING fts5(calendar_id UNINDEXED, uid UNINDEXED, etag UNINDEXED, body, tokenize = 'unicode61 remove_diacritics 2');`

const taskSearchPostgres = `CREATE TABLE task_search (
 calendar_id TEXT NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
 uid TEXT NOT NULL,
 etag TEXT NOT NULL,
 body TEXT NOT NULL,
 PRIMARY KEY (calendar_id, uid)
);`

const cancellationReservations = `CREATE TABLE cancellation_reservations (
 calendar_id TEXT NOT NULL,
 name TEXT NOT NULL,
 sender_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 bytes BIGINT NOT NULL CHECK(bytes>=0),
 PRIMARY KEY(calendar_id,name),
 FOREIGN KEY(calendar_id,name) REFERENCES calendar_objects(calendar_id,name) ON DELETE CASCADE
);
CREATE INDEX cancellation_reservations_sender ON cancellation_reservations(sender_id);`

const invitationSponsorship = `ALTER TABLE calendar_objects ADD COLUMN invitation_sender TEXT NOT NULL DEFAULT '';
ALTER TABLE calendar_objects ADD COLUMN invitation_attendee TEXT NOT NULL DEFAULT '';
ALTER TABLE calendar_objects ADD COLUMN invitation_recipient TEXT NOT NULL DEFAULT '';
CREATE TABLE invitation_byte_budget (sender_id TEXT PRIMARY KEY, window_start BIGINT NOT NULL, bytes BIGINT NOT NULL);`

const sessionExportOnly = `ALTER TABLE sessions ADD COLUMN export_only BOOLEAN NOT NULL DEFAULT FALSE;`

const sessionUnproven = `ALTER TABLE sessions ADD COLUMN unproven BOOLEAN NOT NULL DEFAULT FALSE;
UPDATE sessions SET unproven = TRUE WHERE user_id IN (SELECT id FROM users WHERE sso_provider <> 'local');`

const clearOIDCEmails = `UPDATE users SET email = '' WHERE sso_provider = 'oidc' AND email <> '';`

const dropUnreachableSSOSessions = `DELETE FROM sessions WHERE sso_sid = '' AND user_id IN (SELECT id FROM users WHERE sso_provider <> 'local');
DELETE FROM mfa_challenges WHERE user_id IN (SELECT id FROM users WHERE sso_provider <> 'local');`

const calendarComponents = `ALTER TABLE calendars ADD COLUMN components TEXT NOT NULL DEFAULT 'VEVENT';
ALTER TABLE calendar_objects ADD COLUMN component TEXT NOT NULL DEFAULT 'VEVENT';`

const calendarUsageTable = `CREATE TABLE calendar_usage (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    bytes BIGINT NOT NULL
);
INSERT INTO calendar_usage (id, bytes) SELECT 1, COALESCE(SUM(LENGTH(data)), 0) FROM calendar_objects;`

const lookupIndexes = `CREATE INDEX idx_users_username_lower ON users (LOWER(username));
CREATE INDEX idx_users_email_lower ON users (LOWER(email));
CREATE INDEX idx_group_members_user ON group_members (user_id);
CREATE INDEX idx_mfa_challenges_user ON mfa_challenges (user_id);
CREATE INDEX idx_pairings_user ON device_pairings (user_id);`

const groupSource = `ALTER TABLE groups ADD COLUMN source TEXT NOT NULL DEFAULT 'local';
UPDATE groups SET source = 'scim';`

// userSSOIssuer records the binding each SSO account belongs to. Existing active rows of the
// stored binding's kind get that binding: they were provisioned under it. Inactive rows may be a
// previous issuer's, disabled by an earlier change, and stay unstamped (refused at login). With
// no binding nothing is stamped; the first binding stamps its kind's rows.
const userSSOIssuer = `ALTER TABLE users ADD COLUMN sso_issuer TEXT NOT NULL DEFAULT '';
UPDATE users SET sso_issuer = (SELECT value FROM server_settings WHERE key = 'signin_bound')
WHERE status = 'active' AND ((sso_provider IN ('kysignon', 'scim') AND EXISTS (SELECT 1 FROM server_settings WHERE key = 'signin_bound' AND value LIKE 'kyidentity %'))
   OR (sso_provider = 'oidc' AND EXISTS (SELECT 1 FROM server_settings WHERE key = 'signin_bound' AND value LIKE 'oidc %')));`

const revokeSSOAdminSessions = `DELETE FROM sessions WHERE user_id IN (SELECT id FROM users WHERE role = 'admin' AND sso_provider IN ('kysignon', 'scim'));`

// Run executes all pending migrations for the specified database driver.
func Run(ctx context.Context, db *sql.DB, driver string) error {
	driver = strings.ToLower(driver)
	if driver == "postgresql" {
		driver = "postgres"
	}
	if driver == "postgres" {
		// Replicas starting together apply each migration once: the same session lock as the
		// first-admin bootstrap (store.Serialized "bootstrap").
		return withBootstrapLock(ctx, db, func() error { return run(ctx, db, driver) })
	}
	return run(ctx, db, driver)
}

// BootstrapLockSQL takes the bootstrap session lock, key space 3 scoped to the schema.
const BootstrapLockSQL = `SELECT pg_advisory_lock(3, hashtext(current_schema() || ':bootstrap'))`

// withBootstrapLock holds the bootstrap lock on a connection of its own, discarded afterwards
// so the session lock can never return to the pool.
func withBootstrapLock(ctx context.Context, db *sql.DB, work func() error) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	defer conn.Raw(func(any) error { return driver.ErrBadConn })
	if _, err := conn.ExecContext(ctx, BootstrapLockSQL); err != nil {
		return fmt.Errorf("bootstrap lock: %w", err)
	}
	return work()
}

// LatestVersion is the schema version this build migrates to.
func LatestVersion() int { return registry[len(registry)-1].Version }

// execer is a database or one transaction.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// RunTx migrates inside tx, all or nothing, for a target written in the same transaction
// (migrate-postgres). The caller holds whatever lock it needs.
func RunTx(ctx context.Context, tx *sql.Tx, driver string) error {
	if _, err := tx.ExecContext(ctx, initTable(driver)); err != nil {
		return fmt.Errorf("failed to init schema_migrations: %w", err)
	}
	for _, m := range registry {
		if err := apply(ctx, tx, driver, m); err != nil {
			return err
		}
	}
	return nil
}

func initTable(driver string) string {
	if driver == "postgres" {
		return `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    applied_at TIMESTAMPTZ NOT NULL
);`
	}
	return `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    applied_at DATETIME NOT NULL
);`
}

// apply runs m in ex unless it is recorded.
func apply(ctx context.Context, ex execer, driver string, m Migration) error {
	q := "SELECT COUNT(1) FROM schema_migrations WHERE version = ?"
	if driver == "postgres" {
		q = "SELECT COUNT(1) FROM schema_migrations WHERE version = $1"
	}
	var exists int
	if err := ex.QueryRowContext(ctx, q, m.Version).Scan(&exists); err != nil {
		return fmt.Errorf("failed to check migration version %d: %w", m.Version, err)
	}
	if exists > 0 {
		return nil
	}
	ddl := m.SQLite
	record := "INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)"
	if driver == "postgres" {
		ddl = m.Postgres
		record = "INSERT INTO schema_migrations (version, name, applied_at) VALUES ($1, $2, $3)"
	}
	if _, err := ex.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("failed executing migration v%d (%s): %w", m.Version, m.Name, err)
	}
	if _, err := ex.ExecContext(ctx, record, m.Version, m.Name, time.Now().UTC()); err != nil {
		return fmt.Errorf("failed to record migration v%d: %w", m.Version, err)
	}
	return nil
}

func run(ctx context.Context, db *sql.DB, driver string) error {
	if _, err := db.ExecContext(ctx, initTable(driver)); err != nil {
		return fmt.Errorf("failed to init schema_migrations: %w", err)
	}
	for _, m := range registry {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("failed to begin migration tx for v%d: %w", m.Version, err)
		}
		if err := apply(ctx, tx, driver, m); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit migration v%d: %w", m.Version, err)
		}
	}
	return nil
}

// presencePushes: one row per member, the outbox of their busy blocks for KyMessages
// (docs/SUITE-INTEGRATIONS.md section P). A write to one of their calendars bumps rev in the
// write's transaction; refresh_at makes the row due again every 6 hours. No event is stored.
func presencePushes(ts string) string {
	return `CREATE TABLE presence_pushes (
 user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 rev BIGINT NOT NULL DEFAULT 1,
 done_rev BIGINT NOT NULL DEFAULT 0,
 attempts INTEGER NOT NULL DEFAULT 0,
 next_attempt_at ` + ts + ` NOT NULL,
 refresh_at ` + ts + `
);
CREATE INDEX presence_pushes_due ON presence_pushes(next_attempt_at);`
}

// resourceBookings: one row per resource an event object names, with the answer KyCalendar gave
// for it, and the busy spans (unix seconds) of accepted and pending bookings up to the horizon.
// open_after (not 0) is where the spans of a series that goes on stop. Both go with the event
// object (and so with its calendar).
func resourceBookings(ts string) string {
	return `CREATE TABLE resource_bookings (
 id TEXT PRIMARY KEY,
 resource_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
 calendar_id TEXT NOT NULL,
 object_name TEXT NOT NULL,
 booked_by TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL CHECK (status IN ('accepted', 'pending', 'declined')),
 reason TEXT NOT NULL DEFAULT '',
 detail TEXT NOT NULL DEFAULT '',
 fingerprint TEXT NOT NULL,
 decided_by TEXT NOT NULL DEFAULT '',
 open_after BIGINT NOT NULL DEFAULT 0,
 created_at ` + ts + ` NOT NULL,
 updated_at ` + ts + ` NOT NULL,
 UNIQUE (calendar_id, object_name, resource_id),
 FOREIGN KEY (calendar_id, object_name) REFERENCES calendar_objects(calendar_id, name) ON DELETE CASCADE
);
CREATE INDEX resource_bookings_resource ON resource_bookings(resource_id, status);
CREATE TABLE resource_busy (
 booking_id TEXT NOT NULL REFERENCES resource_bookings(id) ON DELETE CASCADE,
 resource_id TEXT NOT NULL,
 start_at BIGINT NOT NULL,
 end_at BIGINT NOT NULL
);
CREATE INDEX resource_busy_time ON resource_busy(resource_id, start_at);
CREATE INDEX resource_busy_booking ON resource_busy(booking_id);`
}
