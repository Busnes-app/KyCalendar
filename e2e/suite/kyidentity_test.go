package suite

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"
)

// issuer is KyIdentity's public origin, served by front like every other *.e2e.test name.
const issuer = "https://id.e2e.test"

// frontTransport dials front's loopback port for every *.e2e.test name and verifies the
// certificate against the run's CA, so the driver is a browser on the real https origins.
func frontTransport(t *testing.T, caPEM, front string) *http.Transport {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(caPEM)) {
		t.Fatal("run CA")
	}
	var d net.Dialer
	return &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool},
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if host, _, _ := net.SplitHostPort(addr); !strings.HasSuffix(host, ".e2e.test") {
				return nil, &net.AddrError{Err: "the driver reaches only *.e2e.test", Addr: addr}
			}
			return d.DialContext(ctx, network, front)
		},
	}
}

// browser is one person's cookies across every *.e2e.test origin.
type browser struct {
	t   *testing.T
	jar *cookiejar.Jar
	hc  *http.Client
}

func (s *suite) browser() *browser {
	j, _ := cookiejar.New(nil)
	return &browser{t: s.t, jar: j, hc: &http.Client{Jar: j, Transport: s.web, Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// csrfCookies are the double-submit cookies of KyIdentity, KyCalendar and KyMessages.
var csrfCookies = []string{"kyidentity_csrf", "ky_csrf", "__Host-ky_csrf"}

// do sends one request as the page on that origin would: same Origin, its CSRF cookie
// echoed in X-CSRF-Token, a JSON body. It returns the response with the body read.
func (b *browser) do(method, rawURL string, body any, hdr map[string]string) (int, []byte) {
	b.t.Helper()
	res, out := b.send(method, rawURL, body, hdr)
	return res.StatusCode, out
}

func (b *browser) send(method, rawURL string, body any, hdr map[string]string) (*http.Response, []byte) {
	b.t.Helper()
	var r io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			b.t.Fatal(err)
		}
		r = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, rawURL, r)
	if err != nil {
		b.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != "GET" {
		req.Header.Set("Origin", req.URL.Scheme+"://"+req.URL.Host)
		for _, c := range b.jar.Cookies(req.URL) {
			for _, name := range csrfCookies {
				if c.Name == name {
					req.Header.Set("X-CSRF-Token", c.Value)
				}
			}
		}
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := b.hc.Do(req)
	if err != nil {
		b.t.Fatalf("%s %s: %v", method, rawURL, err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	return res, out
}

// must is do that fails the test unless the answer is want.
func (b *browser) must(want int, method, rawURL string, body any, hdr map[string]string) []byte {
	b.t.Helper()
	code, out := b.do(method, rawURL, body, hdr)
	if code != want {
		b.t.Fatalf("%s %s: %d, want %d: %s", method, rawURL, code, want, out)
	}
	return out
}

// signIn takes a CSRF token, then signs in at KyIdentity with a password alone (E8).
func (b *browser) signIn(username, password string) {
	b.t.Helper()
	b.must(200, "GET", issuer+"/api/auth/csrf", nil, nil)
	b.must(200, "POST", issuer+"/api/auth/login", map[string]string{"username": username, "password": password}, nil)
}

// stepUp re-proves the password for one exact method and path, as KyIdentity requires for
// every sensitive admin write, and returns the header that carries it.
func (b *browser) stepUp(password, method, path string) map[string]string {
	b.t.Helper()
	var ans struct {
		Kind  string `json:"kind"`
		Token string `json:"stepUpToken"`
	}
	payload := map[string]string{"password": password, "method": "", "operation": method + " " + path}
	var raw []byte
	for attempt := 0; attempt < 5; attempt++ {
		code, body := b.do("POST", issuer+"/api/auth/step-up", payload, nil)
		if code == http.StatusTooManyRequests {
			time.Sleep(5 * time.Second)
			continue
		}
		if code != http.StatusOK {
			b.t.Fatalf("POST %s/api/auth/step-up: %d, want 200: %s", issuer, code, body)
		}
		raw = body
		break
	}
	if err := json.Unmarshal(raw, &ans); err != nil || ans.Kind != "grant" || ans.Token == "" {
		b.t.Fatalf("step-up for %s %s: %s", method, path, raw)
	}
	return map[string]string{"X-KyIdentity-StepUp": ans.Token}
}

// admin is a step-up admin write at KyIdentity; rawURL is issuer + path.
func (b *browser) admin(password string, want int, method, rawURL string, body any) []byte {
	b.t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		b.t.Fatal(err)
	}
	return b.must(want, method, rawURL, body, b.stepUp(password, method, u.Path))
}
