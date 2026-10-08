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
	// ErrLastAdmin refuses a change that would leave no active local administrator.
	ErrLastAdmin = errors.New("last active local administrator")
	// ErrActorRevoked refuses an administrator's write once they are no longer an active
	// administrator or their session is gone.
	ErrActorRevoked = errors.New("acting administrator revoked")
	// ErrAlreadyBound: ReattachSSOUser found the account already bound to the target binding and
	// wrote nothing; its status is the identity provider's.
	ErrAlreadyBound = errors.New("account already bound to this sign-in")
	// ErrAccessChanged: UpdateSCIMUser would grant access to an account whose access changed since
	// SCIM read it; the caller retries from a fresh read.
	ErrAccessChanged = errors.New("account access changed since it was read")
)

// Actor is who makes an access write. An administrator is rechecked inside the write's
// transaction; System (the CLI) is not. The zero Actor is refused.
type Actor struct {
	userID, sessionHash string
	system              bool
}

// System is the operator at the console.
var System = Actor{system: true}

// AdminActor is an administrator acting through the session with token hash sessionHash.
func AdminActor(userID, sessionHash string) Actor {
	return Actor{userID: userID, sessionHash: sessionHash}
}

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

	// ResetAfterRestore ends every session, MFA challenge and app password and writes a new
	// sync epoch, in one transaction, so nothing issued after the backup is believed.
	ResetAfterRestore(ctx context.Context) error

	Driver() string
	Ping(ctx context.Context) error
	Close() error
}

// UserStore defines repository operations for accounts.
type UserStore interface {
	CreateUser(ctx context.Context, u *User) error
	// CreateUserAs is CreateUser by actor: ErrActorRevoked unless actor is still an active
	// administrator with a live session, checked under the local-admins lock.
	CreateUserAs(ctx context.Context, actor Actor, u *User) error
	GetUserByID(ctx context.Context, id string) (*User, error)
	GetUserByUsername(ctx context.Context, username string) (*User, error)
	GetUserByEmail(ctx context.Context, email string) (*User, error)
	GetUserBySSO(ctx context.Context, provider, subject string) (*User, error)
	UpdateUser(ctx context.Context, u *User) error
	ResetAdminPassword(ctx context.Context, userID, newHash string) error
	// ResetPassword is the operator reset for any local account: new hash, forced change,
	// grants revoked. Role and status are untouched. Not local or missing: ErrNotFound.
	// An actor no longer an active administrator with a live session: ErrActorRevoked.
	ResetPassword(ctx context.Context, actor Actor, userID, newHash string) error
	// RenameUser changes only a local account's username and audits it as actor in one
	// transaction. Not local or missing: ErrNotFound; name taken: ErrAlreadyExists.
	RenameUser(ctx context.Context, actor, userID, newName string) error
	// UpdateProfile sets a local account's display name and email and nothing else. Not local
	// or missing: ErrNotFound.
	UpdateProfile(ctx context.Context, userID, displayName, email string) error
	// SetRole and SetStatus change a local account's role or status and, in the same
	// transaction, delete its sessions, MFA challenges, device pairings and app passwords. A
	// change that would leave no active local administrator is ErrLastAdmin. Not local or
	// missing: ErrNotFound. An actor no longer an active administrator with a live session:
	// ErrActorRevoked, checked in the same transaction before the write.
	SetRole(ctx context.Context, actor Actor, userID, role string) error
	SetStatus(ctx context.Context, actor Actor, userID, status string) error
	// SetSSORole sets the role of an active SSO account and nothing else, so a login never writes
	// back a status it read before a concurrent deactivation, and in the same transaction (row
	// first) deletes its sessions, MFA challenges, device pairings and app passwords. Inactive,
	// local or missing: ErrNotFound.
	SetSSORole(ctx context.Context, userID, role string) error
	// UpdateSCIMUser writes a non-local account's username, email and display name and only the
	// access the request changes relative to what SCIM read (expectedRole, expectedStatus). A
	// removal (deactivation, demotion) always lands first, with the revocation of every grant, in
	// its own row-first transaction. A grant (activation, promotion) lands only together with the
	// profile write and only if the stored value still equals what SCIM read, else
	// ErrAccessChanged and nothing more is written. Local or missing: ErrNotFound; a username
	// another row holds exactly: ErrAlreadyExists.
	UpdateSCIMUser(ctx context.Context, u *User, expectedRole, expectedStatus string) error
	// RevokeSSOUser deletes a non-local account's sessions, MFA challenges, device pairings and
	// app passwords in one transaction, after setting it inactive when deactivate is true (rows
	// first). It never sets an account active. Local or missing: ErrNotFound.
	RevokeSSOUser(ctx context.Context, userID string, deactivate bool) error
	// UpdateKySignOnProfile sets a kysignon account's display name and email and nothing else.
	// Any other provider or missing: ErrNotFound.
	UpdateKySignOnProfile(ctx context.Context, userID, displayName, email string) error
	// BindSignIn is one transaction under the local-admins lock: ErrActorRevoked unless actor is
	// System or still an active administrator with a live session; then every active account
	// whose sso_provider is in b.Disable becomes inactive (local accounts never, whatever the list)
	// and the sessions, MFA challenges, device pairings and app passwords of every account of
	// those providers are deleted; then b.Stamp, when set, becomes the sso_issuer of every
	// unstamped account of b.StampProviders; then settings are written, an empty value deleting
	// its key. It returns how many accounts it deactivated. Any failure writes nothing.
	BindSignIn(ctx context.Context, actor Actor, b SignInBinding, settings map[string]string) (int, error)
	// ReattachSSOUser is the admin's one write on an SSO account, under the local-admins lock:
	// ErrActorRevoked unless actor is still an active administrator with a live session; then the
	// account's sso_issuer becomes binding and its status active, and its sessions, MFA challenges,
	// device pairings and app passwords are deleted. The role is untouched. It returns the previous
	// sso_issuer. Local or missing: ErrNotFound. Already bound to binding: ErrAlreadyBound, and
	// nothing is written or revoked.
	ReattachSSOUser(ctx context.Context, actor Actor, userID, binding string) (string, error)
	// CountSSOAccounts counts the active accounts BindSignIn would deactivate.
	CountSSOAccounts(ctx context.Context, providers []string) (int, error)
	CompletePasswordChange(ctx context.Context, userID, oldHash, newHash, ip string) error
	UpdateRecoveryCodes(ctx context.Context, userID, oldHashes, newHashes string) error
	// SpendTOTPCounter records counter as used. It returns ErrAlreadyExists when counter is
	// not greater than the stored one, which is how a replayed code inside the skew window fails.
	SpendTOTPCounter(ctx context.Context, userID string, counter int64) error
	DeleteUser(ctx context.Context, id string) error
	ListUsers(ctx context.Context, offset, limit int, filter UserFilter) ([]*User, int, error)
	// SSOUserIDs returns those of ids that name an existing non-local account, in any order.
	SSOUserIDs(ctx context.Context, ids []string) ([]string, error)
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
	// CreateGroup and UpdateGroup refuse (ErrAlreadyExists) a name another group holds in any case.
	CreateGroup(ctx context.Context, g *Group) error
	GetGroupByID(ctx context.Context, id string) (*Group, error)
	GetGroupByName(ctx context.Context, name string) (*Group, error)
	UpdateGroup(ctx context.Context, g *Group) error
	DeleteGroup(ctx context.Context, id string) error
	// ListGroups pages groups by name; source "" lists every owner.
	ListGroups(ctx context.Context, offset, limit int, source string) ([]*Group, int, error)
	AddGroupMember(ctx context.Context, groupID, userID string) error
	RemoveGroupMember(ctx context.Context, groupID, userID string) error
	GetUserGroups(ctx context.Context, userID string) ([]*Group, error)
}

