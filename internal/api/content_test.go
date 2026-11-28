package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/api"
	"github.com/Busnes-app/kycalendar/internal/apppass"
	"github.com/Busnes-app/kycalendar/internal/auth"
	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/contentkey"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/store"
)

const evSecret = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:evt-s\r\nDTSTAMP:20261001T000000Z\r\nDTSTART:20261008T090000Z\r\nDTEND:20261008T100000Z\r\nSUMMARY:Secret merger call\r\nDESCRIPTION:Acme numbers\r\nX-KEEP;P=1:value\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

// signIn opens another session for an existing local user.
func signIn(t *testing.T, srv *api.Server, username string) *http.Cookie {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": testPassword})
	req := httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			return c
		}
	}
	t.Fatalf("sign-in: %d %s", w.Code, w.Body.String())
	return nil
}

// seedAppPassword is an app password made before setup, with no slot.
func seedAppPassword(t *testing.T, st store.Store, userID string) string {
	t.Helper()
	id, token, hash, err := apppass.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AppPasswords().Create(context.Background(), store.Seed, &store.AppPassword{Scope: store.ScopeDAV, ID: id, UserID: userID, Label: "phone", Hash: hash}, 0); err != nil {
		t.Fatal(err)
	}
	return token
}

// storedData is what the database holds for object name: a worker's read is the row as stored.
func storedData(t *testing.T, st store.Store, cal, name string) (data, sealed []byte) {
	t.Helper()
	o, err := st.Calendars().GetObject(context.Background(), cal, name)
	if err != nil {
		t.Fatal(err)
	}
	return o.Data, o.Sealed
}

