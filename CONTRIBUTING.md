# Contributing to KyCalendar

[Product overview](README.md) · [Standalone installation](INSTALL.md)

## How to contribute

1. Report bugs and propose features in [GitHub issues](https://github.com/Busnes-app/KyCalendar/issues).
   Open an issue before a change larger than a bug fix or a documentation edit.
2. Report security problems privately, never in an issue: use **Report a vulnerability** on the
   repository's Security tab. The [Busnes-app security policy](https://github.com/Busnes-app/.github/blob/main/SECURITY.md)
   covers response times and disclosure.
3. Branch off `main`, make one logical change, and open a pull request against `main`. Use a
   Conventional Commit subject (`fix:`, `feat:`, `docs:`, `refactor:`, `test:`, `chore:`).
4. Every CI job in `.github/workflows/ci.yml` must pass before merge. Run the same checks locally
   with `make ci`; add `make test-postgres` when a PostgreSQL instance is available.
5. Read [AGENTS.md](AGENTS.md) for the server's contracts before changing them, and update it in the
   same change when a contract moves.

## Tests

New behaviour ships with a test that fails when the behaviour breaks. A security-relevant change
(sign-in, sessions, app passwords, authorization, CalDAV paths, outbound fetches, backups) also
needs a test for the attack path, not just the expected one. A new route needs a row in the
authorization matrix (`internal/api/authz_matrix_test.go`), which fails when a route has none.

## Coding standard

Contributions follow these style guides. CI enforces the parts a tool can check, so a style
failure is a red build, not a review comment.

| Language | Style guide | Enforced by |
|---|---|---|
| Go | [Effective Go](https://go.dev/doc/effective_go) and the [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments) | `gofmt` (must list nothing), `go vet ./...` and a tidy `go.mod` in the `Go build, vet & tests (SQLite)` job |
| TypeScript (`web/`) | The TypeScript compiler in `strict` mode with `noUnusedLocals` and `noUnusedParameters` | `tsc -b` in `npm run build` (`Frontend typecheck & build` job) |
| Shell (`scripts/`) | [ShellCheck](https://www.shellcheck.net/)'s rules | `shellcheck scripts/*.sh` in the `End-to-end smoke test` job |

Beyond what the tools check: validate at boundaries and trust types inside them (the engineering
principles at the top of [AGENTS.md](AGENTS.md)), and keep to the comment density and naming of
the file you are editing.

## Licence

KyCalendar is licensed under the [MIT License](LICENSE.txt). By contributing you agree your
contribution is licensed under the same terms.