// AuditStore logs security events.
type AuditStore interface {
	LogAudit(ctx context.Context, r *AuditRecord) error
	ListAuditRecords(ctx context.Context, offset, limit int) ([]*AuditRecord, int, error)
}

// SignInBinding is what BindSignIn applies to SSO accounts.
type SignInBinding struct {
	Disable        []string // providers whose accounts are deactivated
	Stamp          string   // the binding stamped on unstamped accounts; "" stamps nothing
	StampProviders []string // providers whose unstamped accounts get Stamp
}

// SettingsStore handles persistent key-value configuration.
type SettingsStore interface {
	GetSetting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, val string) error
	DeleteSetting(ctx context.Context, key string) error
	GetAllSettings(ctx context.Context) (map[string]string, error)
}

// Grantor is who issues an app password: a signed-in session, rechecked inside the insert's
// transaction, or Seed (fixtures), which is not. The zero Grantor is refused.
type Grantor struct {
	sessionHash, passwordHash string
	seed                      bool
}

// Seed issues without a session: test and fixture data only.
var Seed = Grantor{seed: true}

// SessionGrantor is the session with token hash sessionHash, of a user whose password hash was
// passwordHash when the request was authenticated.
func SessionGrantor(sessionHash, passwordHash string) Grantor {
	return Grantor{sessionHash: sessionHash, passwordHash: passwordHash}
}

// AppPasswordStore persists per-user app passwords for native CalDAV clients.
type AppPasswordStore interface {
	// Create stores p in one transaction. A session grantor first locks the user row as session
	// issuance does (active, password unchanged) and rechecks that its session is live and the
	// user's, so a credential is never minted after a purge (role, status, reattach, password
	// change) committed: ErrSessionExpired. Then the user's count is checked against max (0 = no
	// limit; ErrQuotaExceeded) and p inserted.
	Create(ctx context.Context, by Grantor, p *AppPassword, max int) error
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
