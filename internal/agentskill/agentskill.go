// Package agentskill holds the SKILL.md that teaches AI agents (OpenClaw, Hermes Agent) to use
// KyCalendar's MCP tools. Each instance serves the copy matching its own tools.
package agentskill

import (
	"bytes"
	_ "embed"
	"strconv"
)

// KyCalendar is kycalendar/SKILL.md as committed; its frontmatter version is SourceVersion.
//
//go:embed kycalendar/SKILL.md
var KyCalendar []byte

// SourceVersion is the frontmatter version line as committed, which For replaces.
const SourceVersion = "\nversion: \"dev\"\n"

// For is the skill a build of version v serves at /agent/skills/kycalendar/SKILL.md: the
// committed text with v as its frontmatter version.
func For(v string) []byte {
	return bytes.Replace(KyCalendar, []byte(SourceVersion), []byte("\nversion: "+strconv.Quote(v)+"\n"), 1)
}
