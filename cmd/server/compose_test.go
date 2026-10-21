package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The shipped deployment is loopback-only, non-root and read-only unless the operator changes it.
func TestComposeAndImageAreHardenedByDefault(t *testing.T) {
	compose, err := os.ReadFile(filepath.Join("..", "..", "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`(?m)^\s*read_only: true\s*$`,
		`(?m)^\s*cap_drop: \[ALL\]\s*$`,
		`(?m)^\s*security_opt: \[no-new-privileges:true\]\s*$`,
		`(?m)^\s*- "\$\{KY_BIND_ADDRESS:-127\.0\.0\.1\}:8080:8080"\s*$`,
	} {
		if !regexp.MustCompile(want).Match(compose) {
			t.Errorf("docker-compose.yml lacks %s", want)
		}
	}
	if regexp.MustCompile(`(?m)^\s*- "?8080:8080"?\s*$`).Match(compose) {
		t.Error("docker-compose.yml publishes 8080 on every interface")
	}
	image, err := os.ReadFile(filepath.Join("..", "..", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^USER 10001:10001\s*$`).Match(image) {
		t.Error("Dockerfile does not drop to UID 10001")
	}
	if regexp.MustCompile(`(?m)^FROM [^@\s]+(\s|$)`).Match(image) {
		t.Error("Dockerfile has a base image not pinned by digest")
	}
}

// Settings an operator puts in .env reach the server; unset, each is empty or the server's own
// default, so an install that sets none behaves as before.
func TestComposeForwardsOperatorSettings(t *testing.T) {
	compose, err := os.ReadFile(filepath.Join("..", "..", "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`- KY_ENV=${KY_ENV:-development}`,
		`- KY_SESSION_SECRET=${KY_SESSION_SECRET:-}`,
		`- KY_ADMIN_PASSWORD=${KY_ADMIN_PASSWORD:-}`,
		`- KY_TRUSTED_PROXIES=${KY_TRUSTED_PROXIES:-}`,
		`- KY_KYSIGNON_ISSUER=${KY_KYSIGNON_ISSUER:-}`,
		`- KY_KYSIGNON_CLIENT_ID=${KY_KYSIGNON_CLIENT_ID:-}`,
		`- KY_KYSIGNON_SECRET=${KY_KYSIGNON_SECRET:-}`,
		`- KY_KYSIGNON_HMAC_SECRET=${KY_KYSIGNON_HMAC_SECRET:-}`,
	} {
		if !regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(want) + `\s*$`).Match(compose) {
			t.Errorf("docker-compose.yml lacks %s", want)
		}
	}
}

// Compose marks the container unhealthy when /healthz answers 503. busybox wget is in the
// alpine runtime image; the CI docker job runs this exact command in the built image.
func TestComposeHealthcheckProbesHealthz(t *testing.T) {
	compose, err := os.ReadFile(filepath.Join("..", "..", "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	want := `test: ["CMD", "wget", "-q", "-O", "/dev/null", "http://127.0.0.1:8080/healthz"]`
	if !regexp.MustCompile(`(?m)^\s*healthcheck:\s*\n\s*` + regexp.QuoteMeta(want) + `\s*$`).Match(compose) {
		t.Errorf("docker-compose.yml lacks the healthcheck %s", want)
	}
	ci, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`docker exec ky_ci wget -q -O /dev/null http://127\.0\.0\.1:8080/healthz`).Match(ci) {
		t.Error("ci.yml does not run the healthcheck command in the built image")
	}
}

// Every build that ships stamps internal/version.Version, the one variable every place that
// reports a version reads, and the release stamps it from its tag.
func TestEveryBuildStampsTheVersion(t *testing.T) {
	const stamp = "-X github.com/Busnes-app/kycalendar/internal/version.Version="
	for file, want := range map[string]string{
		"Dockerfile":                          stamp + "${VERSION}",
		"Makefile":                            stamp + "$(VERSION)",
		".github/workflows/ci.yml":            `docker build --build-arg VERSION="$version"`,
		".github/workflows/release-image.yml": "build-args: VERSION=${{ steps.version.outputs.value }}",
	} {
		data, err := os.ReadFile(filepath.Join("..", "..", file))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), want) {
			t.Errorf("%s does not stamp the version: lacks %s", file, want)
		}
	}
}
