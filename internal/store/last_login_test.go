package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/store"
)

func TestCreateSessionRecordsLastLogin(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	u := &store.User{ID: "usr_l", Username: "l", Role: "user", Status: "active", SSOProvider: "local"}
	seedUsers(t, st, u)
	// An SSO session dated from an auth_time a day old is still a sign-in now.
	day := time.Now().UTC().Add(-24 * time.Hour)
	if err := st.Sessions().CreateSession(ctx, &store.Session{TokenHash: "tok_old", UserID: u.ID, CreatedAt: day, ExpiresAt: time.Now().Add(time.Hour)}, store.Credentials{}); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Users().GetUserByID(ctx, u.ID)
	if got.LastLoginAt == nil || time.Since(*got.LastLoginAt) > time.Minute {
		t.Fatalf("last_login_at = %v, want the session time", got.LastLoginAt)
	}
}
