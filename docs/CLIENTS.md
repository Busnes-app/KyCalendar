# Clients

[Product overview](../README.md) · [Standalone installation](../INSTALL.md)

Commands and unlinked file paths below are relative to the repository root.

## Connect a phone

Native clients need the server on HTTPS. With an `https://` `KY_APP_URL`, session cookies are
`Secure` and HSTS is sent; `KY_COOKIE_SECURE=false` overrides that for local testing only. Sign in as an everyday user (administrators are refused
on CalDAV), open **Settings** (the gear beside Sign out) → **Phones & apps**, name the device and create an app password. The page shows
the server, your user name and the password once; revoke it there at any time.

- iPhone and iPad: Settings > Calendar > Accounts > Add Account > Other > Add CalDAV Account.
- Android: DAVx5 > Add account > Login with URL and user name, URL `https://<your host>/`.
- Thunderbird: New Calendar > On the Network, location `https://<your host>/`.

Enter the app password where the client asks for a password. Per-user caps are in
[Calendar limits](ACCOUNTS-AND-GROUPS.md#calendar-limits).

### Tasks

Task lists are CalDAV calendars holding to-dos (VTODO), so the same tasks show in the web
Tasks screen and in your apps. On Android, add the account in DAVx5 and open the task lists in
Tasks.org or jtx Board. Apple Reminders and Thunderbird pick them up from the same account.
Priority, status, due and start dates, notes, tags, subtasks, repeats and reminders are standard
properties, and edits in the web keep everything an app wrote that the web does not show.
Areas, saved views, estimates and habits exist only in KyCalendar; apps see ordinary lists and
tasks.

## Connect an AI agent

AI agents such as OpenClaw and Hermes Agent reach your personal calendars over MCP at
`https://<your host>/mcp`. On **Settings** → **Phones & apps**, choose **AI agent** and an access level:

- **Read only** (the default): the agent sees your schedule, your tasks and the free gaps in a
  day (busy times only, no titles).
- **Read and write**: it can also add events and tasks and change, complete or place in the day
  the ones it created. The switch **Can also change events it did not create** lets it change
  any of your events and tasks.

No agent can delete events, tasks or lists, or see group calendars and group task lists. The page shows the token once, with config
for both agents that reads it from `KYCAL_TOKEN`, and the command to install the KyCalendar
skill (served at `/agent/skills/kycalendar/SKILL.md`), which teaches the agent the tools and
when to ask you first. You can change an agent's access there later. An agent token unused for
90 days is revoked; an agent token never works over CalDAV, nor a phone's password over MCP.

Event text can come from other people, and an agent may treat it as instructions. Give an
agent write access only if you trust everyone who can message it.
