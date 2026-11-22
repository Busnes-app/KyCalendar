package main

import (
	"context"
	"errors"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/store"
)

// purge-org purges only a stale or deleted org of a multi-tenant install: a single-tenant one,
// an org KyIdentity still lists, and the staff org are refused.
func TestPurgeOrgOnlyPurgesAStaleOrg(t *testing.T) {
	st := openBootstrapStore(t)
	ctx := context.Background()
	if err := purgeOrg(ctx, st, "org-b"); err == nil {
		t.Fatal("single-tenant purge-org succeeded")
	}
	for k, v := range map[string]string{store.TenancySetting: "multi", store.StaffOrgSetting: "org-staff"} {
		if err := st.Settings().SetSetting(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	poll := func(orgs ...store.MirroredOrg) {
		t.Helper()
		if err := st.ApplyOrgs(ctx, "org-staff", orgs, 9); err != nil {
			t.Fatal(err)
		}
	}
	poll(store.MirroredOrg{ID: "org-a", Status: "active", Revision: 1, Enabled: true, Quotas: `{}`},
		store.MirroredOrg{ID: "org-b", Status: "active", Revision: 1, Enabled: true, Quotas: `{}`})
	if err := st.Users().CreateUser(ctx, &store.User{ID: "ub", Username: "b", Role: "user", Status: "active", SSOProvider: "local", OrgID: "org-b"}); err != nil {
		t.Fatal(err)
	}
	if err := st.ResetAfterRestore(ctx); err != nil { // both stale
		t.Fatal(err)
	}
	poll(store.MirroredOrg{ID: "org-a", Status: "active", Revision: 2, Enabled: true, Quotas: `{}`}) // org-b not listed: stays stale

	for _, org := range []store.OrgID{"org-a", "org-staff", "org-none"} {
		if err := purgeOrg(ctx, st, org); !errors.Is(err, store.ErrNotPurgeable) {
			t.Errorf("purge-org %s: %v", org, err)
		}
	}
	if err := purgeOrg(ctx, st, "org-b"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Users().GetUserByID(ctx, "ub"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("org-b's account after purge-org: %v", err)
	}
}
