//go:build kye2e

// Package suite drives the cross-app multi-tenant end-to-end run
// (docs/superpowers/specs/2026-11-24-multi-tenant-e2e-design.md). Run it with ./run.sh.
package suite

import (
	"bufio"
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"debug/buildinfo"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	composeFile = "compose/compose.yml"
	memBudget   = 5 << 30 // every container together (E3)
	// The staff org every app names (KyIdentity has no staff org yet: its tenancy build step 5).
	staffOrgID = "6f1c2e2e-0000-4000-8000-000000000001"
)

type app struct{ repo, sha string }

// target is one app in the smoke run: how its kye2e image is built and what each build says.
type target struct {
	name     string   // apps.lock name; images/<name>.Dockerfile
	bin      string   // the binary inside both images
	services []string // compose services that must start in multi mode
	ready    string   // GET on 127.0.0.1 inside each service whose body names readiness
	release  string   // the compose service running the release image
	refusal  string   // what the release build says to a complete multi configuration
	issuer   string   // the issuer variable, and what the kye2e build says to a non-.test one
	refuseIs string
}

var targets = []target{
	{"kycalendar", "/app/kycalendar", []string{"kycalendar-x1", "kycalendar-x2", "kycalendar-y"}, "http://127.0.0.1:8080/readyz",
		"kycalendar-release", "every requirement is met, but multi-tenant startup stays refused",
		"KY_KYSIGNON_ISSUER", "serves only an https issuer under .test"},
	{"kyidentity", "/usr/local/bin/kyidentity", []string{"kyidentity"}, "http://127.0.0.1:5867/readyz",
		"kyidentity-release", "KYIDENTITY_TENANCY=multi is not available in this release",
		"KYIDENTITY_ISSUER_URL", "KYIDENTITY_ISSUER_URL must be a host under .test"},
	{"kymessages", "/app/kymessages", []string{"kymessages"}, "",
		"kymessages-release", "KY_TENANCY_MODE=multi: multi-tenant mode is unavailable in this release",
		"KY_KYIDENTITY_ISSUER", "this test build accepts only an https issuer under .test"},
}

// The first check: each pinned kye2e image starts in multi mode against the .test issuer, the
// release image at the same commit refuses, and the test image refuses any other issuer.
func TestSmoke(t *testing.T) {
	s := start(t)
	for _, a := range targets {
		for _, svc := range a.services {
			if a.ready == "" {
				continue // healthy is all it reports (compose up --wait)
			}
			if out, err := s.compose("exec", "-T", svc, "wget", "-q", "-O", "-", a.ready); err != nil || !strings.Contains(out, `"ready"`) {
				t.Errorf("%s not ready: %v %s", svc, err, out)
			}
		}
		refuses(t, s.compose, a.refusal, "run", "--rm", "--no-deps", a.release)
		refuses(t, s.compose, a.refuseIs, "run", "--rm", "--no-deps", "-e", a.issuer+"=https://id.example", a.services[0])
	}
}

// suite is one running install: every pinned kye2e image in multi mode, KyIdentity's bootstrap
// administrator signed in, and each app's OIDC client registered.
type suite struct {
	t         *testing.T
	pins      map[string]app
	compose   func(...string) (string, error)
	web       *http.Transport // reaches every *.e2e.test name through front, trusting the run's CA
	adminPass string
	staff     *browser
}

