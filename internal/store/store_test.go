package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/store"
	"github.com/Busnes-app/kycalendar/internal/testdb"
	"github.com/google/uuid"
)

func newTestStore(t *testing.T) store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), testdb.Config(t))
	if err != nil {
		t.Fatalf("failed to open test store: %v", err)
	}

	t.Cleanup(func() {
		_ = st.Close()
	})

	return st
}

func TestUserStoreLifecycle(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	userID := uuid.NewString()
	user := &store.User{
		ID:           userID,
		Username:     "alice",
		Email:        "alice@busnes.app",
		DisplayName:  "Alice Admin",
		PasswordHash: "argon2id$mockedhash",
		Role:         "admin",
		Status:       "active",
		SSOProvider:  "local",
	}

	// 1. Create
	if err := st.Users().CreateUser(ctx, user); err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// Duplicate should fail
	if err := st.Users().CreateUser(ctx, user); err != store.ErrAlreadyExists {
		t.Fatalf("expected ErrAlreadyExists, got %v", err)
	}

	// 2. GetByID
	got, err := st.Users().GetUserByID(ctx, userID)
	if err != nil {
		t.Fatalf("GetUserByID error: %v", err)
	}
	if got.Username != "alice" || got.DisplayName != "Alice Admin" {
		t.Errorf("unexpected user data: %+v", got)
	}

	// 3. GetByUsername (case-insensitive)
	gotByU, err := st.Users().GetUserByUsername(ctx, "ALICE")
	if err != nil {
		t.Fatalf("GetUserByUsername error: %v", err)
	}
	if gotByU.ID != userID {
		t.Errorf("expected ID %s, got %s", userID, gotByU.ID)
	}

	// 4. GetByEmail
	gotByE, err := st.Users().GetUserByEmail(ctx, "alice@busnes.app")
	if err != nil {
		t.Fatalf("GetUserByEmail error: %v", err)
	}
	if gotByE.ID != userID {
		t.Errorf("expected ID %s, got %s", userID, gotByE.ID)
	}

	// 5. Update
	user.DisplayName = "Alice Operations"
	user.Role = "manager"
	if err := st.Users().UpdateUser(ctx, user); err != nil {
		t.Fatalf("UpdateUser error: %v", err)
	}
	gotUpdated, _ := st.Users().GetUserByID(ctx, userID)
	if gotUpdated.DisplayName != "Alice Operations" || gotUpdated.Role != "manager" {
		t.Errorf("update not reflected: %+v", gotUpdated)
	}

	// 6. List & Count
	users, count, err := st.Users().ListUsers(ctx, 0, 10, store.UserFilter{Field: store.UserFieldUsername, Value: "ALICE"})
	if err != nil {
		t.Fatalf("ListUsers error: %v", err)
	}
	if count != 1 || len(users) != 1 {
		t.Errorf("expected 1 user, got count=%d len=%d", count, len(users))
	}

	// 7. Delete
	if err := st.Users().DeleteUser(ctx, userID); err != nil {
		t.Fatalf("DeleteUser error: %v", err)
	}
	if _, err := st.Users().GetUserByID(ctx, userID); err != store.ErrNotFound {
		t.Fatalf("expected ErrNotFound after deletion, got %v", err)
	}
}

