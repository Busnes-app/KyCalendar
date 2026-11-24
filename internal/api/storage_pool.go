package api

import (
	"context"
	"errors"
	"log"

	"github.com/Busnes-app/ky-primitives/suitedir"
	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// SyncStoragePool is called by the elected org worker after a successful mirror poll.
// Each bounded batch is snapshotted durably before HTTP, so takeover retries exact bytes.
// No network call holds calendar-total. The mirror job and early-refresh job share the
// same persisted snapshots and idempotent result handling.
func (s *Server) SyncStoragePool(ctx context.Context) {
	if err := s.syncStoragePool(ctx); err != nil {
		log.Printf("suite: storage: %v", err)
	}
}

// SyncStorageRefresh runs only inside the existing elected application worker.
func (s *Server) SyncStorageRefresh(ctx context.Context) {
	if err := s.reportStoragePool(ctx, true); err != nil {
		log.Printf("suite: storage refresh: %v", err)
	}
}
func (s *Server) syncStoragePool(ctx context.Context) error { return s.reportStoragePool(ctx, false) }
func (s *Server) reportStoragePool(ctx context.Context, early bool) error {
	ten, err := s.store.Tenancy(ctx)
	if err != nil || !ten.Multi {
		return err
	}
	p := s.signin.Load()
	if p == nil || p.Kind != sso.KindKyIdentity {
		return nil
	}
	dir, err := p.SuiteDirectory()
	if err != nil {
		return err
	}
	var orgs []store.MirroredOrgView
	if early {
		v, readErr := s.store.Settings().GetSettings(ctx, store.ErasedPendingSetting)
		if readErr != nil {
			return readErr
		}
		if v[store.ErasedPendingSetting] != "" {
			return nil
		}
		orgs, err = s.store.StorageRefreshOrgs(ctx)
	} else {
		orgs, err = s.store.ListMirroredOrgs(ctx)
	}
	if err != nil {
		return err
	}
	batch := make([]suitedir.StorageReport, 0, 200)
	pending := map[store.OrgID]store.StorageReport{}
	send := func() error {
		if len(batch) == 0 {
			return nil
		}
		answers, err := dir.ReportStorage(ctx, batch)
		if err != nil {
			return err
		}
		for _, a := range answers {
			org := store.OrgID(a.OrgID)
			r := pending[org]
			switch a.Result {
			case "ok":
				err = s.store.ApplyStorageGrant(ctx, org, a.Seq, a.CeilingBytes, a.TTLSeconds)
			case "stale":
				err = s.store.StaleStorageReport(ctx, org, r.Seq, a.Seq)
				log.Printf("suite: storage: org %s report %d is stale; latest remote sequence %d; operator recovery required", org, r.Seq, a.Seq)
			case "unknown_org":
				err = s.store.ForgetStorageGrant(ctx, org, r.Seq)
			}
			if err != nil {
				return err
			}
		}
		clear(pending)
		batch = batch[:0]
		return nil
	}
	for _, o := range orgs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if o.ID == store.DefaultOrg || o.ID == ten.Staff || o.Status == "deleted" {
			continue
		}
		// The base demand is bounded by KyIdentity; the store increases it for recent growth.
		// First-write/fan-out latency and reservation fairness remain acceptance gates.
		want := int64(64 << 20)
		if o.Status != "active" || !o.Enabled {
			want = 0
		}
		r, err := s.store.BeginStorageReport(ctx, o.ID, want)
		if errors.Is(err, store.ErrStorageSequence) {
			log.Printf("suite: storage: org %s needs operator sequence recovery", o.ID)
			continue
		}
		if err != nil {
			return err
		}
		pending[o.ID] = r
		batch = append(batch, suitedir.StorageReport{OrgID: string(r.Org), Seq: r.Seq, UsedBytes: r.UsedBytes, WantBytes: r.WantBytes})
		if len(batch) == 200 {
			if err := send(); err != nil {
				return err
			}
		}
	}
	return send()
}
