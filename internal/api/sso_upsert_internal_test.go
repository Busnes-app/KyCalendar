package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/access"
	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// kyBinding is the live KyIdentity provider these tests sign in through.
const kyBinding = "kyidentity https://id.example"

// The admin grant follows the `roles` claim at every login. A change revokes app passwords,
// so a promoted user's phones stop syncing, but the personal calendar stays for migration.
func TestUpsertSSOUserFollowsRolesClaim(t *testing.T) {
	s, _ := davInternalServer(t)
	ctx := context.Background()
	claims := &sso.IdentityClaims{Subject: "sub-1", PreferredUsername: "carol", Provider: "kysignon"}

	u, err := s.upsertSSOUser(ctx, claims, kyBinding)
	if err != nil || u.Role != "user" {
		t.Fatalf("first login: %+v %v", u, err)
	}
	cal := &store.Calendar{ID: "cal_carol", OwnerKind: "user", OwnerID: u.ID, Slug: "default", Name: "Calendar"}
	if err := s.store.Calendars().CreateCalendar(ctx, cal, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.store.AppPasswords().Create(ctx, store.Seed, &store.AppPassword{ID: "pw_carol", UserID: u.ID, Label: "phone", Hash: "h"}, 0); err != nil {
		t.Fatal(err)
	}

	claims.Roles = []string{access.AdminAppRole}
	if u, err = s.upsertSSOUser(ctx, claims, kyBinding); err != nil || u.Role != "admin" {
		t.Fatalf("promotion: %+v %v", u, err)
	}
	if list, _ := s.store.AppPasswords().ListByUser(ctx, u.ID); len(list) != 0 {
		t.Fatal("promotion kept the app passwords")
	}
	if _, err := s.store.Calendars().GetCalendarBySlug(ctx, "user", u.ID, "default"); err != nil {
		t.Fatalf("promotion removed the personal calendar: %v", err)
	}

	claims.Roles = []string{"admin", "kypost.admin"}
	if u, err = s.upsertSSOUser(ctx, claims, kyBinding); err != nil || u.Role != "user" {
		t.Fatalf("global admin must not keep the grant: %+v %v", u, err)
	}
	recs, _, _ := s.store.Audit().ListAuditRecords(ctx, 0, 50)
	changes := 0
	for _, r := range recs {
		if r.Action == "sso.role_changed" {
			changes++
		}
	}
	if changes != 2 {
		t.Fatalf("want 2 sso.role_changed audit rows, got %d", changes)
	}

	u.Status = "inactive"
	if err := s.store.Users().UpdateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if _, err := s.upsertSSOUser(ctx, claims, kyBinding); !errors.Is(err, errAccountInactive) {
		t.Fatalf("inactive login: want errAccountInactive, got %v", err)
	}
}

// KyIdentity's SCIM externalId is the ID token's sub: a provisioned user who signs in is the
// same row, so the group calendars SCIM membership grants reach them.
func TestUpsertSSOUserAdoptsSCIMUser(t *testing.T) {
	s, _ := davInternalServer(t)
	ctx := context.Background()
	alice := &store.User{ID: "usr_alice", Username: "alice", Role: "user", Status: "active", SSOProvider: "scim", SSOSubject: "sub-x", SSOIssuer: kyBinding}
	if err := s.store.Users().CreateUser(ctx, alice); err != nil {
		t.Fatal(err)
	}
	cal := &store.Calendar{ID: "cal_team", OwnerKind: "group", OwnerID: "cal_team", Slug: "group", Name: "Team"}
	if err := s.store.Calendars().CreateCalendar(ctx, cal, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.store.Groups().CreateGroup(ctx, &store.Group{ID: "grp_team", DisplayName: "Team"}); err != nil {
		t.Fatal(err)
	}
	if err := s.store.Groups().AddGroupMember(ctx, "grp_team", alice.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.store.Calendars().SetGrant(ctx, store.CalendarGrant{CalendarID: cal.ID, GroupID: "grp_team", Role: "reader"}); err != nil {
		t.Fatal(err)
	}

	u, err := s.upsertSSOUser(ctx, &sso.IdentityClaims{Subject: "sub-x", PreferredUsername: "alice", Provider: "kysignon"}, kyBinding)
	if err != nil || u.ID != alice.ID {
		t.Fatalf("login: want %s, got %+v %v", alice.ID, u, err)
	}
	if _, total, _ := s.store.Users().ListUsers(ctx, 0, 10, store.UserFilter{}); total != 1 {
		t.Fatalf("login created a second user: %d users", total)
	}
	if grants, err := s.store.Calendars().UserGrants(ctx, u.ID); err != nil || len(grants) == 0 {
		t.Fatalf("group grant does not reach the signed-in user: %v %v", grants, err)
	}

	// Only KySignOn subjects are KyIdentity IDs; a generic OIDC sub must not adopt the row.
	s.config.SSO.AutoProvision = false
	if _, err := s.upsertSSOUser(ctx, &sso.IdentityClaims{Subject: "sub-x", Provider: "oidc"}, "oidc https://acme.example"); !errors.Is(err, errNotProvisioned) {
		t.Fatalf("oidc sub adopted a SCIM user: %v", err)
	}
}

type failingUpdates struct{ store.Store }
type failingUserStore struct{ store.UserStore }

func (f failingUpdates) Users() store.UserStore { return failingUserStore{f.Store.Users()} }
func (failingUserStore) SetSSORole(context.Context, string, string) error {
	return errors.New("update failed")
}

// Revocation precedes the role write: if storing the promotion fails, the old everyday
// session is already gone, and the next login still sees a change and revokes again.
func TestUpsertSSOUserRevokesBeforeStoringRole(t *testing.T) {
	s, _ := davInternalServer(t)
	ctx := context.Background()
	u := &store.User{ID: "usr_eve", Username: "eve", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "sub-e", SSOIssuer: kyBinding}
	if err := s.store.Users().CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := s.store.Sessions().CreateSession(ctx, &store.Session{TokenHash: "tok_eve", UserID: u.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.store.AppPasswords().Create(ctx, store.Seed, &store.AppPassword{ID: "pw_eve", UserID: u.ID, Label: "phone", Hash: "h"}, 0); err != nil {
		t.Fatal(err)
	}
	real := s.store
	s.store = failingUpdates{real}
	if _, err := s.upsertSSOUser(ctx, &sso.IdentityClaims{Subject: "sub-e", Provider: "kysignon", Roles: []string{access.AdminAppRole}}, kyBinding); err == nil {
		t.Fatal("want the update error")
	}
	if _, err := real.Sessions().GetSession(ctx, "tok_eve"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("everyday session survived a failed promotion: %v", err)
	}
	if list, _ := real.AppPasswords().ListByUser(ctx, u.ID); len(list) != 0 {
		t.Fatal("app passwords survived a failed promotion")
	}
	if got, _ := real.Users().GetUserByID(ctx, u.ID); got.Role != "user" {
		t.Fatalf("role stored despite the failure: %q", got.Role)
	}
}

// A local account owns its name: an IdP user with the same username is refused, never linked,
// because linking by name would let whoever holds that IdP username take the local account.
func TestUpsertSSOUserRefusesTakenUsername(t *testing.T) {
	s, _ := davInternalServer(t)
	ctx := context.Background()
	if err := s.store.Users().CreateUser(ctx, &store.User{ID: "usr_local_admin", Username: "admin", Role: "admin", Status: "active", SSOProvider: "local"}); err != nil {
		t.Fatal(err)
	}
	claims := &sso.IdentityClaims{Subject: "sub-admin", PreferredUsername: "admin", Provider: "kysignon", Roles: []string{access.AdminAppRole}}
	if _, err := s.upsertSSOUser(ctx, claims, kyBinding); !errors.Is(err, errUsernameTaken) {
		t.Fatalf("want errUsernameTaken, got %v", err)
	}
	local, err := s.store.Users().GetUserByUsername(ctx, "admin")
	if err != nil || local.ID != "usr_local_admin" || local.SSOProvider != "local" {
		t.Fatalf("local account changed: %+v %v", local, err)
	}
}

type deactivateAfterRead struct{ store.Store }
type deactivateAfterReadUsers struct{ store.UserStore }

func (d deactivateAfterRead) Users() store.UserStore {
	return deactivateAfterReadUsers{d.Store.Users()}
}
func (u deactivateAfterReadUsers) GetUserBySSO(ctx context.Context, provider, subject string) (*store.User, error) {
	got, err := u.UserStore.GetUserBySSO(ctx, provider, subject)
	if err == nil {
		stale := *got
		stale.Status = "inactive"
		if err := u.UserStore.UpdateUser(ctx, &stale); err != nil {
			return nil, err
		}
	}
	return got, err
}

// A row deactivated (SCIM, the webhook) after the login read it stays inactive: the role write
// touches the role of an active row only, never writing back the status it read.
func TestUpsertSSOUserRoleChangeKeepsAConcurrentDeactivation(t *testing.T) {
	s, _ := davInternalServer(t)
	ctx := context.Background()
	createUsers(t, s, &store.User{ID: "usr_fay", Username: "fay", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "sub-f", SSOIssuer: kyBinding})
	real := s.store
	s.store = deactivateAfterRead{real}
	if _, err := s.upsertSSOUser(ctx, &sso.IdentityClaims{Subject: "sub-f", Provider: "kysignon", Roles: []string{access.AdminAppRole}}, kyBinding); !errors.Is(err, errAccountInactive) {
		t.Fatalf("role change on a row deactivated meanwhile: %v, want errAccountInactive", err)
	}
	if got, _ := real.Users().GetUserByID(ctx, "usr_fay"); got.Status != "inactive" || got.Role != "user" {
		t.Fatalf("fay: %s %s, want inactive user", got.Status, got.Role)
	}
}
