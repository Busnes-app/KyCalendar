package store

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

func poolWorld(t *testing.T, s *SQLStore) *quotaWorld {
	t.Helper()
	w := newQuotaWorld(t, s, `{}`, "alice", "bob", "default-carol")
	if err := s.Settings().SetSetting(context.Background(), TenancySetting, "multi"); err != nil {
		t.Fatal(err)
	}
	return w
}
func poolGrant(t *testing.T, s *SQLStore, ceiling int64) StorageReport {
	t.Helper()
	ctx := context.Background()
	r, err := s.BeginStorageReport(ctx, "org-a", ceiling)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyStorageGrant(ctx, "org-a", r.Seq, r.UsedBytes+ceiling, StorageGrantTTL); err != nil {
		t.Fatal(err)
	}
	return r
}
func poolSQL(t *testing.T, s *SQLStore, q string, args ...any) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(), s.rebind(q), args...); err != nil {
		t.Fatal(err)
	}
}

func TestStoragePoolAdmission(t *testing.T) {
	eachDialect(t, func(t *testing.T, s *SQLStore) {
		ctx := context.Background()
		w := poolWorld(t, s)
		// Before the first report, only the existing per-app limits apply for fifteen minutes.
		if err := w.put(s, "alice", "one", 100); err != nil {
			t.Fatal(err)
		}
		poolSQL(t, s, `UPDATE orgs SET pool_since=`+s.dbNow()+`-901 WHERE id='org-a'`)
		if err := w.put(s, "bob", "two", 100); !errors.Is(err, ErrQuotaExceeded) {
			t.Fatalf("expired initial grace: %v", err)
		}
		if err := w.put(s, "alice", "one", 50); err != nil {
			t.Fatalf("shrink without grant: %v", err)
		}
		r := poolGrant(t, s, 150) // 50 used plus 150 new bytes
		if err := w.put(s, "bob", "two", 150); err != nil {
			t.Fatal(err)
		}
		if err := w.put(s, "bob", "three", 50); !errors.Is(err, ErrQuotaExceeded) {
			t.Fatalf("over ceiling: %v", err)
		}
		// Even a fresh per-app poll cannot extend an expired pool grant.
		poolSQL(t, s, `UPDATE orgs SET pool_expires_at=`+s.dbNow()+`-1 WHERE id='org-a'`)
		if err := s.ApplyStorageGrant(ctx, "org-a", r.Seq, 200, StorageGrantTTL); err != nil {
			t.Fatal(err)
		}
		if err := w.put(s, "bob", "three", 50); !errors.Is(err, ErrQuotaExceeded) {
			t.Fatalf("replay renewed lease: %v", err)
		}
		if err := w.put(s, "bob", "two", 100); err != nil {
			t.Fatalf("expired grant blocked shrink: %v", err)
		}
		if err := s.Calendars().DeleteObject(ctx, w.cal["alice"], "one.ics", ""); err != nil {
			t.Fatal(err)
		}
		if err := w.put(s, "default-carol", "unpooled", 5000); err != nil {
			t.Fatalf("default org pooled: %v", err)
		}
		// Single mode ignores even a zero/expired customer grant.
		if err := s.Settings().SetSetting(ctx, TenancySetting, "single"); err != nil {
			t.Fatal(err)
		}
		if err := w.put(s, "alice", "single", 5000); err != nil {
			t.Fatalf("single mode changed: %v", err)
		}
	})
}

