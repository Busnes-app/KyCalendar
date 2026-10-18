package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Busnes-app/ky-primitives/recoveryclient"
	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/backup"
)

type healthBody struct {
	Schema  string `json:"schema"`
	Service string `json:"service"`
	Status  string `json:"status"`
	Checks  []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
		Reason string `json:"reason"`
	} `json:"checks"`
}

func getHealth(t *testing.T, srv http.Handler, method string) (int, healthBody, string) {
	t.Helper()
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(method, "/healthz", nil))
	var body healthBody
	if method == http.MethodGet {
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("body %q: %v", rr.Body.String(), err)
		}
	}
	return rr.Code, body, rr.Body.String()
}

func wantCheck(t *testing.T, body healthBody, name, status, reason string) {
	t.Helper()
	for _, c := range body.Checks {
		if c.Name == name {
			if c.Status != status || c.Reason != reason {
				t.Errorf("%s = %s/%q, want %s/%q", name, c.Status, c.Reason, status, reason)
			}
			return
		}
	}
	t.Errorf("no %s check in %+v", name, body.Checks)
}

func TestHealthOKAndAnonymous(t *testing.T) {
	srv, _, cfg := setupTestServer(t)
	code, body, raw := getHealth(t, srv, http.MethodGet)
	if code != http.StatusOK || body.Schema != "ky.health/1" || body.Service != "kycalendar" || body.Status != "ok" || len(body.Checks) != 2 {
		t.Fatalf("health = %d %s", code, raw)
	}
	wantCheck(t, body, "database", "ok", "")
	wantCheck(t, body, "backup", "ok", "")
	if strings.Contains(raw, cfg.Database.DataDir) {
		t.Fatalf("health disclosed the data dir: %s", raw)
	}
	if code, _, raw := getHealth(t, srv, http.MethodHead); code != http.StatusOK || raw != "" {
		t.Fatalf("HEAD = %d %q", code, raw)
	}
}

func TestHealthBackupFailureIsDegradedWithoutDetail(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	if err := recoveryclient.SetInterval(backup.Settings(ctx, st.Settings()), 3600); err != nil {
		t.Fatal(err)
	}
	api.RecordLastRun(ctx, st.Settings(), "failure", errors.New("dial tcp 10.9.8.7: refused"))
	code, body, raw := getHealth(t, srv, http.MethodGet)
	if code != http.StatusOK || body.Status != "degraded" {
		t.Fatalf("health = %d %s", code, raw)
	}
	wantCheck(t, body, "backup", "degraded", "last_run_failed")
	if strings.Contains(raw, "10.9.8.7") || strings.Contains(raw, "refused") {
		t.Fatalf("health disclosed the backup error: %s", raw)
	}
}

func TestHealthBackupFailureIgnoredWhenScheduleOff(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	if err := recoveryclient.SetInterval(backup.Settings(ctx, st.Settings()), 0); err != nil {
		t.Fatal(err)
	}
	api.RecordLastRun(ctx, st.Settings(), "failure", errors.New("x"))
	if _, body, raw := getHealth(t, srv, http.MethodGet); body.Status != "ok" {
		t.Fatalf("health = %s", raw)
	}
}

func TestHealthDatabaseDown(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	code, body, raw := getHealth(t, srv, http.MethodGet)
	if code != http.StatusServiceUnavailable || body.Status != "down" {
		t.Fatalf("health = %d %s", code, raw)
	}
	wantCheck(t, body, "database", "down", "")
	if strings.Contains(raw, "closed") {
		t.Fatalf("health disclosed the database error: %s", raw)
	}
}
