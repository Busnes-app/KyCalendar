// Package agentskill holds the SKILL.md that teaches AI agents (OpenClaw, Hermes Agent) to use
// KyCalendar's MCP tools. Each instance serves the copy matching its own tools.
package agentskill

import _ "embed"

// KyCalendar is kycalendar/SKILL.md, served at /agent/skills/kycalendar/SKILL.md.
//
//go:embed kycalendar/SKILL.md
var KyCalendar []byte