// start builds the pinned images and brings the install up. The stack is removed when t ends.
func start(t *testing.T) *suite {
	s := &suite{t: t, pins: lock(t)}
	ca, cert, key := tlsFiles(t)
	adminPass := "Aa1!" + random(12)
	env := append(os.Environ(), "E2E_CA="+ca, "E2E_TLS_CERT="+cert, "E2E_TLS_KEY="+key,
		"E2E_PROJECT="+fmt.Sprintf("kye2e-%d-%s", os.Getpid(), strings.ToLower(t.Name())), "E2E_STAFF_ORG_ID="+staffOrgID,
		"E2E_SESSION_SECRET="+random(32), "E2E_ENCRYPTION_KEY="+random(32), "E2E_ADMIN_PASSWORD="+adminPass,
		"E2E_POSTGRES_PASSWORD="+random(16), "E2E_S3_SECRET="+random(16), "E2E_KYIDENTITY_SECRET_KEY="+random(32),
		"E2E_KYIDENTITY_RSA="+rsaKey(t), "E2E_KYIDENTITY_VAPID="+vapidKey(t), "E2E_KYMESSAGES_SEAL="+random(32))
	for _, a := range targets {
		pin, ok := s.pins[a.name]
		if !ok {
			t.Fatalf("apps.lock pins no %s", a.name)
		}
		up := strings.ToUpper(a.name)
		env = append(env, up+"_IMAGE=kye2e/"+a.name+":"+pin.sha, up+"_RELEASE_IMAGE=kye2e-release/"+a.name+":"+pin.sha)
	}
	s.compose = func(args ...string) (string, error) {
		// A one-off container that serves instead of refusing never exits on its own.
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "docker", append([]string{"compose", "-f", composeFile, "--profile", "release"}, args...)...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	checkMemory(t, s.compose)
	for _, a := range targets {
		pin := s.pins[a.name]
		img, rel := "kye2e/"+a.name+":"+pin.sha, "kye2e-release/"+a.name+":"+pin.sha
		build(t, img, "-f", "images/"+a.name+".Dockerfile", "--build-arg", "REPO="+pin.repo, "--build-arg", "SHA="+pin.sha, "images")
		build(t, rel, pin.repo+"#"+pin.sha) // the app's own Dockerfile: its release build
		if !hasTag(t, img, a.bin) || hasTag(t, rel, a.bin) {
			t.Fatalf("build tags: %s must carry kye2e, %s must not", img, rel)
		}
	}
	t.Cleanup(func() {
		if t.Failed() {
			out, _ := s.compose("logs", "--no-color")
			t.Log(out)
		}
		// The memory figures the spec's budget records.
		if ids, err := s.compose("ps", "-q"); err == nil && strings.TrimSpace(ids) != "" {
			args := append([]string{"stats", "--no-stream", "--format", "{{.Name}} {{.MemUsage}}"}, strings.Fields(ids)...)
			out, _ := exec.Command("docker", args...).CombinedOutput()
			t.Logf("memory:\n%s", out)
		}
		if out, err := s.compose("down", "-v", "--remove-orphans"); err != nil {
			t.Errorf("compose down: %v\n%s", err, out)
		}
	})

	recordMulti(t, s.compose)
	// KyMessages will not serve before its first sync with KyIdentity, which needs a client.
	if out, err := s.compose("up", "-d", "--wait", "--wait-timeout", "180", "kyidentity", "front", "mail"); err != nil {
		t.Fatalf("kye2e KyIdentity did not start in multi mode: %v\n%s", err, out)
	}
	s.web = frontTransport(t, ca, hostPort(t, s.compose, "front", "443"))
	s.staff = s.browser()
	s.adminPass = firstAdminSignIn(s.staff, adminPass)
	env = append(env,
		"E2E_KYMESSAGES_CLIENT_SECRET="+registerClient(s.staff, s.adminPass, "kymessages", "https://messages.e2e.test/api/sso/kyidentity/callback"),
		"E2E_KYCALENDAR_CLIENT_SECRET="+registerClient(s.staff, s.adminPass, "kycalendar",
			"https://cal-x.e2e.test/api/sso/kysignon/callback", "https://cal-y.e2e.test/api/sso/kysignon/callback"))
	// One client, two deployments: KyIdentity sends each org's members to its cell's origin.
	s.staff.admin(s.adminPass, 200, "PUT", issuer+"/api/admin/clients/kycalendar/regions",
		map[string]any{"regions": map[string]string{"e2e-1": "https://cal-x.e2e.test", "e2e-2": "https://cal-y.e2e.test"}})
	var services []string
	for _, a := range targets {
		services = append(services, a.services...)
	}
	if out, err := s.compose(append([]string{"up", "-d", "--wait", "--wait-timeout", "180"}, services...)...); err != nil {
		t.Fatalf("a kye2e image did not start in multi mode: %v\n%s", err, out)
	}
	return s
}

// hostPort is the loopback address compose published for a service's port.
func hostPort(t *testing.T, compose func(...string) (string, error), svc, port string) string {
	t.Helper()
	out, err := compose("port", svc, port)
	if err != nil {
		t.Fatalf("compose port %s %s: %v\n%s", svc, port, err, out)
	}
	lines := strings.Fields(out)
	return lines[len(lines)-1]
}

// firstAdminSignIn signs the bootstrap administrator in and makes the password change
// KyIdentity forces on it, returning the new password.
func firstAdminSignIn(b *browser, pass string) string {
	b.t.Helper()
	b.signIn("admin", pass)
	next := "Aa1!" + random(12)
	b.admin(pass, 200, "POST", issuer+"/api/user/password", map[string]string{"newPassword": next})
	b.signIn("admin", next)
	return next
}

// registerClient registers a confidential OIDC client and returns its secret, shown once.
func registerClient(b *browser, adminPass, id string, redirects ...string) string {
	b.t.Helper()
	const path = "/api/admin/clients"
	code, raw := b.do("POST", issuer+path, map[string]any{"clientId": id, "clientName": id, "clientType": "confidential",
		"redirectUris": redirects}, b.stepUp(adminPass, "POST", path))
	var ans struct {
		Secret string `json:"clientSecret"`
	}
	if err := json.Unmarshal(raw, &ans); code != 200 || err != nil || ans.Secret == "" {
		b.t.Fatalf("client %s: %d and no secret", id, code) // the body holds the secret: never logged
	}
	return ans.Secret
}

// tlsFiles makes the run's CA and one certificate for the .test names front and the mail sink serve.
func tlsFiles(t *testing.T) (ca, cert, key string) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "kye2e run CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "id.e2e.test"},
		DNSNames: []string{"id.e2e.test", "cal-x.e2e.test", "cal-y.e2e.test", "messages.e2e.test", "smtp.e2e.test"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, caTmpl, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		t.Fatal(err)
	}
	enc := func(typ string, b []byte) string { return string(pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: b})) }
	return enc("CERTIFICATE", caDER), enc("CERTIFICATE", leafDER), enc("PRIVATE KEY", keyDER)
}

