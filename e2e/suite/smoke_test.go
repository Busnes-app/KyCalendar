//go:build kye2e

// Package suite drives the cross-app multi-tenant end-to-end run
// (docs/superpowers/specs/2026-11-24-multi-tenant-e2e-design.md). Run it with ./run.sh.
package suite

import (
	"bufio"
	"context"
	"crypto/rand"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	gateRefusal = "every requirement is met, but multi-tenant startup stays refused"
	issuerOnly  = "serves only an https issuer under .test"
)

type app struct{ repo, sha string }

// The first check: each pinned kye2e image starts in multi mode against the .test issuer, the
// release image at the same commit refuses, and the test image refuses any other issuer.
func TestSmoke(t *testing.T) {
	cal, ok := lock(t)["kycalendar"]
	if !ok {
		t.Fatal("apps.lock pins no kycalendar")
	}
	img, rel := "kye2e/kycalendar:"+cal.sha, "kye2e-release/kycalendar:"+cal.sha
	project := fmt.Sprintf("kye2e-%d", os.Getpid())
	env := append(os.Environ(), "E2E_PROJECT="+project, "KYCALENDAR_IMAGE="+img, "KYCALENDAR_RELEASE_IMAGE="+rel,
		"E2E_SESSION_SECRET="+random(32), "E2E_ENCRYPTION_KEY="+random(32), "E2E_ADMIN_PASSWORD=Aa1!"+random(12),
		"E2E_POSTGRES_PASSWORD="+random(16), "E2E_S3_SECRET="+random(16))
	compose := func(args ...string) (string, error) {
		// A one-off container that serves instead of refusing never exits on its own.
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "docker", append([]string{"compose", "-f", composeFile, "--profile", "release"}, args...)...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	checkMemory(t, compose)
	build(t, img, "-f", "images/kycalendar.Dockerfile", "--build-arg", "REPO="+cal.repo, "--build-arg", "SHA="+cal.sha, "images")
	build(t, rel, cal.repo+"#"+cal.sha) // the app's own Dockerfile: its release build
	if !hasTag(t, img) || hasTag(t, rel) {
		t.Fatalf("build tags: %s must carry kye2e, %s must not", img, rel)
	}
	t.Cleanup(func() {
		if t.Failed() {
			out, _ := compose("logs", "--no-color")
			t.Log(out)
		}
		// The memory figures the spec's budget records.
		if ids, err := compose("ps", "-q"); err == nil && strings.TrimSpace(ids) != "" {
			args := append([]string{"stats", "--no-stream", "--format", "{{.Name}} {{.MemUsage}}"}, strings.Fields(ids)...)
			out, _ := exec.Command("docker", args...).CombinedOutput()
			t.Logf("memory:\n%s", out)
		}
		if out, err := compose("down", "-v", "--remove-orphans"); err != nil {
			t.Errorf("compose down: %v\n%s", err, out)
		}
	})

	if out, err := compose("up", "-d", "--wait", "--wait-timeout", "180", "kycalendar-x1", "kycalendar-x2", "kycalendar-y"); err != nil {
		t.Fatalf("kye2e KyCalendar did not start in multi mode: %v\n%s", err, out)
	}
	for _, svc := range []string{"kycalendar-x1", "kycalendar-x2", "kycalendar-y"} {
		if out, err := compose("exec", "-T", svc, "wget", "-q", "-O", "-", "http://127.0.0.1:8080/readyz"); err != nil || !strings.Contains(out, `"ready"`) {
			t.Errorf("%s not ready: %v %s", svc, err, out)
		}
	}
	refuses(t, compose, gateRefusal, "run", "--rm", "--no-deps", "kycalendar-release")
	refuses(t, compose, issuerOnly, "run", "--rm", "--no-deps", "-e", "KY_KYSIGNON_ISSUER=https://id.example", "kycalendar-x1")
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

// hasTag reports whether the image's /app/kycalendar was built with -tags kye2e.
func hasTag(t *testing.T, image string) bool {
	t.Helper()
	id, err := exec.Command("docker", "create", image).Output()
	if err != nil {
		t.Fatalf("docker create %s: %v", image, err)
	}
	defer exec.Command("docker", "rm", strings.TrimSpace(string(id))).Run()
	bin := filepath.Join(t.TempDir(), "bin")
	if out, err := exec.Command("docker", "cp", strings.TrimSpace(string(id))+":/app/kycalendar", bin).CombinedOutput(); err != nil {
		t.Fatalf("docker cp: %v %s", err, out)
	}
	info, err := buildinfo.ReadFile(bin)
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