func TestStorageReportSnapshotIsFrozen(t *testing.T) {
	eachDialect(t, func(t *testing.T, s *SQLStore) {
		ctx := context.Background()
		w := poolWorld(t, s)
		poolGrant(t, s, 1000)
		if err := w.put(s, "alice", "one", 100); err != nil {
			t.Fatal(err)
		}
		r, err := s.BeginStorageReport(ctx, "org-a", 2000)
		if err != nil {
			t.Fatal(err)
		}
		if r.Seq != 2 || r.UsedBytes != 100 {
			t.Fatalf("report: %+v", r)
		}
		// The old grant cannot fund unreported growth after its snapshot was sent.
		if err := w.put(s, "bob", "two", 100); !errors.Is(err, ErrQuotaExceeded) {
			t.Fatalf("spent old reservation while report pending: %v", err)
		}
		retry, err := s.BeginStorageReport(ctx, "org-a", 9999)
		if err != nil || !reflect.DeepEqual(r, retry) {
			t.Fatalf("retry altered report: %+v %v", retry, err)
		}
		if err := w.put(s, "alice", "one", 50); err != nil {
			t.Fatal(err)
		}
		// A slow answer's TTL starts at the request, not its receipt. Move the request clock
		// back without sleeping: the newly accepted grant must already be expired.
		poolSQL(t, s, `UPDATE orgs SET pool_sent_at=`+s.dbNow()+`-901 WHERE id='org-a'`)
		if err := s.ApplyStorageGrant(ctx, "org-a", r.Seq, 2100, 900); err != nil {
			t.Fatal(err)
		}
		if err := w.put(s, "bob", "two", 100); !errors.Is(err, ErrQuotaExceeded) {
			t.Fatalf("late response extended TTL: %v", err)
		}
		next := poolGrant(t, s, 500)
		if next.Seq != 3 || next.UsedBytes != 50 {
			t.Fatalf("next: %+v", next)
		}
		if err := w.put(s, "bob", "two", 500); err != nil {
			t.Fatal(err)
		}
		// An old reply cannot shrink the newer grant or reopen an earlier request.
		if err := s.ApplyStorageGrant(ctx, "org-a", r.Seq, 100, 900); err != nil {
			t.Fatal(err)
		}
	})
}

