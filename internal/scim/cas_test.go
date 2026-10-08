package scim_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/scim"
	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

// raceBeforeWrite runs race after the handler read the account and just before the store write.
type raceBeforeWrite struct {
	store.UserStore
	race func()
}

func (u raceBeforeWrite) UpdateSCIMUser(ctx context.Context, user *store.User, role, status string) error {
	u.race()
	return u.UserStore.UpdateSCIMUser(ctx, user, role, status)
}

type racingStore struct {
	store.Store
	users store.UserStore
}

func (s racingStore) Users() store.UserStore { return s.users }

func racingSCIM(t *testing.T, race func(st store.Store)) (http.Handler, store.Store, string) {
	t.Helper()
	st, err := store.Open(context.Background(), testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	token := "scim-cas-token"
	srv := scim.NewServer(racingStore{Store: st, users: raceBeforeWrite{UserStore: st.Users(), race: func() { race(st) }}}, config.SCIMConfig{Enabled: true, BearerToken: token}, "http://localhost:8080")
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux.Handle)
	return srv.AuthMiddleware(mux), st, token
}

// A profile-only PATCH writes no access: a deactivation or demotion that landed after SCIM read
// the account stays, and the profile is written.
func TestSCIMProfilePatchNeverRestoresAccess(t *testing.T) {
	for _, tc := range []struct {
		name             string
		race             func(st store.Store)
		wantRole, wantSt string
	}{
		{"webhook deactivation", func(st store.Store) { _ = st.Users().RevokeSSOUser(context.Background(), "usr_ada", true) }, "admin", "inactive"},
		{"login demotion", func(st store.Store) { _ = st.Users().SetSSORole(context.Background(), "usr_ada", "user") }, "user", "active"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, st, token := racingSCIM(t, tc.race)
			ctx := context.Background()
			if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_ada", Username: "ada", Role: "admin", Status: "active", SSOProvider: "scim", SSOSubject: "a1"}); err != nil {
				t.Fatal(err)
			}
			w := scimDo(t, h, token, "PATCH", "/scim/v2/Users/usr_ada", map[string]any{"schemas": []string{scim.SchemaPatchOp},
				"Operations": []map[string]any{{"op": "replace", "path": "displayName", "value": "Ada L"}}})
			if w.Code != http.StatusOK {
				t.Fatalf("patch: %d %s", w.Code, w.Body.String())
			}
			u, _ := st.Users().GetUserByID(ctx, "usr_ada")
			if u.Role != tc.wantRole || u.Status != tc.wantSt || u.DisplayName != "Ada L" {
				t.Fatalf("after the patch: %s/%s %q, want %s/%s \"Ada L\"", u.Role, u.Status, u.DisplayName, tc.wantRole, tc.wantSt)
			}
		})
	}
}

// A grant whose account changed after SCIM read it is refused with a retryable 412 and writes
// nothing: here another request activated the person between this PUT's read and its write.
func TestSCIMGrantOnAChangedAccountAsksForARetry(t *testing.T) {
	h, st, token := racingSCIM(t, func(st store.Store) {
		_ = st.Users().UpdateSCIMUser(context.Background(), &store.User{ID: "usr_bo", Username: "bo", Role: "user", Status: "active"}, "user", "inactive")
	})
	ctx := context.Background()
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_bo", Username: "bo", Role: "user", Status: "inactive", SSOProvider: "scim", SSOSubject: "b1"}); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"schemas": []string{scim.SchemaUser}, "userName": "bo", "displayName": "Bo", "active": true,
		"roles": []any{map[string]any{"value": "kycalendar.admin"}}}
	w := scimDo(t, h, token, "PUT", "/scim/v2/Users/usr_bo", body)
	if w.Code != http.StatusPreconditionFailed {
		t.Fatalf("grant on a changed account: %d %s, want 412", w.Code, w.Body.String())
	}
	if u, _ := st.Users().GetUserByID(ctx, "usr_bo"); u.Role != "user" || u.DisplayName == "Bo" {
		t.Fatalf("a refused grant wrote: %+v", u)
	}
}
