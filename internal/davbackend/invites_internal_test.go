package davbackend

import (
	"context"
	"errors"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

// With a shared window, two replicas' schedulers count one sender's new invitees against one
// InviteLimit.
func TestReplicasShareOneInviteLimit(t *testing.T) {
	ctx := context.Background()
	cfg := testdb.Config(t)
	var replicas [2]*Scheduler
	for i := range replicas {
		st, err := store.Open(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		replicas[i] = &Scheduler{Store: st, Shared: st.Coordination()}
	}
	if err := replicas[0].admitInvites(ctx, "usr_a", 600); err != nil {
		t.Fatal(err)
	}
	if err := replicas[1].admitInvites(ctx, "usr_a", 600); !errors.As(err, new(InviteLimitError)) {
		t.Fatalf("1200 invitees across two replicas: %v", err)
	}
	if err := replicas[1].admitInvites(ctx, "usr_a", 400); err != nil {
		t.Fatalf("1000 invitees across two replicas: %v", err)
	}
	if err := replicas[0].admitInvites(ctx, "usr_b", InviteLimit); err != nil {
		t.Fatalf("another sender: %v", err)
	}
}
