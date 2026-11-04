package api

import (
	"fmt"
	"testing"
	"time"
)

// The per-account login windows are keyed by account and client address, so they cannot be
// kept forever: past attemptsCap a new key drops the expired windows, never a live one.
func TestAccountLimiterDropsOnlyExpiredWindows(t *testing.T) {
	s := &Server{accounts: map[string]attemptWindow{}}
	gone := time.Now().Add(-time.Minute)
	for i := range attemptsCap - 1 {
		s.accounts[fmt.Sprintf("login:usr_admin@203.0.113.%d/%d", i%250, i)] = attemptWindow{count: 10, reset: gone}
	}
	for range 11 {
		s.allowAccountAttempt("login:usr_admin@198.51.100.7", 10, 15*time.Minute)
	}
	if s.allowAccountAttempt("login:usr_new@192.0.2.1", 10, 15*time.Minute); len(s.accounts) != 2 {
		t.Fatalf("%d windows kept, want the 2 live ones", len(s.accounts))
	}
	if s.allowAccountAttempt("login:usr_admin@198.51.100.7", 10, 15*time.Minute) {
		t.Fatal("pruning reset a live lockout")
	}
}