func TestEncryptedAccountOverCalDAVAndWeb(t *testing.T) {
	srv, st, _ := setupTestServerWith(t, func(c *config.Config) { c.Calendar.ContentEncryption = true })
	alice := loginAs(t, srv, st, "alice", "user")
	phone := seedAppPassword(t, st, "usr_alice")
	ts := httptest.NewServer(srv)
	defer ts.Close()
	path := "/dav/usr_alice/calendars/default/"
	// Before setup: today's behaviour (and creates the default calendar).
	if r := rawDAV(t, ts, "PUT", path+"evt-a.ics", "alice", phone, evA, map[string]string{"Content-Type": "text/calendar"}); r.StatusCode != http.StatusCreated {
		t.Fatalf("PUT before setup %d %s", r.StatusCode, readAll(r))
	}

	if w := do(t, srv, "GET", "/api/encryption", alice); !strings.Contains(w.Body.String(), `"set_up":false`) {
		t.Fatalf("status before setup: %s", w.Body.String())
	}
	w := do(t, srv, "POST", "/api/encryption/recovery-code", alice)
	var rc struct {
		Code string `json:"recovery_code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &rc)
	if rc.Code == "" {
		t.Fatalf("recovery code: %d %s", w.Code, w.Body.String())
	}
	if w := doJSON(t, srv, "POST", "/api/encryption/setup", alice, map[string]string{"passphrase": "short", "recovery_code": rc.Code}); w.Code != http.StatusBadRequest {
		t.Fatalf("a short passphrase was accepted: %d", w.Code)
	}
	if w := doJSON(t, srv, "POST", "/api/encryption/setup", alice, map[string]string{"passphrase": "correct horse battery", "recovery_code": rc.Code}); w.Code != http.StatusNoContent {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}
	if w := doJSON(t, srv, "POST", "/api/encryption/setup", alice, map[string]string{"passphrase": "correct horse battery", "recovery_code": rc.Code}); w.Code != http.StatusConflict {
		t.Fatalf("a second setup: %d", w.Code)
	}

	// The existing app password keeps syncing through the transition, and what it writes is sealed.
	if r := rawDAV(t, ts, "PUT", path+"evt-s.ics", "alice", phone, evSecret, map[string]string{"Content-Type": "text/calendar"}); r.StatusCode != http.StatusCreated {
		t.Fatalf("PUT after setup %d %s", r.StatusCode, readAll(r))
	}
	if body := readAll(rawDAV(t, ts, "GET", path+"evt-s.ics", "alice", phone, "", nil)); body != evSecret {
		t.Fatalf("CalDAV GET of a sealed object is not the bytes sent:\n%q", body)
	}
	if body := readAll(rawDAV(t, ts, "GET", path+"evt-a.ics", "alice", phone, "", nil)); body != evA {
		t.Fatalf("a legacy object changed:\n%q", body)
	}
	cals, err := st.Calendars().ListCalendarsByOwner(context.Background(), "user", "usr_alice")
	if err != nil || len(cals) != 1 {
		t.Fatal(err)
	}
	data, sealed := storedData(t, st, cals[0].ID, "evt-s.ics")
	if sealed == nil || bytes.Contains(data, []byte("Secret merger call")) || bytes.Contains(data, []byte("Acme")) || bytes.Contains(data, []byte("X-KEEP")) {
		t.Fatalf("content reached data:\n%s", data)
	}
	report := `<?xml version="1.0"?><C:calendar-multiget xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav"><D:prop><D:getetag/><C:calendar-data/></D:prop><D:href>` + path + `evt-s.ics</D:href></C:calendar-multiget>`
	if r := rawDAV(t, ts, "REPORT", path, "alice", phone, report, map[string]string{"Content-Type": "application/xml", "Depth": "1"}); !strings.Contains(readAll(r), "Secret merger call") {
		t.Fatal("calendar-multiget did not serve the merged object")
	}

	// The web session that ran setup reads it; a new session is locked until it unlocks.
	events := "/api/events?start=2026-10-01T00:00:00Z&end=2026-11-01T00:00:00Z"
	if w := do(t, srv, "GET", events, alice); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Secret merger call") {
		t.Fatalf("setup session events: %d %s", w.Code, w.Body.String())
	}
	second := signIn(t, srv, "alice")
	if w := do(t, srv, "GET", events, second); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "content_locked") {
		t.Fatalf("a session that has not unlocked: %d %s", w.Code, w.Body.String())
	}
	if w := doJSON(t, srv, "POST", "/api/encryption/unlock", second, map[string]string{"passphrase": "wrong horse battery"}); w.Code != http.StatusForbidden {
		t.Fatalf("a wrong passphrase: %d", w.Code)
	}
	if w := doJSON(t, srv, "POST", "/api/encryption/unlock", second, map[string]string{"recovery_code": rc.Code}); w.Code != http.StatusNoContent {
		t.Fatalf("recovery-code unlock: %d %s", w.Code, w.Body.String())
	}
	if w := do(t, srv, "GET", events, second); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Secret merger call") {
		t.Fatalf("after unlock: %d", w.Code)
	}

	// A new app password made by an unlocked session gets its own slot.
	w = doJSON(t, srv, "POST", "/api/app-passwords", second, map[string]string{"label": "laptop", "scope": "dav"})
	var created struct{ Password string }
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if created.Password == "" {
		t.Fatalf("app password: %d %s", w.Code, w.Body.String())
	}

	// Ending the transition: the moved-over and the new app password keep working, one that
	// never synced is refused with 403 (E10), never served a skeleton.
	unused := seedAppPassword(t, st, "usr_alice")
	if w := do(t, srv, "POST", "/api/encryption/end-transition", second); w.Code != http.StatusNoContent {
		t.Fatalf("end transition: %d %s", w.Code, w.Body.String())
	}
	for _, tok := range []string{phone, created.Password} {
		if body := readAll(rawDAV(t, ts, "GET", path+"evt-s.ics", "alice", tok, "", nil)); body != evSecret {
			t.Fatalf("after the rotation a slotted app password reads %q", body)
		}
	}
	r := rawDAV(t, ts, "GET", path+"evt-s.ics", "alice", unused, "", nil)
	if body := readAll(r); r.StatusCode != http.StatusForbidden || strings.Contains(body, "merger") {
		t.Fatalf("an app password without a slot: %d %s", r.StatusCode, body)
	}
	if r := rawDAV(t, ts, "PUT", path+"evt-x.ics", "alice", unused, strings.ReplaceAll(evSecret, "evt-s", "evt-x"), map[string]string{"Content-Type": "text/calendar"}); r.StatusCode != http.StatusForbidden {
		t.Fatalf("an app password without a slot wrote: %d", r.StatusCode)
	}
}