// recordMulti works around a KyIdentity defect: its schema records tenancy_mode 'single' on
// every new database and nothing records 'multi', so a multi-tenant start always refuses
// ("this database was created single-tenant"). It runs KyIdentity's own migrations through
// its CLI, then sets the one row. It fails once KyIdentity records the mode itself.
func recordMulti(t *testing.T, compose func(...string) (string, error)) {
	t.Helper()
	if out, err := compose("up", "-d", "--wait", "postgres"); err != nil {
		t.Fatalf("postgres: %v\n%s", err, out)
	}
	if out, err := compose("run", "--rm", "--no-deps", "kyidentity", "platform-token", "list"); err != nil {
		t.Fatalf("kyidentity migrations: %v\n%s", err, out)
	}
	psql := func(sql string) string {
		out, err := compose("exec", "-T", "postgres", "psql", "-U", "postgres", "-d", "kyidentity", "-tAc", sql)
		if err != nil {
			t.Fatalf("psql: %v\n%s", err, out)
		}
		return strings.TrimSpace(out)
	}
	switch mode := psql("SELECT value FROM system_settings WHERE key='tenancy_mode'"); mode {
	case "single":
		psql("UPDATE system_settings SET value='multi' WHERE key='tenancy_mode'")
	case "multi":
		t.Fatal("KyIdentity now records tenancy_mode multi itself: delete recordMulti")
	default:
		t.Fatalf("kyidentity tenancy_mode %q", mode)
	}
}

