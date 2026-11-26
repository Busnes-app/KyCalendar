//go:build kye2e

package suite

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// issuer is KyIdentity's public origin. The driver reaches it on a loopback port and sends
// this as Host and Origin, as a browser behind the TLS front would.
const issuer = "https://id.e2e.test"

// jar keeps cookies by name for one origin. It ignores Secure: the driver talks plain HTTP to
// a loopback port, and net/http/cookiejar would drop KyIdentity's Secure cookies.
type jar struct {
	mu sync.Mutex
	c  map[string]*http.Cookie
}

func (j *jar) SetCookies(_ *url.URL, cs []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, c := range cs {
		if c.MaxAge < 0 || c.Value == "" {
			delete(j.c, c.Name)
		} else {
			j.c[c.Name] = &http.Cookie{Name: c.Name, Value: c.Value}
		}
	}
}

func (j *jar) Cookies(*url.URL) []*http.Cookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	var out []*http.Cookie
	for _, c := range j.c {
		out = append(out, c)
	}
	return out
}

func (j *jar) get(name string) string {
	j.mu.Lock()
	defer j.mu.Unlock()
	if c := j.c[name]; c != nil {
		return c.Value
	}
	return ""
}

// browser is one person's session at KyIdentity's public port.
type browser struct {
	t    *testing.T
	base string // http://127.0.0.1:<port>
	jar  *jar
	hc   *http.Client
}

func newBrowser(t *testing.T, base string) *browser {
	j := &jar{c: map[string]*http.Cookie{}}
	return &browser{t: t, base: base, jar: j, hc: &http.Client{Jar: j, Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// do sends a JSON request with the CSRF header and any extra headers, returning status and body.
func (b *browser) do(method, path string, body any, hdr map[string]string) (int, []byte) {
	b.t.Helper()
	var r io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			b.t.Fatal(err)
		}
		r = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, b.base+path, r)
	if err != nil {
		b.t.Fatal(err)
	}
	req.Host = strings.TrimPrefix(issuer, "https://")
	req.Header.Set("Origin", issuer)
	req.Header.Set("Content-Type", "application/json")
	if v := b.jar.get("kyidentity_csrf"); v != "" {
		req.Header.Set("X-CSRF-Token", v)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := b.hc.Do(req)
	if err != nil {
		b.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	return res.StatusCode, out
}

// must is do that fails the test unless the answer is want.
func (b *browser) must(want int, method, path string, body any, hdr map[string]string) []byte {
	b.t.Helper()
	code, out := b.do(method, path, body, hdr)
	if code != want {
		b.t.Fatalf("kyidentity %s %s: %d, want %d: %s", method, path, code, want, out)
	}
	return out
}

// signIn takes a CSRF token, then signs in with a password alone (E8).
func (b *browser) signIn(username, password string) {
	b.t.Helper()
	b.must(200, "GET", "/api/auth/csrf", nil, nil)
	b.must(200, "POST", "/api/auth/login", map[string]string{"username": username, "password": password}, nil)
}

// stepUp re-proves the password for one exact method and path, as KyIdentity requires for
// every sensitive admin write, and returns the header that carries it.
func (b *browser) stepUp(password, method, path string) map[string]string {
	b.t.Helper()
	var ans struct {
		Kind  string `json:"kind"`
		Token string `json:"stepUpToken"`
	}
	raw := b.must(200, "POST", "/api/auth/step-up", map[string]string{"password": password, "method": "", "operation": method + " " + path}, nil)
	if err := json.Unmarshal(raw, &ans); err != nil || ans.Kind != "grant" || ans.Token == "" {
		b.t.Fatalf("step-up for %s %s: %s", method, path, raw)
	}
	return map[string]string{"X-KyIdentity-StepUp": ans.Token}
}

// admin is a step-up admin write.
func (b *browser) admin(password string, want int, method, path string, body any) []byte {
	b.t.Helper()
	return b.must(want, method, path, body, b.stepUp(password, method, path))
}
