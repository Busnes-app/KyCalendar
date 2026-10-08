package scim_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/scim"
	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

func setupSCIMServer(t *testing.T) (*scim.Server, *http.ServeMux, string) {
	t.Helper()
	st, err := store.Open(context.Background(), testdb.Config(t))
	if err != nil {
		t.Fatalf("failed to open test store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	token := "scim-secret-bearer-token"
	srv := scim.NewServer(st, config.SCIMConfig{
		Enabled:     true,
		BearerToken: token,
	}, "http://localhost:8080")

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux.Handle)
	return srv, mux, token
}

func TestSCIMUserLifecycle(t *testing.T) {
	srv, mux, token := setupSCIMServer(t)
	handler := srv.AuthMiddleware(mux)

	// 1. Create SCIM User
	userPayload := map[string]any{
		"schemas":     []string{scim.SchemaUser},
		"userName":    "scim_alice",
		"displayName": "Alice SCIM",
		"active":      true,
		"emails": []map[string]any{
			{"value": "scim_alice@busnes.app", "primary": true},
		},
	}
	body, _ := json.Marshal(userPayload)

	req := httptest.NewRequest("POST", "/scim/v2/Users", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", w.Code, w.Body.String())
	}

	var created struct {
		ID       string `json:"id"`
		UserName string `json:"userName"`
		Active   bool   `json:"active"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if created.UserName != "scim_alice" || !created.Active {
		t.Errorf("unexpected created SCIM user: %+v", created)
	}

	// 2. Query SCIM Users
	req = httptest.NewRequest("GET", "/scim/v2/Users?filter=userName%20eq%20%22scim_alice%22", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}

	var listResp struct {
		TotalResults int `json:"totalResults"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &listResp)
	if listResp.TotalResults != 1 {
		t.Errorf("expected 1 user result, got %d", listResp.TotalResults)
	}

	// 3. Patch SCIM User (Deactivate)
	patchPayload := map[string]any{
		"schemas": []string{scim.SchemaPatchOp},
		"Operations": []map[string]any{
			{"op": "replace", "path": "active", "value": false},
		},
	}
	patchBody, _ := json.Marshal(patchPayload)
	req = httptest.NewRequest("PATCH", "/scim/v2/Users/"+created.ID, bytes.NewReader(patchBody))
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK after patch, got %d: %s", w.Code, w.Body.String())
	}

	var patched struct {
		Active bool `json:"active"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &patched)
	if patched.Active {
		t.Errorf("expected user to be deactivated after patch")
	}

	// 4. Delete SCIM User
	req = httptest.NewRequest("DELETE", "/scim/v2/Users/"+created.ID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204 No Content, got %d", w.Code)
	}
}

func TestSCIMAuthEnforcement(t *testing.T) {
	srv, mux, _ := setupSCIMServer(t)
	handler := srv.AuthMiddleware(mux)

	// Missing token -> 401
	req := httptest.NewRequest("GET", "/scim/v2/Users", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized for missing token, got %d", w.Code)
	}
}

func TestSCIMDeactivationRevokesAppPasswords(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	token := "scim-secret-bearer-token"
	srv := scim.NewServer(st, config.SCIMConfig{Enabled: true, BearerToken: token}, "http://localhost:8080")
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux.Handle)
	handler := srv.AuthMiddleware(mux)

	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_ap", Username: "ap_user", Role: "user", Status: "active", SSOProvider: "scim"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AppPasswords().Create(ctx, store.Seed, &store.AppPassword{ID: "pw1", UserID: "usr_ap", Label: "phone", Hash: "h"}, 0); err != nil {
		t.Fatal(err)
	}

	patch, _ := json.Marshal(map[string]any{
		"schemas":    []string{scim.SchemaPatchOp},
		"Operations": []map[string]any{{"op": "replace", "path": "active", "value": false}},
	})
	req := httptest.NewRequest("PATCH", "/scim/v2/Users/usr_ap", bytes.NewReader(patch))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", w.Code, w.Body.String())
	}
	if list, _ := st.AppPasswords().ListByUser(ctx, "usr_ap"); len(list) != 0 {
		t.Fatalf("app passwords survived deactivation: %d", len(list))
	}
}

func scimDo(t *testing.T, h http.Handler, token, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r *bytes.Reader
	if body == nil {
		r = bytes.NewReader(nil)
	} else {
		raw, _ := json.Marshal(body)
		r = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, r)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// An IdP removes the admin role by sending no roles; the user must drop to everyday and lose
// every grant, or a deprovisioned administrator keeps the backup routes.
func TestSCIMRemovingRolesDemotesAdmin(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	token := "scim-secret-bearer-token"
	srv := scim.NewServer(st, config.SCIMConfig{Enabled: true, BearerToken: token}, "http://localhost:8080")
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux.Handle)
	handler := srv.AuthMiddleware(mux)

	cases := map[string]func(id string) (string, any){
		"put empty roles": func(id string) (string, any) {
			return "PUT", map[string]any{"schemas": []string{scim.SchemaUser}, "userName": id, "active": true, "roles": []any{}}
		},
		"put without roles": func(id string) (string, any) {
			return "PUT", map[string]any{"schemas": []string{scim.SchemaUser}, "userName": id, "active": true}
		},
		"patch remove roles": func(string) (string, any) {
			return "PATCH", map[string]any{"schemas": []string{scim.SchemaPatchOp}, "Operations": []map[string]any{{"op": "remove", "path": "roles"}}}
		},
		"patch remove filtered role": func(string) (string, any) {
			return "PATCH", map[string]any{"schemas": []string{scim.SchemaPatchOp}, "Operations": []map[string]any{{"op": "remove", "path": `roles[value eq "admin"]`}}}
		},
		"patch remove urn roles": func(string) (string, any) {
			return "PATCH", map[string]any{"schemas": []string{scim.SchemaPatchOp}, "Operations": []map[string]any{{"op": "remove", "path": scim.SchemaUser + ":roles"}}}
		},
		"patch replace empty roles": func(string) (string, any) {
			return "PATCH", map[string]any{"schemas": []string{scim.SchemaPatchOp}, "Operations": []map[string]any{{"op": "replace", "path": "roles", "value": []any{}}}}
		},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			id := "usr_" + strings.ReplaceAll(name, " ", "_")
			if err := st.Users().CreateUser(ctx, &store.User{ID: id, Username: id, Role: "admin", Status: "active", SSOProvider: "scim"}); err != nil {
				t.Fatal(err)
			}
			if err := st.AppPasswords().Create(ctx, store.Seed, &store.AppPassword{ID: "pw_" + id, UserID: id, Label: "x", Hash: "h"}, 0); err != nil {
				t.Fatal(err)
			}
			method, body := req(id)
			if w := scimDo(t, handler, token, method, "/scim/v2/Users/"+id, body); w.Code != http.StatusOK {
				t.Fatalf("%s: %d %s", method, w.Code, w.Body.String())
			}
			u, _ := st.Users().GetUserByID(ctx, id)
			if u.Role != "user" {
				t.Fatalf("role %q after removing roles", u.Role)
			}
			if list, _ := st.AppPasswords().ListByUser(ctx, id); len(list) != 0 {
				t.Fatal("grants survived the demotion")
			}
		})
	}
}

// eq is an exact, case-insensitive match on the named attribute; a substring hit would let an
// IdP update the wrong account.
func TestSCIMEqFilterIsExact(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	token := "scim-secret-bearer-token"
	srv := scim.NewServer(st, config.SCIMConfig{Enabled: true, BearerToken: token}, "http://localhost:8080")
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux.Handle)
	handler := srv.AuthMiddleware(mux)
	for _, u := range []*store.User{
		{ID: "usr_devops", Username: "devops", DisplayName: "ops", Email: "ops@example.com", Role: "user", Status: "active", SSOProvider: "scim"},
		{ID: "usr_ops", Username: "Ops", DisplayName: "Operations", Role: "user", Status: "active", SSOProvider: "scim"},
	} {
		if err := st.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	ids := func(filter string) []string {
		w := scimDo(t, handler, token, "GET", "/scim/v2/Users?filter="+url.QueryEscape(filter), nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", filter, w.Code, w.Body.String())
		}
		var page struct{ Resources []struct{ ID string } }
		_ = json.Unmarshal(w.Body.Bytes(), &page)
		var out []string
		for _, r := range page.Resources {
			out = append(out, r.ID)
		}
		return out
	}
	for filter, want := range map[string]string{
		`userName eq "ops"`:                       "usr_ops",
		`displayName eq "ops"`:                    "usr_devops",
		`emails.value eq "OPS@example.com"`:       "usr_devops",
		`userName eq "%"`:                         "",
		`userName eq "devops" or userName eq "x"`: "invalid",
	} {
		if want == "invalid" {
			if w := scimDo(t, handler, token, "GET", "/scim/v2/Users?filter="+url.QueryEscape(filter), nil); w.Code != http.StatusBadRequest {
				t.Errorf("%s: want 400, got %d", filter, w.Code)
			}
			continue
		}
		got := strings.Join(ids(filter), ",")
		if got != want {
			t.Errorf("%s: got %q want %q", filter, got, want)
		}
	}
}

func TestSCIMAdminNeedsTheAppRole(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	token := "scim-secret-bearer-token"
	srv := scim.NewServer(st, config.SCIMConfig{Enabled: true, BearerToken: token}, "http://localhost:8080")
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux.Handle)
	handler := srv.AuthMiddleware(mux)

	for value, want := range map[string]string{"kycalendar.admin": "admin", "admin": "user", "kypost.admin": "user"} {
		name := "u_" + strings.NewReplacer(".", "_").Replace(value)
		body := map[string]any{"schemas": []string{scim.SchemaUser}, "userName": name, "active": true,
			"roles": []any{map[string]any{"value": value, "primary": true}}}
		if w := scimDo(t, handler, token, "POST", "/scim/v2/Users", body); w.Code != http.StatusCreated {
			t.Fatalf("%s: %d %s", value, w.Code, w.Body.String())
		}
		u, err := st.Users().GetUserByUsername(ctx, name)
		if err != nil || u.Role != want {
			t.Fatalf("roles %q: role %q, want %q (%v)", value, u.Role, want, err)
		}
	}
}

// Deactivation revokes app passwords and keeps the personal calendar; reactivation finds it.
func TestSCIMDeactivationKeepsCalendars(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	token := "scim-secret-bearer-token"
	srv := scim.NewServer(st, config.SCIMConfig{Enabled: true, BearerToken: token}, "http://localhost:8080")
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux.Handle)
	handler := srv.AuthMiddleware(mux)

	id := "usr_dana"
	if err := st.Users().CreateUser(ctx, &store.User{ID: id, Username: "dana", Role: "user", Status: "active", SSOProvider: "scim"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Calendars().CreateCalendar(ctx, &store.Calendar{ID: "cal_dana", OwnerKind: "user", OwnerID: id, Slug: "default", Name: "Calendar"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.AppPasswords().Create(ctx, store.Seed, &store.AppPassword{ID: "pw_dana", UserID: id, Label: "phone", Hash: "h"}, 0); err != nil {
		t.Fatal(err)
	}
	patch := func(active bool) {
		body := map[string]any{"schemas": []string{scim.SchemaPatchOp}, "Operations": []map[string]any{{"op": "replace", "path": "active", "value": active}}}
		if w := scimDo(t, handler, token, "PATCH", "/scim/v2/Users/"+id, body); w.Code != http.StatusOK {
			t.Fatalf("PATCH active=%v: %d %s", active, w.Code, w.Body.String())
		}
	}
	patch(false)
	if u, _ := st.Users().GetUserByID(ctx, id); u.Status != "inactive" {
		t.Fatalf("status %q after deactivation", u.Status)
	}
	if list, _ := st.AppPasswords().ListByUser(ctx, id); len(list) != 0 {
		t.Fatal("deactivation kept the app passwords")
	}
	patch(true)
	if _, err := st.Calendars().GetCalendarBySlug(ctx, "user", id, "default"); err != nil {
		t.Fatalf("the personal calendar did not survive deactivation: %v", err)
	}
}

// A user who signed in through KySignOn first is the same person SCIM provisions later:
// KyIdentity's externalId is the ID token's sub. Create adopts the row instead of failing.
func TestSCIMCreateAdoptsKySignOnUser(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	token := "scim-secret-bearer-token"
	srv := scim.NewServer(st, config.SCIMConfig{Enabled: true, BearerToken: token}, "http://localhost:8080")
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux.Handle)
	handler := srv.AuthMiddleware(mux)

	id := "usr_yan"
	if err := st.Users().CreateUser(ctx, &store.User{ID: id, Username: "yan", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "sub-y", SSOIssuer: "kyidentity https://a.example"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AppPasswords().Create(ctx, store.Seed, &store.AppPassword{ID: "pw_yan", UserID: id, Label: "phone", Hash: "h"}, 0); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"schemas": []string{scim.SchemaUser}, "userName": "yan.k", "externalId": "sub-y", "displayName": "Yan K", "active": true,
		"roles": []any{map[string]any{"value": "kycalendar.admin"}}}

	// Under another binding (or none), yan's row is another issuer's: never adopted, 409.
	for _, bound := range []string{"kyidentity https://b.example", ""} {
		if err := st.Settings().SetSetting(ctx, "signin_bound", bound); err != nil {
			t.Fatal(err)
		}
		if w := scimDo(t, handler, token, "POST", "/scim/v2/Users", body); w.Code != http.StatusConflict {
			t.Fatalf("bound %q: adoption of another issuer's row: %d %s, want 409", bound, w.Code, w.Body.String())
		}
		if u, _ := st.Users().GetUserByID(ctx, id); u.Username != "yan" || u.Role != "user" {
			t.Fatalf("bound %q: a refused adoption changed the row: %+v", bound, u)
		}
		if _, total, _ := st.Users().ListUsers(ctx, 0, 10, store.UserFilter{}); total != 1 {
			t.Fatalf("bound %q: a refused adoption created a row: %d users", bound, total)
		}
	}

	// Under yan's own binding the provisioned user is the same person.
	if err := st.Settings().SetSetting(ctx, "signin_bound", "kyidentity https://a.example"); err != nil {
		t.Fatal(err)
	}
	w := scimDo(t, handler, token, "POST", "/scim/v2/Users", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if created.ID != id {
		t.Fatalf("created %q, want the existing %q", created.ID, id)
	}
	if _, total, _ := st.Users().ListUsers(ctx, 0, 10, store.UserFilter{}); total != 1 {
		t.Fatalf("a second user exists: %d users", total)
	}
	u, err := st.Users().GetUserByID(ctx, id)
	if err != nil || u.Username != "yan.k" || u.DisplayName != "Yan K" || u.Role != "admin" || u.SSOProvider != "kysignon" {
		t.Fatalf("SCIM attributes not applied: %+v %v", u, err)
	}
	if u.SSOIssuer != "kyidentity https://a.example" {
		t.Fatalf("adoption restamped the row to %q", u.SSOIssuer)
	}
	if err := st.Settings().SetSetting(ctx, "signin_bound", "kyidentity https://b.example"); err != nil {
		t.Fatal(err)
	}

	// A new row is stamped with the current binding.
	w = scimDo(t, handler, token, "POST", "/scim/v2/Users", map[string]any{"schemas": []string{scim.SchemaUser}, "userName": "zed", "externalId": "sub-z", "active": true})
	if w.Code != http.StatusCreated {
		t.Fatalf("create zed: %d %s", w.Code, w.Body.String())
	}
	if z, err := st.Users().GetUserBySSO(ctx, "scim", "sub-z"); err != nil || z.SSOIssuer != "kyidentity https://b.example" {
		t.Fatalf("new SCIM row: %+v %v, want the current binding", z, err)
	}
	if list, _ := st.AppPasswords().ListByUser(ctx, id); len(list) != 0 {
		t.Fatal("the role change kept the app passwords")
	}
}

type failingUpdates struct{ store.Store }
type failingUserStore struct{ store.UserStore }

func (f failingUpdates) Users() store.UserStore { return failingUserStore{f.Store.Users()} }
func (failingUserStore) UpdateSCIMUser(context.Context, *store.User) error {
	return errors.New("update refused")
}

// A role or status change and its revocation are one write: when it fails, the role, the status
// and the grants are as before, so no session ever runs under the new role.
func TestSCIMFailedPrivilegeChangeChangesNothing(t *testing.T) {
	cases := map[string]func(id string) (string, any){
		"put promotion": func(id string) (string, any) {
			return "PUT", map[string]any{"schemas": []string{scim.SchemaUser}, "userName": id, "active": true,
				"roles": []any{map[string]any{"value": "kycalendar.admin"}}}
		},
		"patch deactivation": func(string) (string, any) {
			return "PATCH", map[string]any{"schemas": []string{scim.SchemaPatchOp}, "Operations": []map[string]any{{"op": "replace", "path": "active", "value": false}}}
		},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			real, err := store.Open(ctx, testdb.Config(t))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = real.Close() })
			id := "usr_" + strings.ReplaceAll(name, " ", "_")
			if err := real.Users().CreateUser(ctx, &store.User{ID: id, Username: id, Role: "user", Status: "active", SSOProvider: "scim"}); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			if err := real.Sessions().CreateSession(ctx, &store.Session{TokenHash: "tok_" + id, UserID: id, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}, ""); err != nil {
				t.Fatal(err)
			}
			if err := real.AppPasswords().Create(ctx, store.Seed, &store.AppPassword{ID: "pw_" + id, UserID: id, Label: "phone", Hash: "h"}, 0); err != nil {
				t.Fatal(err)
			}
			token := "scim-secret-bearer-token"
			srv := scim.NewServer(failingUpdates{real}, config.SCIMConfig{Enabled: true, BearerToken: token}, "http://localhost:8080")
			mux := http.NewServeMux()
			srv.RegisterRoutes(mux.Handle)
			method, body := req(id)
			if w := scimDo(t, srv.AuthMiddleware(mux), token, method, "/scim/v2/Users/"+id, body); w.Code < 400 {
				t.Fatalf("%s succeeded despite the failing write: %d", method, w.Code)
			}
			if u, _ := real.Users().GetUserByID(ctx, id); u.Role != "user" || u.Status != "active" {
				t.Fatalf("change stored despite the failure: role %q status %q", u.Role, u.Status)
			}
		})
	}
}
