# Browser regressions

## Purpose

Exercise the built embedded frontend through Chromium with the real Go server and CSP.

## Ownership

This directory owns test setup, disposable server launch and UI assertions. The parent owns Playwright configuration and CI wiring.

## Local Contracts

- Never reuse a development or production server. Launch the compiled `.browser/server` with a minimal environment and an owned temporary data directory; remove that directory on exit.
- Use loopback only, with `KY_APP_URL` matching the test origin. Bootstrap credentials are disposable test values, not deployment defaults. Specs find the sign-in fields by their labels (`Username`, `Password`).
- Test light/dark at 390px and 1280px, using real authentication and API state. Do not disable service workers, relax CSP/CSRF, or substitute mocked responses.
- `server.mjs` runs `init-admin` (the server bootstraps its admin only into an empty database) and then `create-user` for the everyday user `walter`, both before the server starts; `setup.mjs` replaces both bootstrap passwords, each in its own cookie jar.
- `calendar.spec.mjs` signs in as `walter` and covers the calendar under the production CSP: the calendar list in the app sidebar at 1280px and behind the header's Calendars disclosure at 390px (one left column), create by form, Enter on the focused list event opens it, Escape closes the event dialog, delete (checked again after a reload), week view opening scrolled to 07:00 after a switch from month, the agent form's three access levels (Read only chosen), the AI agent setup's commands scrolling inside their own blocks, and no horizontal scroll.
- `resize.spec.mjs` signs in as `walter` (own `X-Forwarded-For`, 203.0.113.23), half-fills the rename and subscribe forms, then crosses 720px both ways: the same list node stays in the app sidebar with every draft, the phone disclosure closes on Escape (focus back on its summary) and reopens with the drafts, and an add-calendar draft survives the same trip. Nothing is saved.
- `groups.spec.mjs` signs in as `admin`, creates a group, adds `walter` by search, grants the group a new group calendar, then signs `walter` in through a separate browser context and finds the calendar in his sidebar. It sends its own `X-Forwarded-For` (the harness sets `KY_TRUSTED_PROXIES=127.0.0.1`) so its sign-ins do not spend the shared per-IP login limit (20 a minute) the other specs rely on; the limiter itself stays on.
- `people.spec.mjs` has `admin` add a person, reads the one-time password from the dialog, then signs that person in through a separate context, replaces the password and lands on the calendar. It uses its own `X-Forwarded-For` (203.0.113.21), distinct from `groups.spec.mjs`.
- `twofactor.spec.mjs` has `admin` add a person who replaces the password, opens Two-step verification, reads the setup key, fails with a wrong code, confirms with the previous 30-second step's code (computed in the spec, RFC 6238), saves the recovery codes, signs out, and signs in again through the code prompt with the current step's code. It uses its own `X-Forwarded-For` (203.0.113.22).
- Screenshots and failure traces live in ignored `test-results/` and CI artifacts, not production assets.

## Work Guidance

Prefer browser-native behavior and assertions over screenshot-only checks. A passing suite covers these workflows, not every page or a complete accessibility audit.

## Verification

Run `npm run test:browser` from `web/` after rebuilding the embedded frontend and server as documented in the parent.

## Child DOX Index

None.
