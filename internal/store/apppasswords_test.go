package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

func TestAppPasswords(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, u := range []string{"usr_a", "usr_b"} {
		if err := st.Users().CreateUser(ctx, &store.User{ID: u, Username: u, Role: "user", Status: "active", SSOProvider: "local"}); err != nil {
			t.Fatal(err)
		}
	}
	ap := st.AppPasswords()
	if err := ap.Create(ctx, &store.AppPassword{ID: "p1", UserID: "usr_a", Label: "phone", Hash: "h1"}); err != nil {
		t.Fatal(err)
	}
	ap.Create(ctx, &store.AppPassword{ID: "p2", UserID: "usr_a", Label: "laptop", Hash: "h2"})
	ap.Create(ctx, &store.AppPassword{ID: "p3", UserID: "usr_b", Label: "phone", Hash: "h3"})

	if err := ap.Delete(ctx, "usr_b", "p1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleting another user's password: %v", err)
	}
	if err := ap.TouchLastUsed(ctx, "p1", time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err := ap.Get(ctx, "p1")
	if err != nil || got.LastUsedAt == nil || got.UserID != "usr_a" {
		t.Fatalf("get %+v %v", got, err)
	}
	if err := ap.DeleteByUser(ctx, "usr_a"); err != nil {
		t.Fatal(err)
	}
	if list, _ := ap.ListByUser(ctx, "usr_a"); len(list) != 0 {
		t.Fatalf("left %d", len(list))
	}
	if list, _ := ap.ListByUser(ctx, "usr_b"); len(list) != 1 {
		t.Fatalf("usr_b lost passwords: %d", len(list))
	}
}
