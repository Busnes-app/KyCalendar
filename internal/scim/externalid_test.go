package scim_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/elimity-com/scim/schema"

	"github.com/Busnes-app/kycalendar/internal/store"
)

func listIDs(t *testing.T, h http.Handler, token, path, filter string) (int, []string) {
	t.Helper()
	w := scimDo(t, h, token, "GET", path+"?startIndex=1&count=2&filter="+url.QueryEscape(filter), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("%s %s: %d %s", path, filter, w.Code, w.Body.String())
	}
	var page struct {
		Total     int `json:"totalResults"`
		Resources []struct {
			ID         string `json:"id"`
			ExternalID string `json:"externalId"`
		} `json:"Resources"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &page)
	var ids []string
	for _, r := range page.Resources {
		ids = append(ids, r.ID)
	}
	return page.Total, ids
}

// KyIdentity finds the account it provisions by `externalId eq` (its user ID, the KySignOn sub):
// exact and case-sensitive, never a local row, never a row of another sign-in binding.
func TestSCIMUsersFilterByExternalID(t *testing.T) {
	h, st, token := scimWithStore(t)
	ctx := context.Background()
	const bound = "kyidentity https://id.example"
	if err := st.Settings().SetSetting(ctx, "signin_bound", bound); err != nil {
		t.Fatal(err)
	}
	for _, u := range []*store.User{
		{ID: "usr_scim", Username: "s", SSOProvider: "scim", SSOSubject: "kid-1", SSOIssuer: bound},
		{ID: "usr_kys", Username: "k", SSOProvider: "kysignon", SSOSubject: "kid-2", SSOIssuer: bound},
		{ID: "usr_old", Username: "o", SSOProvider: "kysignon", SSOSubject: "kid-3", SSOIssuer: "kyidentity https://old.example"},
		{ID: "usr_local", Username: "l", SSOProvider: "local", SSOSubject: "kid-4"},
		{ID: "usr_oidc", Username: "c", SSOProvider: "oidc", SSOSubject: "kid-5", SSOIssuer: bound},
	} {
		u.Role, u.Status = "user", "active"
		if err := st.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	for filter, want := range map[string]string{
		`externalId eq "kid-1"`: "usr_scim",
		`externalId eq "kid-2"`: "usr_kys",
		`externalId eq "KID-1"`: "",
		`externalId eq "kid-3"`: "", // another binding's row
		`externalId eq "kid-4"`: "", // local
		`externalId eq "kid-5"`: "", // an OIDC subject is not a KyIdentity ID
		`externalId eq "nope"`:  "",
	} {
		total, ids := listIDs(t, h, token, "/scim/v2/Users", filter)
		if want == "" && (total != 0 || len(ids) != 0) || want != "" && (total != 1 || len(ids) != 1 || ids[0] != want) {
			t.Errorf("%s: total %d %v, want %q", filter, total, ids, want)
		}
	}
}

// KyIdentity finds a group the same way; only SCIM groups are visible.
func TestSCIMGroupsFilterByExternalID(t *testing.T) {
	h, st, token := scimWithStore(t)
	ctx := context.Background()
	w := scimDo(t, h, token, "POST", "/scim/v2/Groups", map[string]any{"schemas": []string{schema.GroupSchema}, "displayName": "Crew", "externalId": "kgrp-1"})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var crew struct{ ID string }
	_ = json.Unmarshal(w.Body.Bytes(), &crew)
	if w := scimDo(t, h, token, "POST", "/scim/v2/Groups", map[string]any{"schemas": []string{schema.GroupSchema}, "displayName": "Other", "externalId": "kgrp-2"}); w.Code != http.StatusCreated {
		t.Fatalf("create other: %d", w.Code)
	}
	if err := st.Groups().CreateGroup(ctx, &store.Group{ID: "grp_local", DisplayName: "Local", ExternalID: "kgrp-3"}); err != nil {
		t.Fatal(err)
	}
	if total, ids := listIDs(t, h, token, "/scim/v2/Groups", `externalId eq "kgrp-1"`); total != 1 || len(ids) != 1 || ids[0] != crew.ID {
		t.Errorf("kgrp-1: total %d %v, want %s", total, ids, crew.ID)
	}
	for _, ext := range []string{"kgrp-3", "KGRP-1", "nope"} {
		if total, ids := listIDs(t, h, token, "/scim/v2/Groups", `externalId eq "`+ext+`"`); total != 0 || len(ids) != 0 {
			t.Errorf("%s: total %d %v, want none", ext, total, ids)
		}
	}
	if w := scimDo(t, h, token, "GET", "/scim/v2/Groups?filter="+url.QueryEscape(`members eq "x"`), nil); w.Code != http.StatusBadRequest {
		t.Errorf("unsupported group filter: %d, want 400", w.Code)
	}
}
