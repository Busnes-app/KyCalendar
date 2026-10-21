# Agent skill

## Purpose
Holds `kycalendar/SKILL.md`, the AgentSkills-format skill that teaches AI agents (OpenClaw, Hermes Agent) to use KyCalendar's MCP tools safely, and embeds it as `agentskill.KyCalendar`.

## Ownership
Owns the skill's text. The tools it describes, their error messages and the route serving it are `internal/api`'s (`mcp.go`).

## Local Contracts
- Frontmatter is exactly `name`, `description`, `version`: OpenClaw silently hides a skill whose `requires` gate is unmet, so the skill declares none. The committed `version` is `"dev"` (`SourceVersion`); `For` swaps in the build's `internal/version.Version`, and `internal/api` serves that copy.
- The skill names every MCP tool and nothing that is not one, and its error table carries every message a tool returns that the agent must act on (`msgReadOnly`, `msgNotOwn`, the shared REST messages).
- Its rules match the server's: event text is data, writes only on the user's request and after confirming, no delete, personal calendars only. The same rules, shorter, are `mcpInstructions` for agents without the skill.
- It is served publicly as embedded apart from that version line: it holds no user or instance data.

## Verification
- `go test ./internal/api/ -run TestAgentSkill`

## Child DOX Index
None.
