package api

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kycalendar/internal/access"
	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// signingIdP issues a signed id_token for sub. onToken runs inside the token request: after the
// callback's first provider check, before it writes any account.
type signingIdP struct {
	*httptest.Server
	sub, nonce string
	onToken    func()
}

func newSigningIdP(t *testing.T) *signingIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	b64 := base64.RawURLEncoding.EncodeToString
	idp := &signingIdP{}
	idp.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/keys":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
				"kty": "RSA", "kid": "k1", "alg": "RS256", "use": "sig",
				"n": b64(key.N.Bytes()), "e": b64(big.NewInt(int64(key.E)).Bytes()),
			}}})
		case "/token":
			if idp.onToken != nil {
				idp.onToken()
			}
			head, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k1", "typ": "JWT"})
			body, _ := json.Marshal(map[string]any{
				"iss": idp.URL, "aud": "kc", "sub": idp.sub, "nonce": idp.nonce, "preferred_username": "user-" + idp.sub,
				"roles": []string{access.AdminAppRole}, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix(),
			})
			signing := b64(head) + "." + b64(body)
			sum := sha256.Sum256([]byte(signing))
			sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
			if err != nil {
				t.Error(err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "a", "token_type": "Bearer", "id_token": signing + "." + b64(sig)})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer": idp.URL, "authorization_endpoint": idp.URL + "/authorize",
				"token_endpoint": idp.URL + "/token", "jwks_uri": idp.URL + "/keys",
			})
		}
	}))
	t.Cleanup(idp.Close)
	return idp
}

// login starts a login for sub and returns its callback request.
func (idp *signingIdP) login(t *testing.T, s *Server, sub string) *http.Request {
	t.Helper()
	req := startLogin(t, s)
	for _, c := range req.Cookies() {
		if strings.HasPrefix(c.Name, "ky_nonce_") {
			_, idp.nonce, _ = strings.Cut(c.Value, ".")
		}
	}
	idp.sub = sub
	return req
}

// lockProbe reports, from inside the callback's account lookup, whether a save could take signinMu.
type lockProbe struct {
	store.Store
	s      *Server
	locked *bool
}
type lockProbeUsers struct {
	store.UserStore
	p lockProbe
}

func (p lockProbe) Users() store.UserStore { return lockProbeUsers{p.Store.Users(), p} }
func (u lockProbeUsers) GetUserBySSO(ctx context.Context, provider, subject string) (*store.User, error) {
	if u.p.s.signinMu.TryLock() {
		u.p.s.signinMu.Unlock()
	} else {
		*u.p.locked = true
	}
	return u.UserStore.GetUserBySSO(ctx, provider, subject)
}

// A save that rebinds sign-in while a callback is exchanging its code makes that callback fail
// once (spec §4): no account for the old provider's sub, no session. The account write itself
// runs under signinMu, so no save can commit between the recheck and the session.
func TestCallbackAcrossASaveDuringExchangeIsRefused(t *testing.T) {
	s, _ := davInternalServer(t)
	ctx := context.Background()
	idp := newSigningIdP(t)
	if _, _, err := s.bindAccounts(ctx, kyidentityAt(idp.URL)); err != nil {
		t.Fatal(err)
	}
	createUsers(t, s, &store.User{ID: "usr_carol", Username: "carol", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "sub-carol"})
	s.signin.Store(sso.NewProvider(sso.KindKyIdentity, "KyIdentity", idp.URL, "kc", "", nil))

	// The account write runs under signinMu.
	locked := false
	s.store = lockProbe{Store: s.store, s: s, locked: &locked}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, idp.login(t, s, "sub-carol"))
	if w.Code != http.StatusFound || !locked {
		t.Fatalf("plain login: %d %s, under signinMu %v", w.Code, w.Body.String(), locked)
	}
	s.store = s.store.(lockProbe).Store
	if err := s.store.Sessions().DeleteUserSessions(ctx, "usr_carol"); err != nil {
		t.Fatal(err)
	}

	for _, sub := range []string{"sub-new", "sub-carol"} {
		idp.onToken = nil
		if _, _, err := s.bindAccounts(ctx, kyidentityAt(idp.URL)); err != nil {
			t.Fatal(err)
		}
		carol, err := s.store.Users().GetUserByID(ctx, "usr_carol")
		if err != nil {
			t.Fatal(err)
		}
		carol.Status = "active"
		if err := s.store.Users().UpdateUser(ctx, carol); err != nil {
			t.Fatal(err)
		}
		s.signin.Store(sso.NewProvider(sso.KindKyIdentity, "KyIdentity", idp.URL, "kc", "", nil))
		req := idp.login(t, s, sub)
		next := "https://new" + sub + ".example"
		idp.onToken = func() {
			s.signinMu.Lock()
			defer s.signinMu.Unlock()
			if _, _, err := s.commitSignIn(ctx, store.System, kyidentityAt(next), nil); err != nil {
				t.Error(err)
			}
			s.signin.Store(sso.NewProvider(sso.KindKyIdentity, "KyIdentity", next, "kc", "", nil))
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "start again") {
			t.Fatalf("%s across a save: %d %s, want 400 start again", sub, w.Code, w.Body.String())
		}
		u, err := s.store.Users().GetUserBySSO(ctx, "kysignon", sub)
		switch {
		case sub == "sub-new" && !errors.Is(err, store.ErrNotFound):
			t.Fatalf("callback across a save created %+v %v", u, err)
		case sub == "sub-carol" && (err != nil || u.Status != "inactive"):
			t.Fatalf("carol after the save: %+v %v, want inactive", u, err)
		}
		if len(w.Result().Cookies()) != 0 {
			t.Fatalf("%s: callback set cookies %v", sub, w.Result().Cookies())
		}
	}
	if n, err := s.store.Users().CountSSOAccounts(ctx, []string{"kysignon"}); err != nil || n != 0 {
		t.Fatalf("active kysignon accounts: %d %v, want 0", n, err)
	}
}