func TestSessionStoreLifecycle(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	userID := uuid.NewString()
	user := &store.User{
		ID:       userID,
		Username: "bob",
		Role:     "user",
		Status:   "active",
	}
	_ = st.Users().CreateUser(ctx, user)

	tokenHash := "mockhash12345"
	sess := &store.Session{
		TokenHash: tokenHash,
		UserID:    userID,
		UserAgent: "Mozilla/5.0 BusnesApp",
		IPAddress: "127.0.0.1",
		CreatedAt: time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(1 * time.Hour),
	}

	if err := st.Sessions().CreateSession(ctx, sess, user.PasswordHash); err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	got, err := st.Sessions().GetSession(ctx, tokenHash)
	if err != nil {
		t.Fatalf("GetSession error: %v", err)
	}
	if got.UserID != userID {
		t.Errorf("expected userID %s, got %s", userID, got.UserID)
	}

	if err := st.Sessions().DeleteSession(ctx, tokenHash); err != nil {
		t.Fatalf("DeleteSession error: %v", err)
	}
	if _, err := st.Sessions().GetSession(ctx, tokenHash); err != store.ErrNotFound {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestDevicePairingLifecycle(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	pairing := &store.DevicePairing{
		Secret:     "secret-pairing-token-abc",
		DeviceName: "Yoshi's Pixel 9",
		Platform:   "android",
		Status:     "pending",
		CreatedAt:  time.Now().UTC(),
		ExpiresAt:  time.Now().UTC().Add(90 * time.Second),
	}

	if err := st.Devices().CreatePairing(ctx, pairing); err != nil {
		t.Fatalf("CreatePairing error: %v", err)
	}

	bySecret, err := st.Devices().GetPairingBySecret(ctx, "secret-pairing-token-abc")
	if err != nil {
		t.Fatalf("GetPairingBySecret error: %v", err)
	}
	if bySecret.Status != "pending" {
		t.Errorf("unexpected status: %s", bySecret.Status)
	}

	if err := st.Devices().ConsumePairing(ctx, pairing.Secret, "Pixel", "android", "fcm-token-xyz"); err != nil {
		t.Fatalf("ConsumePairing error: %v", err)
	}

	updated, _ := st.Devices().GetPairingBySecret(ctx, pairing.Secret)
	if updated.Status != "consumed" || updated.PushToken != "fcm-token-xyz" {
		t.Errorf("pairing update failed: %+v", updated)
	}
}

func TestGroupStoreAndMembers(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	u1 := &store.User{ID: uuid.NewString(), Username: "u1", Role: "user", Status: "active"}
	u2 := &store.User{ID: uuid.NewString(), Username: "u2", Role: "user", Status: "active"}
	_ = st.Users().CreateUser(ctx, u1)
	_ = st.Users().CreateUser(ctx, u2)

	grp := &store.Group{
		ID:          uuid.NewString(),
		DisplayName: "Engineering",
		ExternalID:  "okta-grp-eng-001",
	}

	if err := st.Groups().CreateGroup(ctx, grp); err != nil {
		t.Fatalf("CreateGroup error: %v", err)
	}

	_ = st.Groups().AddGroupMember(ctx, grp.ID, u1.ID)
	_ = st.Groups().AddGroupMember(ctx, grp.ID, u2.ID)

	got, err := st.Groups().GetGroupByID(ctx, grp.ID)
	if err != nil {
		t.Fatalf("GetGroupByID error: %v", err)
	}
	if len(got.Members) != 2 {
		t.Errorf("expected 2 members, got %d", len(got.Members))
	}

	u1Groups, err := st.Groups().GetUserGroups(ctx, u1.ID)
	if err != nil {
		t.Fatalf("GetUserGroups error: %v", err)
	}
	if len(u1Groups) != 1 || u1Groups[0].DisplayName != "Engineering" {
		t.Errorf("unexpected user groups: %+v", u1Groups)
	}
}

func TestAuditAndSettings(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	// Audit log
	rec := &store.AuditRecord{
		UserID:    "user-1",
		Action:    "auth.login",
		Resource:  "session",
		Details:   `{"method":"totp"}`,
		IPAddress: "127.0.0.1",
	}
	if err := st.Audit().LogAudit(ctx, rec); err != nil {
		t.Fatalf("LogAudit error: %v", err)
	}

	records, count, err := st.Audit().ListAuditRecords(ctx, 0, 10)
	if err != nil || count != 1 || len(records) != 1 {
		t.Fatalf("ListAuditRecords failed: count=%d, err=%v", count, err)
	}

	// Settings
	if err := st.Settings().SetSetting(ctx, "theme_default", "patina"); err != nil {
		t.Fatalf("SetSetting error: %v", err)
	}
	val, err := st.Settings().GetSetting(ctx, "theme_default")
	if err != nil || val != "patina" {
		t.Fatalf("GetSetting failed: val=%s, err=%v", val, err)
	}
}

func TestSpendTOTPCounterRefusesReplay(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	u := &store.User{ID: "usr_t", Username: "t", Role: "user", Status: "active", SSOProvider: "local"}
	if err := st.Users().CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := st.Users().SpendTOTPCounter(ctx, u.ID, 100); err != nil {
		t.Fatalf("first spend: %v", err)
	}
	if err := st.Users().SpendTOTPCounter(ctx, u.ID, 100); !errors.Is(err, store.ErrAlreadyExists) {
		t.Fatalf("replay: got %v, want ErrAlreadyExists", err)
	}
	if err := st.Users().SpendTOTPCounter(ctx, u.ID, 99); !errors.Is(err, store.ErrAlreadyExists) {
		t.Fatalf("older counter: got %v, want ErrAlreadyExists", err)
	}
	if err := st.Users().SpendTOTPCounter(ctx, u.ID, 101); err != nil {
		t.Fatalf("next counter: %v", err)
	}
	got, _ := st.Users().GetUserByID(ctx, u.ID)
	if got.TOTPLastCounter != 101 {
		t.Fatalf("stored counter %d, want 101", got.TOTPLastCounter)
	}
}

func TestDeleteSettingIsIdempotent(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	if err := st.Settings().DeleteSetting(ctx, "never"); err != nil {
		t.Fatal(err)
	}
	_ = st.Settings().SetSetting(ctx, "k", "v")
	_ = st.Settings().DeleteSetting(ctx, "k")
	if _, err := st.Settings().GetSetting(ctx, "k"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// Usernames are unique only case-sensitively, so an SSO "ADMIN" can exist beside local "admin";
// the case-insensitive lookup behind password login and init-admin must pick the local account.
func TestGetUserByUsernamePrefersLocalAccount(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_sso", Username: "ADMIN", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "s1"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_local", Username: "admin", Role: "admin", Status: "active", SSOProvider: "local"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"admin", "Admin", "ADMIN"} {
		u, err := st.Users().GetUserByUsername(ctx, name)
		if err != nil || u.ID != "usr_local" {
			t.Errorf("%s: got %+v %v, want usr_local", name, u, err)
		}
	}
}

// A deleted user's calendars must go with them, or their bytes count against the instance cap forever.
func TestDeleteUserRemovesTheirCalendars(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_gone", Username: "gone", Role: "user", Status: "active", SSOProvider: "local"}); err != nil {
		t.Fatal(err)
	}
	cs := st.Calendars()
	c := &store.Calendar{ID: "cal_gone", OwnerKind: "user", OwnerID: "usr_gone", Slug: "default", Name: "x"}
	if err := cs.CreateCalendar(ctx, c, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.PutObject(ctx, &store.CalendarObject{CalendarID: c.ID, Name: "a.ics", UID: "u", Data: []byte("abcd"), FirstStart: 1}, "", false, store.OwnerLimits{}); err != nil {
		t.Fatal(err)
	}
	if err := st.Users().DeleteUser(ctx, "usr_gone"); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.GetCalendarBySlug(ctx, "user", "usr_gone", "default"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("calendar survived its owner: %v", err)
	}
	// The freed bytes are available to everyone else again.
	other := &store.Calendar{ID: "cal_other", OwnerKind: "user", OwnerID: "usr_other", Slug: "default", Name: "y"}
	if err := cs.CreateCalendar(ctx, other, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.PutObject(ctx, &store.CalendarObject{CalendarID: other.ID, Name: "b.ics", UID: "v", Data: []byte("efgh"), FirstStart: 1}, "", false, store.OwnerLimits{MaxTotalBytes: 4}); err != nil {
		t.Fatalf("deleted user's bytes still counted: %v", err)
	}
}

func TestResetAfterRestore(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	u := &store.User{ID: "restored", Username: "restored", PasswordHash: "h", Role: "user", Status: "active", SSOProvider: "local"}
	if err := st.Users().CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	sess := &store.Session{TokenHash: "session", UserID: u.ID, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	if err := st.Sessions().CreateSession(ctx, sess, "h"); err != nil {
		t.Fatal(err)
	}
	challenge := &store.MFAChallenge{TokenHash: "challenge", UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour)}
	if err := st.Sessions().CreateMFAChallenge(ctx, challenge, "h"); err != nil {
		t.Fatal(err)
	}
	addAppPassword(t, st, u.ID)

	before, err := st.Calendars().SyncEpoch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ResetAfterRestore(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := st.Calendars().SyncEpoch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after == before || len(after) != 16 {
		t.Fatalf("epoch %q -> %q", before, after)
	}
	if _, err := st.Sessions().GetSession(ctx, sess.TokenHash); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("session survived", err)
	}
	if _, _, err := st.Sessions().ConsumeMFAChallenge(ctx, challenge.TokenHash); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("challenge survived", err)
	}
	checkAppPasswordsRevoked(t, st, u.ID)
	// A forced change proves nothing: whoever knows a leaked restored password can satisfy it.
	if got, err := st.Users().GetUserByID(ctx, u.ID); err != nil || got.MustChangePassword || got.PasswordHash != "h" {
		t.Fatalf("local account after reset: %+v %v", got, err)
	}

	// Idempotent: an interrupted restore is finished by running it again.
	if err := st.ResetAfterRestore(ctx); err != nil {
		t.Fatal(err)
	}
	again, _ := st.Calendars().SyncEpoch(ctx)
	if again == after {
		t.Fatal("a second reset kept the epoch")
	}
	audits, n, err := st.Audit().ListAuditRecords(ctx, 0, 10)
	if err != nil || n != 2 {
		t.Fatal("audit", n, err)
	}
	for _, a := range audits {
		if a.Action != "system.restore_reset" || a.UserID != "system" || !strings.Contains(a.Details, "sync_epoch=") {
			t.Fatalf("audit row %+v", a)
		}
	}
}
