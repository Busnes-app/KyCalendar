package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Busnes-app/ky-primitives/health"
	"github.com/Busnes-app/ky-primitives/logging"
	"github.com/Busnes-app/ky-primitives/recoveryclient"
	"github.com/Busnes-app/kycalendar/internal/backup"
	"github.com/Busnes-app/kycalendar/internal/store"
)

var backupFailed = health.DeclareReason("last_run_failed")

// healthHandler serves the public ky.health/1 body KyPulse polls. It shows check names and
// fixed reason codes only.
func (s *Server) healthHandler() http.Handler {
	lg, err := logging.New(logging.Config{App: "kycalendar"})
	if err != nil {
		panic(err) // fixed app name
	}
	return health.Handler("kycalendar", lg,
		health.Check{Name: "database", Run: s.store.Ping},
		health.Check{Name: "backup", Run: s.backupHealth},
	)
}

// backupHealth is degraded while the schedule is on and its last run failed. A schedule that
// is off reports ok: nothing was promised.
func (s *Server) backupHealth(ctx context.Context) error {
	if _, on, err := recoveryclient.NextRun(s.config.Backup.DepositInterval, backup.Settings(ctx, s.store.Settings())); err != nil || !on {
		return err
	}
	raw, err := s.store.Settings().GetSetting(ctx, lastRunSetting)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var run struct{ Outcome string }
	if err := json.Unmarshal([]byte(raw), &run); err != nil {
		return err
	}
	if run.Outcome == "failure" {
		return health.Degrade(backupFailed)
	}
	return nil
}
