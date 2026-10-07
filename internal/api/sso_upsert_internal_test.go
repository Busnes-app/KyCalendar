package api

import (
	"context"
	"errors"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/access"
	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// The admin grant follows the `roles` claim at every login. A change revokes app passwords,
// so a promoted user's phones stop syncing, but the personal calendar stays for migration.
func TestUpsertSSOUserFollowsRolesClaim(t *testing.T) {
	s, _ := davInternalServer(t)
	ctx := context.Background()
	claims := &sso.IdentityClaims{Subject: "sub-1", PreferredUsername: "carol", Provider: "kysignon"}

	u, err := s.upsertSSOUser(ctx, claims)
	if err != nil || u.Role != "user" {
		t.Fatalf("first login: %+v %v", u, err)
	}
	cal := &store.Calendar{ID: "cal_carol", OwnerKind: "user", OwnerID: u.ID, Slug: "default", Name: "Calendar"}
	if err := s.store.Calendars().CreateCalendar(ctx, cal, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.store.AppPasswords().Create(ctx, &store.AppPassword{ID: "pw_carol", UserID: u.ID, Label: "phone", Hash: "h"}); err != nil {
		t.Fatal(err)
	}

	claims.Roles = []string{access.AdminAppRole}
	if u, err = s.upsertSSOUser(ctx, claims); err != nil || u.Role != "admin" {
		t.Fatalf("promotion: %+v %v", u, err)
	}
	if list, _ := s.store.AppPasswords().ListByUser(ctx, u.ID); len(list) != 0 {
		t.Fatal("promotion kept the app passwords")
	}
	if _, err := s.store.Calendars().GetCalendarBySlug(ctx, "user", u.ID, "default"); err != nil {
		t.Fatalf("promotion removed the personal calendar: %v", err)
	}

	claims.Roles = []string{"admin", "kypost.admin"}
	if u, err = s.upsertSSOUser(ctx, claims); err != nil || u.Role != "user" {
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
	if _, err := s.upsertSSOUser(ctx, claims); !errors.Is(err, errAccountInactive) {
		t.Fatalf("inactive login: want errAccountInactive, got %v", err)
	}
}

// KyIdentity's SCIM externalId is the ID token's sub: a provisioned user who signs in is the
// same row, so the group calendars SCIM membership grants reach them.
func TestUpsertSSOUserAdoptsSCIMUser(t *testing.T) {
	s, _ := davInternalServer(t)
	ctx := context.Background()
	alice := &store.User{ID: "usr_alice", Username: "alice", Role: "user", Status: "active", SSOProvider: "scim", SSOSubject: "sub-x"}
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

	u, err := s.upsertSSOUser(ctx, &sso.IdentityClaims{Subject: "sub-x", PreferredUsername: "alice", Provider: "kysignon"})
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
	if _, err := s.upsertSSOUser(ctx, &sso.IdentityClaims{Subject: "sub-x", Provider: "oidc"}); !errors.Is(err, errNotProvisioned) {
		t.Fatalf("oidc sub adopted a SCIM user: %v", err)
	}
}
