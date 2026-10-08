package api

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/access"
	"github.com/Busnes-app/kycalendar/internal/auth"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// heldIssuance is an app-password issuance caught mid-transaction on Postgres: it holds the user
// row (as withPassword does) and has inserted its credential, not yet committed.
func heldIssuance(t *testing.T, dsn, userID string) (*sql.DB, *sql.Tx) {
	t.Helper()
	raw, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	tx, err := raw.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	for _, q := range []string{
		`UPDATE users SET id = id WHERE id = $1`,
		`INSERT INTO app_passwords (id, user_id, label, hash, created_at) VALUES ('held', $1, 'phone', 'h', now())`,
	} {
		if _, err := tx.Exec(q, userID); err != nil {
			t.Fatal(err)
		}
	}
	return raw, tx
}

// waitForRowLock returns once some backend waits on a row lock.
func waitForRowLock(t *testing.T, raw *sql.DB) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; {
		var n int
		if err := raw.QueryRow(`SELECT COUNT(1) FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND wait_event IN ('transactionid', 'tuple')`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the purge never waited on the user row")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A login's role change locks the row before revoking: an issuance that checked its session
// before the purge and commits during it loses its credential to the purge.
func TestSSORoleChangeWaitsForAnIssuanceInFlight(t *testing.T) {
	s, _ := davInternalServer(t)
	if s.config.Database.Driver != "postgres" {
		t.Skip("postgres only: SQLite's single connection serialises the two")
	}
	ctx := context.Background()
	createUsers(t, s, &store.User{ID: "usr_eve", Username: "eve", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "sub-e", SSOIssuer: kyBinding})
	raw, held := heldIssuance(t, s.config.Database.DSN, "usr_eve")
	done := make(chan error, 1)
	go func() {
		_, err := s.upsertSSOUser(ctx, &sso.IdentityClaims{Subject: "sub-e", Provider: "kysignon", Roles: []string{access.AdminAppRole}}, kyBinding)
		done <- err
	}()
	waitForRowLock(t, raw)
	if err := held.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if list, _ := s.store.AppPasswords().ListByUser(ctx, "usr_eve"); len(list) != 0 {
		t.Fatalf("an everyday credential outlived the promotion: %+v", list[0])
	}
}

// pausedIssuance runs pause just before the app-password store write.
type pausedIssuance struct {
	store.AppPasswordStore
	pause func()
}

func (p pausedIssuance) Create(ctx context.Context, by store.Grantor, ap *store.AppPassword, max int) error {
	p.pause()
	return p.AppPasswordStore.Create(ctx, by, ap, max)
}

type pausedStore struct {
	store.Store
	ap store.AppPasswordStore
}

func (s pausedStore) AppPasswords() store.AppPasswordStore { return s.ap }

// The same race through HTTP on either backend: a create paused before its insert while a login
// changes the role is refused and stores nothing.
func TestAppPasswordCreatedAcrossAnSSORoleChangeIsRefused(t *testing.T) {
	s, _ := davInternalServer(t)
	ctx := context.Background()
	eve := &store.User{ID: "usr_eve", Username: "eve", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "sub-e", SSOIssuer: kyBinding}
	createUsers(t, s, eve)
	now := time.Now().UTC()
	if err := s.store.Sessions().CreateSession(ctx, &store.Session{TokenHash: crypto.SHA256Hex([]byte("tok-eve")), UserID: eve.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}, ""); err != nil {
		t.Fatal(err)
	}
	real := s.store
	s.store = pausedStore{Store: real, ap: pausedIssuance{AppPasswordStore: real.AppPasswords(), pause: func() {
		if _, err := (&Server{store: real, config: s.config}).upsertSSOUser(ctx, &sso.IdentityClaims{Subject: "sub-e", Provider: "kysignon", Roles: []string{access.AdminAppRole}}, kyBinding); err != nil {
			t.Fatal(err)
		}
	}}}
	req := httptest.NewRequest("POST", "/api/app-passwords", strings.NewReader(`{"label":"phone"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "tok-eve"})
	req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: "csrf"})
	req.Header.Set(auth.HeaderCSRF, "csrf")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("create across a promotion: %d %s, want 401", w.Code, w.Body.String())
	}
	if list, _ := real.AppPasswords().ListByUser(ctx, eve.ID); len(list) != 0 {
		t.Fatalf("a credential was stored: %+v", list[0])
	}
}

// SCIM's role or status change locks the row before revoking, like a login's.
func TestSCIMAccessChangeWaitsForAnIssuanceInFlight(t *testing.T) {
	s, _ := davInternalServer(t)
	if s.config.Database.Driver != "postgres" {
		t.Skip("postgres only: SQLite's single connection serialises the two")
	}
	ctx := context.Background()
	createUsers(t, s, &store.User{ID: "usr_dan", Username: "dan", Role: "user", Status: "active", SSOProvider: "scim", SSOSubject: "d1", SSOIssuer: kyBinding})
	raw, held := heldIssuance(t, s.config.Database.DSN, "usr_dan")
	done := make(chan int, 1)
	go func() {
		done <- scimCall(t, s, "PATCH", "/scim/v2/Users/usr_dan", map[string]any{
			"schemas":    []string{"urn:ietf:params:scim:api:messages:2.0:PatchOp"},
			"Operations": []map[string]any{{"op": "replace", "path": "active", "value": false}},
		}).Code
	}()
	waitForRowLock(t, raw)
	if err := held.Commit(); err != nil {
		t.Fatal(err)
	}
	if code := <-done; code != http.StatusOK {
		t.Fatalf("deactivation: %d", code)
	}
	if list, _ := s.store.AppPasswords().ListByUser(ctx, "usr_dan"); len(list) != 0 {
		t.Fatalf("a credential outlived the deactivation: %+v", list[0])
	}
}

// A create paused before its insert while SCIM promotes the person is refused on either backend.
func TestAppPasswordCreatedAcrossASCIMRoleChangeIsRefused(t *testing.T) {
	s, _ := davInternalServer(t)
	ctx := context.Background()
	dan := &store.User{ID: "usr_dan", Username: "dan", Role: "user", Status: "active", SSOProvider: "scim", SSOSubject: "d1", SSOIssuer: kyBinding}
	createUsers(t, s, dan)
	now := time.Now().UTC()
	if err := s.store.Sessions().CreateSession(ctx, &store.Session{TokenHash: crypto.SHA256Hex([]byte("tok-dan")), UserID: dan.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}, ""); err != nil {
		t.Fatal(err)
	}
	real := s.store
	scimServer := NewServer(s.config, real) // SCIM through the real store, outside the pause
	s.store = pausedStore{Store: real, ap: pausedIssuance{AppPasswordStore: real.AppPasswords(), pause: func() {
		body := map[string]any{"schemas": []string{"urn:ietf:params:scim:schemas:core:2.0:User"}, "userName": "dan", "active": true,
			"roles": []any{map[string]any{"value": access.AdminAppRole}}}
		if w := scimCall(t, scimServer, "PUT", "/scim/v2/Users/usr_dan", body); w.Code != http.StatusOK {
			t.Fatalf("scim promotion: %d %s", w.Code, w.Body.String())
		}
	}}}
	req := httptest.NewRequest("POST", "/api/app-passwords", strings.NewReader(`{"label":"phone"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "tok-dan"})
	req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: "csrf"})
	req.Header.Set(auth.HeaderCSRF, "csrf")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("create across a SCIM promotion: %d %s, want 401", w.Code, w.Body.String())
	}
	if list, _ := real.AppPasswords().ListByUser(ctx, dan.ID); len(list) != 0 {
		t.Fatalf("a credential was stored: %+v", list[0])
	}
}
