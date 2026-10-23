// Package appversion is the one answer to "which KyCalendar is this": the version command, the
// capsule manifest, backup status, the Settings screen, the MCP server info and the agent
// skill all read String.
//
// VERSION holds the release line, X.Y.Z, and is the only file a human bumps. Automated
// dependency releases add a fourth number (X.Y.Z.N) without a commit: release-image.yml passes
// the release tag's version to the build, which sets release through -ldflags.
package appversion

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var line string

// release is set at link time, without the tag's "v":
//
//	go build -ldflags "-X github.com/Busnes-app/kycalendar/internal/appversion.release=0.4.3.1"
//
// The Dockerfile (VERSION build arg), the Makefile and ci.yml pass it: the release tag in
// release-image.yml, a git describe elsewhere. Left empty, the build reports the line.
var release string

// String is the version this build reports.
func String() string {
	if release != "" {
		return release
	}
	return strings.TrimSpace(line)
}