func TestStorageStaleSequenceRequiresOperator(t *testing.T) {
	eachDialect(t, func(t *testing.T, s *SQLStore) {
		ctx := context.Background()
		w := poolWorld(t, s)
		r, err := s.BeginStorageReport(ctx, "org-a", 1000)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.StaleStorageReport(ctx, "org-a", r.Seq, 40); err != nil {
			t.Fatal(err)
		}
		if err := s.StaleStorageReport(ctx, "org-a", r.Seq, 39); err != nil {
			t.Fatal(err)
		}
		if _, err := s.BeginStorageReport(ctx, "org-a", 1000); !errors.Is(err, ErrStorageSequence) {
			t.Fatalf("automatically advanced stale sequence: %v", err)
		}
		if err := s.ApplyStorageGrant(ctx, "org-a", r.Seq, 1000, 900); err != nil {
			t.Fatal(err)
		}
		if err := w.put(s, "alice", "one", 100); !errors.Is(err, ErrQuotaExceeded) {
			t.Fatalf("stale answer granted growth: %v", err)
		}
		if err := s.AdvanceStorageSequence(ctx, "org-a", 39); !errors.Is(err, ErrStorageSequence) {
			t.Fatalf("advanced below remote sequence: %v", err)
		}
		if err := s.AdvanceStorageSequence(ctx, "org-a", 40); err != nil {
			t.Fatal(err)
		}
		next, err := s.BeginStorageReport(ctx, "org-a", 1000)
		if err != nil || next.Seq != 41 {
			t.Fatalf("operator recovery: %+v %v", next, err)
		}
		records, _, err := s.Audit().ListAuditRecords(ctx, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, a := range records {
			if a.Action == "platform.storage_sequence_advanced" && a.Resource == "org:org-a" {
				found = true
			}
		}
		if !found {
			t.Fatal("sequence recovery was not audited")
		}
	})
}

func TestStorageRestoreRevokesLeaseAndPendingReply(t *testing.T) {
	eachDialect(t, func(t *testing.T, s *SQLStore) {
		ctx := context.Background()
		w := poolWorld(t, s)
		poolGrant(t, s, 1000)
		if err := w.put(s, "alice", "one", 100); err != nil {
			t.Fatal(err)
		}
		pending, err := s.BeginStorageReport(ctx, "org-a", 2000)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.ResetAfterRestore(ctx); err != nil {
			t.Fatal(err)
		}
		if err := s.ApplyStorageGrant(ctx, "org-a", pending.Seq, 2100, 900); err != nil {
			t.Fatal(err)
		}
		if err := w.put(s, "bob", "two", 100); !errors.Is(err, ErrQuotaExceeded) {
			t.Fatalf("restored grant allowed growth: %v", err)
		}
		next, err := s.BeginStorageReport(ctx, "org-a", 1000)
		if err != nil || next.Seq != pending.Seq+1 || next.UsedBytes != 100 {
			t.Fatalf("restored pending report reused: %+v %v", next, err)
		}
		if err := w.put(s, "alice", "one", 50); err != nil {
			t.Fatalf("restore blocked shrinking: %v", err)
		}
	})
}

func TestStorageReportCountsQueueAndReservations(t *testing.T) {
	eachDialect(t, func(t *testing.T, s *SQLStore) {
		ctx := context.Background()
		w := poolWorld(t, s)
		msg := &OutboxMessage{ID: "pool-msg", DedupeKey: "pool-msg", SenderID: "usr_alice", Method: "REQUEST", Recipient: "mailto:x@example.net", Body: make([]byte, 200)}
		if _, err := s.Calendars().PutObjectPlanned(ctx, object(w.cal["alice"], "one", 100), "", false, OwnerLimits{}, func([]byte) ([]byte, []*OutboxMessage, error) { return nil, []*OutboxMessage{msg}, nil }); err != nil {
			t.Fatal(err)
		}
		poolSQL(t, s, `INSERT INTO cancellation_reservations (calendar_id,name,sender_id,bytes) VALUES (?,'one.ics','usr_alice',300)`, w.cal["alice"])
		if err := w.put(s, "default-carol", "other-org", 5000); err != nil {
			t.Fatal(err)
		}
		r, err := s.BeginStorageReport(ctx, "org-a", 1000)
		if err != nil || r.UsedBytes != 600 {
			t.Fatalf("usage report: %+v %v", r, err)
		}
		// Noncustomer and single-mode requests never mint a grant/report sequence.
		for _, org := range []OrgID{DefaultOrg, "missing"} {
			if _, err := s.BeginStorageReport(ctx, org, 1); !errors.Is(err, ErrNotFound) {
				t.Fatalf("%s: %v", org, err)
			}
		}
		if err := s.Settings().SetSetting(ctx, StaffOrgSetting, "org-a"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.BeginStorageReport(ctx, "org-a", 1); !errors.Is(err, ErrNotFound) {
			t.Fatalf("staff report: %v", err)
		}
	})
}

func TestStoragePoolTwoReplicasShareLastBytes(t *testing.T) {
	ctx := context.Background()
	dsn := pgTarget(t)
	replicas := make([]*SQLStore, 2)
	for i := range replicas {
		st, err := openPostgres(ctx, dsn, 4, 4, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		replicas[i] = st.(*SQLStore)
	}
	w := poolWorld(t, replicas[0])
	poolGrant(t, replicas[0], 100)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, name := range []string{"alice", "bob"} {
		wg.Add(1)
		go func(i int, name string) { defer wg.Done(); errs[i] = w.put(replicas[i], name, "last", 100) }(i, name)
	}
	wg.Wait()
	passed := 0
	for _, err := range errs {
		if err == nil {
			passed++
		} else if !errors.Is(err, ErrQuotaExceeded) {
			t.Fatal(err)
		}
	}
	if passed != 1 {
		t.Fatalf("two independent connections spent one grant: %v", errs)
	}
	// Another replica must retry the one shared outstanding sequence, not make its own.
	r, err := replicas[0].BeginStorageReport(ctx, "org-a", 100)
	if err != nil {
		t.Fatal(err)
	}
	other, err := replicas[1].BeginStorageReport(ctx, "org-a", 500)
	if err != nil || other != r {
		t.Fatalf("replica snapshot: %+v %+v %v", r, other, err)
	}
}

func TestStorageHalfGrantAndShrinkRequestEarlyRefresh(t *testing.T) {
	eachDialect(t, func(t *testing.T, s *SQLStore) {
		ctx := context.Background()
		w := poolWorld(t, s)
		poolGrant(t, s, 1000)
		if err := w.put(s, "alice", "one", 400); err != nil {
			t.Fatal(err)
		}
		due, err := s.StorageRefreshOrgs(ctx)
		if err != nil || len(due) != 0 {
			t.Fatalf("early below half: %+v %v", due, err)
		}
		if err := w.put(s, "alice", "one", 500); err != nil {
			t.Fatal(err)
		}
		due, err = s.StorageRefreshOrgs(ctx)
		if err != nil || len(due) != 1 || due[0].ID != "org-a" {
			t.Fatalf("half grant not signalled: %+v %v", due, err)
		}
		r, err := s.BeginStorageReport(ctx, "org-a", 100)
		if err != nil {
			t.Fatal(err)
		}
		// Recent 500-byte growth asks for twice that rate, rather than the tiny base demand.
		if r.WantBytes != 1000 {
			t.Fatalf("request not adaptive: %+v", r)
		}
		if err := s.ApplyStorageGrant(ctx, "org-a", r.Seq, r.UsedBytes+1000, 900); err != nil {
			t.Fatal(err)
		}
		due, err = s.StorageRefreshOrgs(ctx)
		if err != nil || len(due) != 0 {
			t.Fatalf("applied request still due: %+v %v", due, err)
		}
		if err := w.put(s, "alice", "one", 300); err != nil {
			t.Fatal(err)
		}
		due, err = s.StorageRefreshOrgs(ctx)
		if err != nil || len(due) != 1 {
			t.Fatalf("shrunk usage not due: %+v %v", due, err)
		}
	})
}

// Early renewal must not relax #208's first-report admission rule on an outage.
func TestStorageFirstReportStillFencesGrowth(t *testing.T) {
	eachDialect(t, func(t *testing.T, s *SQLStore) {
		ctx := context.Background()
		w := poolWorld(t, s)
		if _, err := s.BeginStorageReport(ctx, "org-a", 1000); err != nil {
			t.Fatal(err)
		}
		if err := w.put(s, "alice", "first", 100); !errors.Is(err, ErrQuotaExceeded) {
			t.Fatalf("outstanding first report allowed growth: %v", err)
		}
	})
}

func TestStorageRefreshKeepsChangesDuringReport(t *testing.T) {
	eachDialect(t, func(t *testing.T, s *SQLStore) {
		ctx := context.Background()
		w := poolWorld(t, s)
		poolGrant(t, s, 1000)
		if err := w.put(s, "alice", "one", 400); err != nil {
			t.Fatal(err)
		}
		if err := w.put(s, "alice", "one", 1100); !errors.Is(err, ErrQuotaExceeded) {
			t.Fatalf("over-grant write: %v", err)
		}
		if due, err := s.StorageRefreshOrgs(ctx); err != nil || len(due) != 0 {
			t.Fatalf("refused growth committed a signal: %+v %v", due, err)
		}
		r, err := s.BeginStorageReport(ctx, "org-a", 1000)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.put(s, "alice", "one", 300); err != nil {
			t.Fatal(err)
		}
		if err := s.ApplyStorageGrant(ctx, "org-a", r.Seq, r.UsedBytes+1000, 900); err != nil {
			t.Fatal(err)
		}
		if due, err := s.StorageRefreshOrgs(ctx); err != nil || len(due) != 1 {
			t.Fatalf("response lost a concurrent shrink: %+v %v", due, err)
		}
		next, err := s.BeginStorageReport(ctx, "org-a", 1000)
		if err != nil || next.UsedBytes != 300 {
			t.Fatalf("next report missed committed usage: %+v %v", next, err)
		}
	})
}

func TestStorageErasureRequestsEarlyRefresh(t *testing.T) {
	eachDialect(t, func(t *testing.T, s *SQLStore) {
		ctx := context.Background()
		w := poolWorld(t, s)
		poolGrant(t, s, 1000)
		if err := w.put(s, "alice", "one", 400); err != nil {
			t.Fatal(err)
		}
		if err := s.Users().DeleteUser(ctx, "usr_alice"); err != nil {
			t.Fatal(err)
		}
		if due, err := s.StorageRefreshOrgs(ctx); err != nil || len(due) != 1 || due[0].ID != "org-a" {
			t.Fatalf("erasure not signalled: %+v %v", due, err)
		}
		r, err := s.BeginStorageReport(ctx, "org-a", 1000)
		if err != nil || r.UsedBytes != 0 {
			t.Fatalf("report retained erased bytes: %+v %v", r, err)
		}
	})
}
