# eve-esi-client

Library-only Go module — no service/proxy binary. Consumers import `pkg/client`;
`pkg/ratelimit`, `pkg/cache`, `pkg/pagination` are also usable standalone.

## Gotchas

- Lint = golangci-lint **v2** (`.golangci.yml` is `version: "2"`); runs in CI (Test job) + pre-commit hook. `golangci-lint config verify` is strict — use v2 keys (`linters.settings`, `linters.exclusions.rules`), not v1.
- Run integration tests under UTC: `TZ=UTC go test ./...`. `tests/integration` cache-expiry tests fail on non-UTC locales (e.g. CEST); CI runs UTC.
- `.githooks/` (`git config core.hooksPath .githooks`) runs go vet + golangci-lint + secret scan + Conventional-Commit lint → commit messages MUST be Conventional Commits.
- SemVer source of truth = `CHANGELOG.md` + git tags (`vX.Y.Z`); there is no VERSION file.
