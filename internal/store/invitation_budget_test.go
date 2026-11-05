package store_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

func TestFailedSaveDoesNotSpendInvitationByteBudget(t *testing.T) {
	ctx := context.Background()
	cfg := testdb.Config(t)
	st, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	driver := "sqlite"
	if cfg.Driver == "postgres" {
		driver = "pgx"
	}
	db, err := sql.Open(driver, cfg.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	query := `INSERT INTO invitation_byte_budget (sender_id,window_start,bytes) VALUES ('usr_alice',?,?)`
	if cfg.Driver == "postgres" {
		query = strings.Replace(query, "?", "$1", 1)
		query = strings.Replace(query, "?", "$2", 1)
	}
	if _, err := db.ExecContext(ctx, query, time.Now().Unix(), store.InviteBytesLimit-100); err != nil {
		t.Fatal(err)
	}
	o := outboxObject(t, st, "alice")
	first := row("refused", "usr_alice", 90)
	first.CountInviteBytes = true
	// Admission fits, but object+pending storage does not. The byte reservation must roll back.
	if _, err := st.Calendars().PutObjectPlanned(ctx, o, "", true, store.OwnerLimits{MaxBytes: 50}, plan(first)); !errors.Is(err, store.ErrQuotaExceeded) {
		t.Fatalf("quota refusal: %v", err)
	}
	fitting := row("fits", "usr_alice", 90)
	fitting.CountInviteBytes = true
	if _, err := st.Calendars().PutObjectPlanned(ctx, o, "", true, store.OwnerLimits{MaxBytes: 1000}, plan(fitting)); err != nil {
		t.Fatalf("failed save spent allowance: %v", err)
	}
	// Deduplicated rows spend no additional bytes.
	if _, err := st.Calendars().PutObjectPlanned(ctx, o, o.ETag, false, store.OwnerLimits{MaxBytes: 1000}, plan(fitting)); err != nil {
		t.Fatalf("duplicate billed again: %v", err)
	}
	extra := row("over", "usr_alice", 11)
	extra.CountInviteBytes = true
	_, err = st.Calendars().PutObjectPlanned(ctx, o, o.ETag, false, store.OwnerLimits{MaxBytes: 1000}, plan(extra))
	var limit store.InviteByteLimitError
	if !errors.As(err, &limit) || !strings.Contains(err.Error(), "again at") {
		t.Fatalf("hourly refusal: %v", err)
	}
	// Expiry and the retained window survive reopening the store.
	if _, err := db.ExecContext(ctx, `UPDATE invitation_byte_budget SET window_start = 0`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Calendars().PutObjectPlanned(ctx, o, o.ETag, false, store.OwnerLimits{MaxBytes: 1000}, plan(extra)); err != nil {
		t.Fatalf("expired byte window: %v", err)
	}
}

// A rejection must happen before copying a potentially enormous recipient batch into SQL/WAL.
func TestOutboxChecksQuotaBeforeWritingBodies(t *testing.T) {
	for _, kind := range []string{"owner", "instance", "hourly"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			cfg := testdb.Config(t)
			st, err := store.Open(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			driver := "sqlite"
			if cfg.Driver == "postgres" {
				driver = "pgx"
			}
			db, err := sql.Open(driver, cfg.DSN)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			trigger := `CREATE TRIGGER refuse_body_insert BEFORE INSERT ON scheduling_outbox BEGIN SELECT RAISE(ABORT, 'outbox body reached SQL before admission'); END;`
			if cfg.Driver == "postgres" {
				trigger = `CREATE FUNCTION refuse_body_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'outbox body reached SQL before admission'; END; $$; CREATE TRIGGER refuse_body_insert BEFORE INSERT ON scheduling_outbox FOR EACH ROW EXECUTE FUNCTION refuse_body_insert();`
			}
			if _, err := db.ExecContext(ctx, trigger); err != nil {
				t.Fatal(err)
			}
			o := outboxObject(t, st, "alice")
			m := row("oversize", "usr_alice", 200)
			lim := store.OwnerLimits{}
			switch kind {
			case "owner":
				lim.MaxBytes = 100
			case "instance":
				lim.MaxTotalBytes = 100
			case "hourly":
				m.CountInviteBytes = true
				q := `INSERT INTO invitation_byte_budget(sender_id,window_start,bytes) VALUES ('usr_alice',?,?)`
				if cfg.Driver == "postgres" {
					q = strings.Replace(q, "?", "$1", 1)
					q = strings.Replace(q, "?", "$2", 1)
				}
				if _, err := db.ExecContext(ctx, q, time.Now().Unix(), store.InviteBytesLimit-100); err != nil {
					t.Fatal(err)
				}
			}
			_, err = st.Calendars().PutObjectPlanned(ctx, o, "", true, lim, plan(m))
			if kind == "hourly" {
				var budget store.InviteByteLimitError
				if !errors.As(err, &budget) {
					t.Fatalf("wanted byte admission before body insert, got %v", err)
				}
			} else if !errors.Is(err, store.ErrQuotaExceeded) {
				t.Fatalf("wanted quota admission before body insert, got %v", err)
			}
		})
	}
}
