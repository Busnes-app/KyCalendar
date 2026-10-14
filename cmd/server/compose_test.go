package main

import (
	"os"
	"path/filepath"
	"regexp"
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
