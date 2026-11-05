package api_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/store"
)

func TestAdminEnableLiveKyIdentityOnly(t *testing.T) {
	ctx := context.Background()
	srv, st, _ := setupTestServer(t)
	admin := loginAs(t, srv, st, "root", "admin")
	carol := carolOf(t, st)
	enable := func() int { return call(t, srv, "POST", "/api/admin/users/"+carol.ID+"/enable", "", admin).Code }
	if got := enable(); got != http.StatusConflict {
		t.Fatalf("no live binding: %d", got)
	}
	liveAt(srv, "https://b.example")
	if got := enable(); got != http.StatusConflict {
		t.Fatalf("other binding: %d", got)
	}
	liveAt(srv, "https://a.example")
	restore := api.SetStepUpWindowForTest(0)
	if got := enable(); got != http.StatusForbidden {
		t.Fatalf("stale session enabled KyIdentity person: %d", got)
	}
	restore()

	if got := enable(); got != http.StatusOK {
		t.Fatalf("live binding enable: %d", got)
	}
	u, _ := st.Users().GetUserByID(ctx, carol.ID)
	if u.Status != "active" || u.SSOIssuer != bindingA || u.SSOProvider != "kysignon" {
		t.Fatalf("wrong account changed: %+v", u)
	}
	if _, err := st.Sessions().GetSession(ctx, crypto.SHA256Hex([]byte("tok-"+carol.ID))); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("session survived: %v", err)
	}
	if passwords, _ := st.AppPasswords().ListByUser(ctx, carol.ID); len(passwords) != 0 {
		t.Fatal("app password survived enable")
	}
	if got := enable(); got != http.StatusOK {
		t.Fatalf("idempotent enable: %d", got)
	}
	audits, _, err := st.Audit().ListAuditRecords(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, a := range audits {
		if a.Action == "admin.user_enable" && a.Resource == carol.ID {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("enable audit count: %d", n)
	}
	for _, provider := range []string{"scim", "oidc"} {
		u.SSOProvider, u.Status = provider, "inactive"
		if err := st.Users().UpdateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
		if got := enable(); got != http.StatusConflict {
			t.Fatalf("%s unexpectedly enabled: %d", provider, got)
		}
	}
}
