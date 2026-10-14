package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// The SCIM screen learns whether SCIM is on, where the IdP connects and which accounts SCIM
// manages; never the bearer token.
func TestAdminSCIMStatusNeverShowsTheToken(t *testing.T) {
	srv, st, cfg := setupTestServer(t)
	ctx := context.Background()
	bound := "kyidentity https://a.example"
	if err := st.Settings().SetSetting(ctx, sso.KeyBound, bound); err != nil {
		t.Fatal(err)
	}
	for _, u := range []*store.User{
		{ID: "usr_in", Username: "synced", Role: "user", Status: "active", SSOProvider: "scim", SSOIssuer: bound},
		{ID: "usr_oidc", Username: "oidc", Role: "user", Status: "active", SSOProvider: "oidc", SSOIssuer: "oidc https://o.example"},
	} {
		if err := st.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	w := do(t, srv, "GET", "/api/admin/scim", loginAs(t, srv, st, "alice", "admin"))
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), cfg.SCIM.BearerToken) {
		t.Fatal("the SCIM bearer token left the server")
	}
	var got struct {
		Enabled         bool   `json:"enabled"`
		TokenConfigured bool   `json:"token_configured"`
		Endpoint        string `json:"endpoint"`
		Total           int    `json:"total"`
		Users           []struct {
			ID string `json:"id"`
		} `json:"users"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Enabled || !got.TokenConfigured || got.Endpoint != cfg.Server.AppURL+"/scim/v2" {
		t.Fatalf("status %+v", got)
	}
	if got.Total != 1 || len(got.Users) != 1 || got.Users[0].ID != "usr_in" {
		t.Fatalf("users %+v, want usr_in only (not the local admin or the OIDC login)", got)
	}
}
