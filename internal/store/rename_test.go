package store_test

import (
	"context"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/store"
)

// The People screen renames as an admin; the audit row names that admin, not "system".
func TestRenameUserAuditsTheActor(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_l", Username: "lee", Role: "user", Status: "active", SSOProvider: "local"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Users().RenameUser(ctx, "usr_admin", "usr_l", "lee2"); err != nil {
		t.Fatal(err)
	}
	recs, _, err := st.Audit().ListAuditRecords(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) == 0 || recs[0].Action != "user.renamed" || recs[0].UserID != "usr_admin" || recs[0].Details != "from=lee to=lee2" {
		t.Fatalf("audit %+v, want user.renamed by usr_admin", recs)
	}
}
