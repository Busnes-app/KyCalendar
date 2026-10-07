package store

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound       = errors.New("record not found")
	ErrAlreadyExists  = errors.New("record already exists")
	ErrSessionExpired = errors.New("session expired")
	ErrPairingExpired = errors.New("pairing session expired")

	ErrPreconditionFailed = errors.New("precondition failed")
	ErrUIDConflict        = errors.New("uid already used in calendar")
	ErrSyncTokenExpired   = errors.New("sync token expired")
	ErrQuotaExceeded      = errors.New("owner quota exceeded")
)

// Store defines the unified storage contract implemented across SQLite, PostgreSQL, and MySQL.
type Store interface {
	Users() UserStore
	Sessions() SessionStore
	Devices() DeviceStore
	Groups() GroupStore
	Calendars() CalendarStore
	AppPasswords() AppPasswordStore
	Audit() AuditStore
	Settings() SettingsStore

	Driver() string
	Ping(ctx context.Context) error
	Close() error
}

// UserStore defines repository operations for accounts.
type UserStore interface {
	CreateUser(ctx context.Context, u *User) error
	GetUserByID(ctx context.Context, id string) (*User, error)
	GetUserByUsername(ctx context.Context, username string) (*User, error)
	GetUserByEmail(ctx context.Context, email string) (*User, error)
	GetUserBySSO(ctx context.Context, provider, subject string) (*User, error)
	UpdateUser(ctx context.Context, u *User) error
	ResetAdminPassword(ctx context.Context, userID, newHash string) error
	CompletePasswordChange(ctx context.Context, userID, oldHash, newHash, ip string) error
	UpdateRecoveryCodes(ctx context.Context, userID, oldHashes, newHashes string) error
	// SpendTOTPCounter records counter as used. It returns ErrAlreadyExists when counter is
	// not greater than the stored one, which is how a replayed code inside the skew window fails.
	SpendTOTPCounter(ctx context.Context, userID string, counter int64) error
	DeleteUser(ctx context.Context, id string) error
	ListUsers(ctx context.Context, offset, limit int, filter UserFilter) ([]*User, int, error)
	CountUsers(ctx context.Context) (int, error)
}

// SessionStore defines repository operations for active login sessions.
type SessionStore interface {
	CreateSession(ctx context.Context, s *Session, expectedPasswordHash string) error
	GetSession(ctx context.Context, tokenHash string) (*Session, error)
	DeleteSession(ctx context.Context, tokenHash string) error
	DeleteUserSessions(ctx context.Context, userID string) error
	CleanExpiredSessions(ctx context.Context) error
	CreateMFAChallenge(ctx context.Context, challenge *MFAChallenge, expectedPasswordHash string) error
	ConsumeMFAChallenge(ctx context.Context, tokenHash string) (userID, passwordHash string, err error)
}

// DeviceStore handles 90s ephemeral QR pairing sessions and paired push clients.
type DeviceStore interface {
	CreatePairing(ctx context.Context, p *DevicePairing) error
	GetPairingBySecret(ctx context.Context, secret string) (*DevicePairing, error)
	ConsumePairing(ctx context.Context, secret, deviceName, platform, pushToken string) error
	CleanExpiredPairings(ctx context.Context) error
}

// GroupStore defines repository operations for SCIM and RBAC groups.
type GroupStore interface {
	CreateGroup(ctx context.Context, g *Group) error
	GetGroupByID(ctx context.Context, id string) (*Group, error)
	GetGroupByName(ctx context.Context, name string) (*Group, error)
	UpdateGroup(ctx context.Context, g *Group) error
	DeleteGroup(ctx context.Context, id string) error
	ListGroups(ctx context.Context, offset, limit int) ([]*Group, int, error)
	AddGroupMember(ctx context.Context, groupID, userID string) error
	RemoveGroupMember(ctx context.Context, groupID, userID string) error
	GetUserGroups(ctx context.Context, userID string) ([]*Group, error)
}

// AuditStore logs security events.
type AuditStore interface {
	LogAudit(ctx context.Context, r *AuditRecord) error
	ListAuditRecords(ctx context.Context, offset, limit int) ([]*AuditRecord, int, error)
}

// SettingsStore handles persistent key-value configuration.
type SettingsStore interface {
	GetSetting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, val string) error
	DeleteSetting(ctx context.Context, key string) error
	GetAllSettings(ctx context.Context) (map[string]string, error)
}

// AppPasswordStore persists per-user app passwords for native CalDAV clients.
type AppPasswordStore interface {
	Create(ctx context.Context, p *AppPassword) error
	Get(ctx context.Context, id string) (*AppPassword, error)
	ListByUser(ctx context.Context, userID string) ([]*AppPassword, error)
	Delete(ctx context.Context, userID, id string) error // ErrNotFound if not the user's
	DeleteByUser(ctx context.Context, userID string) error
	TouchLastUsed(ctx context.Context, id string, at time.Time) error
}

// CalendarStore persists calendars, their objects and the per-calendar change log.
type CalendarStore interface {
	// CreateCalendar refuses with ErrQuotaExceeded when the owner already has maxPerOwner calendars (0 = no limit).
	CreateCalendar(ctx context.Context, c *Calendar, maxPerOwner int) error
	GetCalendarBySlug(ctx context.Context, ownerKind, ownerID, slug string) (*Calendar, error)
	ListCalendarsByOwner(ctx context.Context, ownerKind, ownerID string) ([]*Calendar, error)
	GetCalendarByID(ctx context.Context, id string) (*Calendar, error)
	ListCalendarsByKind(ctx context.Context, ownerKind string) ([]*Calendar, error)
	// DeleteCalendar removes a calendar with its objects, changes and grants.
	DeleteCalendar(ctx context.Context, id string) error
	ListGrants(ctx context.Context, calendarID string) ([]CalendarGrant, error)
	// SetGrant creates or replaces one group's role; ErrNotFound unless the calendar is a
	// group calendar and the group exists.
	SetGrant(ctx context.Context, g CalendarGrant) error
	DeleteGrant(ctx context.Context, calendarID, groupID string) error // absent is not an error
	// UserGrants lists every grant that reaches userID through group membership.
	UserGrants(ctx context.Context, userID string) ([]CalendarGrant, error)
	UpdateCalendar(ctx context.Context, id string, name, description, color *string) error
	GetObject(ctx context.Context, calendarID, name string) (*CalendarObject, error)
	GetObjectByUID(ctx context.Context, calendarID, uid string) (*CalendarObject, error)
	ListObjects(ctx context.Context, calendarID string) ([]*CalendarObject, error)
	ListObjectsInRange(ctx context.Context, calendarID string, start, end int64) ([]*CalendarObject, error)
	// PutObject enforces lim across every calendar of the target calendar's owner, atomically with the write.
	PutObject(ctx context.Context, o *CalendarObject, ifMatch string, ifNoneMatch bool, lim OwnerLimits) (created bool, err error)
	DeleteObject(ctx context.Context, calendarID, name, ifMatch string) error
	ChangesSince(ctx context.Context, calendarID string, seq int64) ([]CalendarChange, error)
	PruneChanges(ctx context.Context, before time.Time) error
	SyncEpoch(ctx context.Context) (string, error)
}
