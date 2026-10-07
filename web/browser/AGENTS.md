# Browser regressions

## Purpose

Exercise the built embedded frontend through Chromium with the real Go server and CSP.

## Ownership

This directory owns test setup, disposable server launch and UI assertions. The parent owns Playwright configuration and CI wiring.

## Local Contracts

- Never reuse a development or production server. Launch the compiled `.browser/server` with a minimal environment and an owned temporary data directory; remove that directory on exit.
- Use loopback only, with `KY_APP_URL` matching the test origin. Bootstrap credentials are disposable test values, not deployment defaults.
- Test light/dark at 390px and 1280px, using real authentication and API state. Do not disable service workers, relax CSP/CSRF, or substitute mocked responses.
- `server.mjs` runs `init-admin` (the server bootstraps its admin only into an empty database) and then `create-user` for the everyday user `walter`, both before the server starts; `setup.mjs` replaces both bootstrap passwords, each in its own cookie jar.
- `calendar.spec.mjs` signs in as `walter` and covers the calendar under the production CSP: create by form, Enter on the focused list event opens it, Escape closes the event dialog, delete (checked again after a reload), no horizontal scroll.
- `groups.spec.mjs` signs in as `admin`, creates a group, adds `walter` by search, grants the group a new group calendar, then signs `walter` in through a separate browser context and finds the calendar in his sidebar. It sends its own `X-Forwarded-For` (the harness sets `KY_TRUSTED_PROXIES=127.0.0.1`) so its sign-ins do not spend the shared per-IP login limit (20 a minute) the other specs rely on; the limiter itself stays on.
- Screenshots and failure traces live in ignored `test-results/` and CI artifacts, not production assets.

## Work Guidance

Prefer browser-native behavior and assertions over screenshot-only checks. A passing suite covers these workflows, not every page or a complete accessibility audit.

## Verification

Run `npm run test:browser` from `web/` after rebuilding the embedded frontend and server as documented in the parent.

## Child DOX Index

None.
