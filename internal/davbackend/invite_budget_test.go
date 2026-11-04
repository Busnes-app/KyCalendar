package davbackend

import (
	"context"
	"errors"
	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
	"strings"
	"testing"
	"time"
)

func TestInvitationByteBudgetCountsUpdatesAndExpires(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	s := &Scheduler{Now: func() time.Time { return now }}
	if err := s.admitInvites("alice", 1, InviteBytesLimit); err != nil {
		t.Fatal(err)
	}
	// No new people: a repeated update still consumes delivery bytes.
	err := s.admitInvites("alice", 0, 1)
	var limit InviteLimitError
	if !errors.As(err, &limit) || !limit.Bytes || !limit.Until.Equal(now.Add(time.Hour)) || !strings.Contains(err.Error(), "again at 13:00 UTC") {
		t.Fatalf("byte refusal: %v", err)
	}
	if err := s.admitInvites("bob", 1, 100); err != nil {
		t.Fatal("another sender's budget was used", err)
	}
	if err := s.admitInvites("alice", 0, 0); err != nil {
		t.Fatal("ordinary write blocked", err)
	}
	now = now.Add(time.Hour)
	if err := s.admitInvites("alice", 1, 100); err != nil {
		t.Fatal("budget never expired", err)
	}
}

func TestInvitationByteBudgetIsAppliedByPlannerToUpdates(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var alice *store.User
	for _, name := range []string{"alice", "bob"} {
		u := &store.User{ID: name, Username: name, Email: name + "@example.com", Role: "user", Status: "active", SSOProvider: "local"}
		if err := st.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
		if name == "alice" {
			alice = u
		}
	}
	now := time.Now()
	s := &Scheduler{Store: st, Now: func() time.Time { return now }, invites: map[string]*inviteWindow{"alice": {start: now, bytes: InviteBytesLimit}}}
	body := "BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:x\nBEGIN:VEVENT\nUID:m\nDTSTAMP:20261101T000000Z\nDTSTART:20261110T090000Z\nDTEND:20261110T100000Z\nORGANIZER:mailto:alice@example.com\nATTENDEE:mailto:bob@example.com\nSUMMARY:Old\nSEQUENCE:1\nEND:VEVENT\nEND:VCALENDAR\n"
	old, err := encodeCal(decodeICS(t, body))
	if err != nil {
		t.Fatal(err)
	}
	newCal := decodeICS(t, strings.Replace(body, "SUMMARY:Old", "SUMMARY:Updated", 1))
	_, _, err = s.Plan(ctx, alice, newCal)(old)
	var limit InviteLimitError
	if !errors.As(err, &limit) || !limit.Bytes {
		t.Fatalf("update bypassed byte budget: %v", err)
	}
	// A remote-only REQUEST has no internal delivered copy to spend the byte budget.
	remoteBody := strings.ReplaceAll(body, "bob@example.com", "remote@external.example")
	old, _ = encodeCal(decodeICS(t, remoteBody))
	newCal = decodeICS(t, strings.Replace(remoteBody, "SUMMARY:Old", "SUMMARY:Updated", 1))
	if _, _, err := s.Plan(ctx, alice, newCal)(old); err != nil {
		t.Fatalf("external-only update spent internal budget: %v", err)
	}
}
