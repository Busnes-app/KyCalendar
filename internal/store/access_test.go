package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/store"
)

func seedUsers(t *testing.T, st store.Store, users ...*store.User) {
	t.Helper()
	for _, u := range users {
		if err := st.Users().CreateUser(context.Background(), u); err != nil {
			t.Fatal(err)
		}
	}
}

func seedSession(t *testing.T, st store.Store, u *store.User) {
	t.Helper()
	now := time.Now().UTC()
	if err := st.Sessions().CreateSession(context.Background(), &store.Session{TokenHash: "tok_" + u.ID, UserID: u.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}, u.PasswordHash); err != nil {
		t.Fatal(err)
	}
	if err := st.AppPasswords().Create(context.Background(), &store.AppPassword{ID: "ap_" + u.ID, UserID: u.ID, Label: "phone", Hash: "h"}); err != nil {
		t.Fatal(err)
	}
}

func TestSetRoleAndStatusRevokeAndKeepALocalAdmin(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	root := &store.User{ID: "usr_root", Username: "root", Role: "admin", Status: "active", SSOProvider: "local"}
	ann := &store.User{ID: "usr_ann", Username: "ann", Role: "user", Status: "active", SSOProvider: "local"}
	seedUsers(t, st, root, ann,
		&store.User{ID: "usr_sso", Username: "sso", Role: "admin", Status: "active", SSOProvider: "kysignon", SSOSubject: "s1"},
		&store.User{ID: "usr_off", Username: "off", Role: "admin", Status: "inactive", SSOProvider: "local"},
	)
	seedSession(t, st, ann)

	if err := st.Users().SetRole(ctx, ann.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Sessions().GetSession(ctx, "tok_"+ann.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("promotion kept the session: %v", err)
	}
	if list, _ := st.AppPasswords().ListByUser(ctx, ann.ID); len(list) != 0 {
		t.Errorf("promotion kept %d app passwords", len(list))
	}
	if err := st.Users().SetRole(ctx, root.ID, "user"); err != nil {
		t.Fatalf("demote one of two local admins: %v", err)
	}
	// ann is now the only active local admin: an SSO admin and an inactive local admin do not count.
	if err := st.Users().SetStatus(ctx, ann.ID, "inactive"); !errors.Is(err, store.ErrLastAdmin) {
		t.Errorf("disable the last local admin: %v, want ErrLastAdmin", err)
	}
	if err := st.Users().SetRole(ctx, ann.ID, "user"); !errors.Is(err, store.ErrLastAdmin) {
		t.Errorf("demote the last local admin: %v, want ErrLastAdmin", err)
	}
	if u, _ := st.Users().GetUserByID(ctx, ann.ID); u.Role != "admin" || u.Status != "active" {
		t.Errorf("a refused change was stored: %+v", u)
	}
	if err := st.Users().SetStatus(ctx, "usr_sso", "inactive"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("SSO account: %v, want ErrNotFound", err)
	}
	if err := st.Users().SetStatus(ctx, root.ID, "inactive"); err != nil {
		t.Fatalf("disable an everyday account: %v", err)
	}
	if err := st.Users().SetStatus(ctx, root.ID, "active"); err != nil {
		t.Fatalf("enable it again: %v", err)
	}
}

// Two admins demoting each other at once: exactly one may succeed.
func TestConcurrentDemotionKeepsOneLocalAdmin(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	seedUsers(t, st,
		&store.User{ID: "usr_a", Username: "a", Role: "admin", Status: "active", SSOProvider: "local"},
		&store.User{ID: "usr_b", Username: "b", Role: "admin", Status: "active", SSOProvider: "local"},
	)
	start := make(chan struct{})
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i, id := range []string{"usr_a", "usr_b"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = st.Users().SetRole(ctx, id, "user")
		}()
	}
	close(start)
	wg.Wait()
	if (errs[0] == nil) == (errs[1] == nil) {
		t.Fatalf("want exactly one demotion, got %v and %v", errs[0], errs[1])
	}
	for _, err := range errs {
		if err != nil && !errors.Is(err, store.ErrLastAdmin) {
			t.Fatalf("refusal: %v, want ErrLastAdmin", err)
		}
	}
}

func TestUpdateProfileIsLocalOnly(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	seedUsers(t, st,
		&store.User{ID: "usr_l", Username: "l", Role: "user", Status: "active", SSOProvider: "local"},
		&store.User{ID: "usr_s", Username: "s", Role: "user", Status: "active", SSOProvider: "scim", SSOSubject: "x"},
	)
	if err := st.Users().UpdateProfile(ctx, "usr_l", "Ella L", "l@example.com"); err != nil {
		t.Fatal(err)
	}
	if u, _ := st.Users().GetUserByID(ctx, "usr_l"); u.DisplayName != "Ella L" || u.Email != "l@example.com" {
		t.Fatalf("profile not stored: %+v", u)
	}
	if err := st.Users().UpdateProfile(ctx, "usr_s", "Taken", ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("SCIM account: %v, want ErrNotFound", err)
	}
}
