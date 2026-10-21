// Package version is the one answer to "which KyCalendar is this": the version command, the
// capsule manifest, backup status, the Settings screen, the MCP server info and the agent
// skill all read Version.
package version

import (
	"runtime/debug"
	"strings"
)

// Version is set at link time from the release tag, without its "v":
//
//	go build -ldflags "-X github.com/Busnes-app/kycalendar/internal/version.Version=0.5.0"
//
// The Dockerfile, the Makefile and the release workflow pass it. Left empty, it is the module
// version the go command stamped from the checkout, else "dev": a build straight from source
// says so instead of claiming a release.
var Version string

func init() {
	if Version == "" {
		Version = fromBuildInfo()
	}
}

func fromBuildInfo() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return strings.TrimPrefix(bi.Main.Version, "v")
	}
	return "dev"
}
