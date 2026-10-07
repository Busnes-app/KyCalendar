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
	seedSession(t, st, u)
	got, _ := st.Users().GetUserByID(ctx, u.ID)
	if got.LastLoginAt == nil || time.Since(*got.LastLoginAt) > time.Minute {
		t.Fatalf("last_login_at = %v, want the session time", got.LastLoginAt)
	}
}
