package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
)

const adminPassword = "AdminPassword-123!"

// multiConfig is a multi-tenant configuration on db: staff org org-staff, cells us-east-2 and
// us-east-3, and an object store (never reached here).
func multiConfig(t *testing.T, db config.DatabaseConfig) *config.Config {
	t.Helper()
	cfg := serverConfig(t, db)
	cfg.Tenancy = config.TenancyConfig{Multi: true, StaffOrg: "org-staff", Cells: []string{"us-east-2", "us-east-3"}}
	cfg.ObjectStorage = config.ObjectStorageConfig{Endpoint: "https://s3.example", Bucket: "cal", AccessKey: "ak", SecretKey: "sk"}
	return cfg
}

// A fresh multi-tenant install (tenancy spec step 6): the first start records the mode, the
// staff org and the storage cell, writes the staff org active and enabled, and makes the
// bootstrap administrator in it from KY_ADMIN_PASSWORD; that administrator signs in with a
// password, and nothing is left in KY_DATA_DIR.
func TestFreshMultiTenantInstallSignsTheBootstrapAdminIn(t *testing.T) {
	requirePostgres(t)
	t.Setenv("KY_ADMIN_PASSWORD", adminPassword)
	cfg := multiConfig(t, testdb.Config(t))
	st := openStore(t, cfg)
	ctx := context.Background()
	if err := prepareStart(ctx, cfg, st); err != nil {
		t.Fatal(err)
	}
	ten, err := st.Tenancy(ctx)
	if err != nil || !ten.Multi || ten.Staff != "org-staff" || ten.Cell != "us-east-2" {
		t.Fatalf("recorded %+v %v", ten, err)
	}
	admin, err := st.Users().GetUserByUsername(ctx, "admin")
	if err != nil || store.OrgOf(admin) != "org-staff" || admin.Role != "admin" || !admin.OrgActive {
		t.Fatalf("bootstrap admin %+v %v", admin, err)
	}
	if files, _ := os.ReadDir(cfg.Database.DataDir); len(files) != 0 {
		t.Fatalf("a multi-tenant start left %d files in KY_DATA_DIR: %v", len(files), files[0].Name())
	}
	// The start serves only KY_CELLS (Cells, C5).
	if err := st.ApplyOrgs(ctx, "org-staff", []store.MirroredOrg{
		{ID: "org-here", Status: "active", Revision: 1, Enabled: true, Quotas: `{}`, HomeRegion: "us-east-3"},
		{ID: "org-there", Status: "active", Revision: 2, Enabled: true, Quotas: `{}`, HomeRegion: "us-east-1"},
	}, 2); err != nil {
		t.Fatal(err)
	}
	for org, want := range map[store.OrgID]bool{"org-here": true, "org-there": false} {
		if ok, err := st.OrgServes(ctx, org); err != nil || ok != want {
			t.Fatalf("%s serves %t (%v), want %t", org, ok, err, want)
		}
	}
	srv := api.NewServer(cfg, st)
	t.Cleanup(srv.StopDetached)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"admin","password":"`+adminPassword+`"}`))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "198.51.100.7:4000"
	srv.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"authenticated":true`) {
		t.Fatalf("bootstrap admin sign-in: %d %s", w.Code, w.Body.String())
	}
	// A later start with the same settings passes, the cells reordered included (adding one
	// changes the shared replica settings: accept-replica-config).
	cfg.Tenancy.Cells = []string{"us-east-3", "us-east-2"}
	if err := prepareStart(ctx, cfg, st); err != nil {
		t.Fatalf("a second start: %v", err)
	}
}

// Each D1 or D15 requirement checked against the database refuses the start: no
// KY_ADMIN_PASSWORD on an empty database (and no password file written), another staff org,
// cells without the recorded storage cell, single-tenant on a multi-tenant database and
// multi-tenant on a single-tenant install with accounts.
func TestMultiTenantStartRefusals(t *testing.T) {
	requirePostgres(t)
	ctx := context.Background()
	t.Run("no admin password", func(t *testing.T) {
		t.Setenv("KY_ADMIN_PASSWORD", "")
		cfg := multiConfig(t, testdb.Config(t))
		if err := prepareStart(ctx, cfg, openStore(t, cfg)); !errors.Is(err, errNoAdminPassword) {
			t.Fatalf("got %v", err)
		}
		if files, _ := os.ReadDir(cfg.Database.DataDir); len(files) != 0 {
			t.Fatalf("a refused start left %d files in KY_DATA_DIR", len(files))
		}
	})
	t.Run("changed settings", func(t *testing.T) {
		t.Setenv("KY_ADMIN_PASSWORD", adminPassword)
		cfg := multiConfig(t, testdb.Config(t))
		st := openStore(t, cfg)
		if err := prepareStart(ctx, cfg, st); err != nil {
			t.Fatal(err)
		}
		for name, change := range map[string]func(*config.Config){
			"staff org":    func(c *config.Config) { c.Tenancy.StaffOrg = "org-other" },
			"storage cell": func(c *config.Config) { c.Tenancy.Cells = []string{"us-east-3"} },
			"single":       func(c *config.Config) { c.Tenancy = config.TenancyConfig{} },
		} {
			c := *cfg
			change(&c)
			if err := prepareStart(ctx, &c, st); !errors.Is(err, store.ErrTenancyChanged) {
				t.Errorf("%s: %v", name, err)
			}
		}
	})
	t.Run("a single-tenant install", func(t *testing.T) {
		t.Setenv("KY_ADMIN_PASSWORD", adminPassword)
		single := serverConfig(t, testdb.Config(t))
		st := openStore(t, single)
		if err := prepareStart(ctx, single, st); err != nil {
			t.Fatal(err)
		}
		multi := multiConfig(t, single.Database)
		if err := prepareStart(ctx, multi, st); !errors.Is(err, store.ErrTenancyChanged) {
			t.Fatalf("got %v", err)
		}
		if ten, _ := st.Tenancy(ctx); ten.Multi {
			t.Fatal("a refused start changed the recorded mode")
		}
	})
}