func TestContentEncryptionOffChangesNothing(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	alice := loginAs(t, srv, st, "alice", "user")
	if w := do(t, srv, "GET", "/api/encryption", alice); !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Fatalf("status with the setting off: %s", w.Body.String())
	}
	if w := doJSON(t, srv, "POST", "/api/encryption/setup", alice, map[string]string{"passphrase": "correct horse battery"}); w.Code != http.StatusNotFound {
		t.Fatalf("setup with the setting off: %d", w.Code)
	}
}

// Wrong unlock secrets are counted before they are checked: after the per-user limit even the
// right passphrase waits.
func TestContentUnlockIsRateLimited(t *testing.T) {
	srv, st, _ := setupTestServerWith(t, func(c *config.Config) { c.Calendar.ContentEncryption = true })
	alice := loginAs(t, srv, st, "alice", "user")
	w := do(t, srv, "POST", "/api/encryption/recovery-code", alice)
	var rc struct {
		Code string `json:"recovery_code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &rc)
	if w := doJSON(t, srv, "POST", "/api/encryption/setup", alice, map[string]string{"passphrase": "correct horse battery", "recovery_code": rc.Code}); w.Code != http.StatusNoContent {
		t.Fatalf("setup: %d", w.Code)
	}
	second := signIn(t, srv, "alice")
	for i := 0; i < 50; i++ {
		if w := doJSON(t, srv, "POST", "/api/encryption/unlock", second, map[string]string{"recovery_code": "AAAA-AAAA-AAAA-AAAA-AAAAA-AAAAA"}); w.Code != http.StatusForbidden {
			t.Fatalf("wrong code %d: %d", i, w.Code)
		}
	}
	if w := doJSON(t, srv, "POST", "/api/encryption/unlock", second, map[string]string{"recovery_code": rc.Code}); w.Code != http.StatusTooManyRequests {
		t.Fatalf("after 50 failures the right code got %d, want 429", w.Code)
	}
}

// Setup needs a recent sign-in: a stale session cannot plant permanent unlock secrets.
func TestContentSetupNeedsRecentSignIn(t *testing.T) {
	srv, st, _ := setupTestServerWith(t, func(c *config.Config) { c.Calendar.ContentEncryption = true })
	alice := loginAs(t, srv, st, "alice", "user")
	w := do(t, srv, "POST", "/api/encryption/recovery-code", alice)
	code := between(w.Body.String(), `"recovery_code":"`, `"`)
	restore := api.SetStepUpWindowForTest(0)
	defer restore()
	if w := doJSON(t, srv, "POST", "/api/encryption/setup", alice, map[string]string{"passphrase": "correct horse battery", "recovery_code": code}); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "reauth_required") {
		t.Fatalf("setup on a stale session: %d %s", w.Code, w.Body.String())
	}
	if n, err := st.Content().SetUpAccounts(context.Background()); err != nil || n != 0 {
		t.Fatalf("a stale session created %d account keys (%v)", n, err)
	}
}

// slotStore lets a test act just before a new credential's slot is written.
type slotStore struct {
	store.Store
	before func() error
}

func (s slotStore) Content() store.ContentStore { return slotContent{s.Store.Content(), s.before} }

type slotContent struct {
	store.ContentStore
	before func() error
}

func (c slotContent) AddSlot(ctx context.Context, account *contentkey.KeyPair, cred store.Credential) error {
	if err := c.before(); err != nil {
		return err
	}
	return c.ContentStore.AddSlot(ctx, account, cred)
}

// An app password is only handed out with a slot that opens the account: a rotation between
// opening the account and adding the slot is retried with the new key, and a slot that cannot
// be written revokes the credential and fails the request.
func TestNewAppPasswordAlwaysUnlocks(t *testing.T) {
	srv, st, cfg := setupTestServerWith(t, func(c *config.Config) { c.Calendar.ContentEncryption = true })
	alice := loginAs(t, srv, st, "alice", "user")
	phone := seedAppPassword(t, st, "usr_alice")
	ts := httptest.NewServer(srv)
	defer ts.Close()
	path := "/dav/usr_alice/calendars/default/"
	rawDAV(t, ts, "PROPFIND", "/dav/usr_alice/calendars/", "alice", phone, "", map[string]string{"Depth": "1"})
	w := do(t, srv, "POST", "/api/encryption/recovery-code", alice)
	code := between(w.Body.String(), `"recovery_code":"`, `"`)
	if w := doJSON(t, srv, "POST", "/api/encryption/setup", alice, map[string]string{"passphrase": "correct horse battery", "recovery_code": code}); w.Code != http.StatusNoContent {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}
	if r := rawDAV(t, ts, "PUT", path+"evt-s.ics", "alice", phone, evSecret, map[string]string{"Content-Type": "text/calendar"}); r.StatusCode != http.StatusCreated {
		t.Fatalf("PUT %d %s", r.StatusCode, readAll(r))
	}
	labels := func() []string {
		list, err := st.AppPasswords().ListByUser(context.Background(), "usr_alice")
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, p := range list {
			out = append(out, p.Label)
		}
		return out
	}

	// The transition ends (the rotation) after the request opened the account key.
	rotated := false
	api.SetStoreForTest(srv, slotStore{st, func() error {
		if !rotated {
			rotated = true
			return st.Content().EndTransition(context.Background(), "usr_alice", crypto.DeriveKey(cfg.Security.EncryptionKey, "kycalendar:content-transition"))
		}
		return nil
	}})
	w = doJSON(t, srv, "POST", "/api/app-passwords", alice, map[string]string{"label": "laptop", "scope": "dav"})
	var created struct{ Password string }
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if w.Code != http.StatusCreated || !rotated {
		t.Fatalf("app password across a rotation: %d %s (rotated %v)", w.Code, w.Body.String(), rotated)
	}
	if body := readAll(rawDAV(t, ts, "GET", path+"evt-s.ics", "alice", created.Password, "", nil)); body != evSecret {
		t.Fatalf("the new app password does not unlock after the rotation: %q", body)
	}

	// The slot write fails: no credential is handed out, and none is left behind.
	api.SetStoreForTest(srv, slotStore{st, func() error { return errors.New("disk full") }})
	w = doJSON(t, srv, "POST", "/api/app-passwords", alice, map[string]string{"label": "tablet", "scope": "dav"})
	if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), `"password":`) {
		t.Fatalf("a failed slot write: %d %s", w.Code, w.Body.String())
	}
	if l := labels(); slices.Contains(l, "tablet") {
		t.Fatalf("the credential without a slot was kept: %v", l)
	}

	// Opening the account fails for a reason other than locked or not set up: the same.
	api.SetStoreForTest(srv, st)
	db := rawDB(t, cfg.Database.Driver, cfg.Database.DSN)
	if _, err := db.Exec(`ALTER TABLE key_slots RENAME TO key_slots_away`); err != nil {
		t.Fatal(err)
	}
	w = doJSON(t, srv, "POST", "/api/app-passwords", alice, map[string]string{"label": "watch", "scope": "dav"})
	if _, err := db.Exec(`ALTER TABLE key_slots_away RENAME TO key_slots`); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), `"password":`) {
		t.Fatalf("an account that failed to open: %d %s", w.Code, w.Body.String())
	}
	if l := labels(); slices.Contains(l, "watch") {
		t.Fatalf("the credential without a slot was kept: %v", l)
	}
}
