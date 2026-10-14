package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/store"
)

func TestPruneAuditDeletesOnlyOlderRecords(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	now := time.Now().UTC()
	for action, at := range map[string]time.Time{"old": now.Add(-400 * 24 * time.Hour), "recent": now.Add(-24 * time.Hour)} {
		if err := st.Audit().LogAudit(ctx, &store.AuditRecord{Action: action, CreatedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Audit().PruneAudit(ctx, now.Add(-365*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	recs, total, err := st.Audit().ListAuditRecords(ctx, 0, 10)
	if err != nil || total != 1 || recs[0].Action != "recent" {
		t.Fatalf("after prune: %d records %v", total, err)
	}
}