// lock reads apps.lock: <app> <repository> <full SHA> per line.
func lock(t *testing.T) map[string]app {
	t.Helper()
	f, err := os.Open("apps.lock")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	apps := map[string]app{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 || len(fields[2]) != 40 || strings.Trim(fields[2], "0123456789abcdef") != "" || !strings.HasPrefix(fields[1], "https://github.com/Busnes-app/") {
			t.Fatalf("apps.lock: %q is not <app> https://github.com/Busnes-app/<repo>.git <full SHA>", line)
		}
		apps[fields[0]] = app{fields[1], fields[2]}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return apps
}

// build makes a local image; E2E_BUILDER names a buildx builder (on Major, one with a memory cap).
func build(t *testing.T, tag string, args ...string) {
	t.Helper()
	cmd := []string{"buildx", "build", "--load", "-t", tag}
	if b := os.Getenv("E2E_BUILDER"); b != "" {
		cmd = append(cmd, "--builder", b)
	}
	if out, err := exec.Command("docker", append(cmd, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", tag, err, out)
	}
}

// hasTag reports whether the image's binary bin was built with -tags kye2e.
func hasTag(t *testing.T, image, bin string) bool {
	t.Helper()
	id, err := exec.Command("docker", "create", image).Output()
	if err != nil {
		t.Fatalf("docker create %s: %v", image, err)
	}
	defer exec.Command("docker", "rm", strings.TrimSpace(string(id))).Run()
	local := filepath.Join(t.TempDir(), "bin")
	if out, err := exec.Command("docker", "cp", strings.TrimSpace(string(id))+":"+bin, local).CombinedOutput(); err != nil {
		t.Fatalf("docker cp %s: %v %s", image, err, out)
	}
	info, err := buildinfo.ReadFile(local)
	if err != nil {
		t.Fatalf("%s: %v", image, err)
	}
	for _, s := range info.Settings {
		if s.Key == "-tags" && strings.Contains(","+s.Value+",", ",kye2e,") {
			return true
		}
	}
	return false
}

// checkMemory refuses a service without a memory limit (swap included) or a total over memBudget.
func checkMemory(t *testing.T, compose func(...string) (string, error)) {
	t.Helper()
	out, err := compose("config", "--format", "json")
	if err != nil {
		t.Fatalf("compose config: %v\n%s", err, out)
	}
	var cfg struct {
		Services map[string]struct {
			Mem  json.RawMessage `json:"mem_limit"`
			Swap json.RawMessage `json:"memswap_limit"`
		} `json:"services"`
	}
	if i := strings.Index(out, "{"); i > 0 {
		out = out[i:] // past any warning compose printed first
	}
	if err := json.Unmarshal([]byte(out), &cfg); err != nil {
		t.Fatal(err)
	}
	var total int64
	for name, s := range cfg.Services {
		mem, _ := strconv.ParseInt(strings.Trim(string(s.Mem), `"`), 10, 64)
		swap, _ := strconv.ParseInt(strings.Trim(string(s.Swap), `"`), 10, 64)
		if mem <= 0 || swap != mem {
			t.Errorf("service %s: mem_limit %s and memswap_limit %s must be set and equal", name, s.Mem, s.Swap)
		}
		total += mem
	}
	if total > memBudget {
		t.Errorf("containers total %d bytes, over the %d budget", total, int64(memBudget))
	}
	if t.Failed() {
		t.FailNow() // before anything starts
	}
}

// refuses runs a one-off container that must exit non-zero saying want.
func refuses(t *testing.T, compose func(...string) (string, error), want string, args ...string) {
	t.Helper()
	if out, err := compose(args...); err == nil || !strings.Contains(out, want) {
		t.Errorf("%v: want a refusal saying %q, got %v\n%s", args, want, err, out)
	}
}

func random(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// rsaKey is KyIdentity's signing key, PKCS#1 PEM.
func rsaKey(t *testing.T) string {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}))
}

// vapidKey is KyIdentity's Web Push key: the raw P-256 scalar, base64url without padding.
func vapidKey(t *testing.T) string {
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(k.Bytes())
}
