package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/store"
)

// Between the removal and the grant transactions another writer changes the account. The removal
// has landed; what SCIM did not change is never rewritten from its snapshot, and a grant whose
// read went stale is refused.
func TestUpdateSCIMUserComparesAccessBeforeGranting(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	t.Run("demotion then a webhook deactivation", func(t *testing.T) {
		ada := &store.User{ID: "usr_ada", Username: "ada", Role: "admin", Status: "active", SSOProvider: "scim", SSOSubject: "a1"}
		seedUsers(t, st, ada)
		restore := store.SetBetweenSCIMWritesForTest(func() {
			if err := st.Users().RevokeSSOUser(ctx, ada.ID, true); err != nil {
				t.Fatal(err)
			}
		})
		defer restore()
		// PATCH remove roles: the snapshot still says active.
		req := *ada
		req.Role = "user"
		if err := st.Users().UpdateSCIMUser(ctx, &req, "admin", "active"); err != nil {
			t.Fatal(err)
		}
		if u, _ := st.Users().GetUserByID(ctx, ada.ID); u.Role != "user" || u.Status != "inactive" {
			t.Fatalf("after the demotion and the deactivation: %s/%s, want user/inactive", u.Role, u.Status)
		}
	})
	t.Run("demotion and activation then a concurrent activation", func(t *testing.T) {
		bo := &store.User{ID: "usr_bo", Username: "bo", Role: "admin", Status: "inactive", SSOProvider: "scim", SSOSubject: "b1"}
		seedUsers(t, st, bo)
		restore := store.SetBetweenSCIMWritesForTest(func() {
			u, _ := st.Users().GetUserByID(ctx, bo.ID)
			u.Status = "active"
			if err := st.Users().UpdateUser(ctx, u); err != nil {
				t.Fatal(err)
			}
		})
		defer restore()
		req := *bo
		req.Role, req.Status, req.DisplayName = "user", "active", "Bo"
		if err := st.Users().UpdateSCIMUser(ctx, &req, "admin", "inactive"); !errors.Is(err, store.ErrAccessChanged) {
			t.Fatalf("activation on a changed account: %v, want ErrAccessChanged", err)
		}
		if u, _ := st.Users().GetUserByID(ctx, bo.ID); u.Role != "user" || u.DisplayName == "Bo" {
			t.Fatalf("want the demotion kept and no profile write: %+v", u)
		}
	})
}
