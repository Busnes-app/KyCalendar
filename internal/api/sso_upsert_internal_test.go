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
