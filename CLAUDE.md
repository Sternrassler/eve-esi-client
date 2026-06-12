# eve-esi-client

Library-only Go module — no service/proxy binary. Consumers import `pkg/client`;
`pkg/ratelimit`, `pkg/cache`, `pkg/pagination`, `pkg/logging` are also usable standalone.
Beispiele in `examples/`, Details in `docs/configuration.md` + `docs/troubleshooting.md`.

## Commands

- `make help` listet alle Targets. `make test` = `go test -v -race -coverprofile=coverage.out ./...`; `make lint` = `golangci-lint run ./...`.
- Integration tests (`tests/integration/`) starten Redis selbst via **testcontainers** → brauchen einen laufenden **Docker-Daemon** (kein `REDIS_URL` nötig). Nur ausführen: `go test ./tests/integration/...`. CI nutzt stattdessen einen Redis-Service-Container und setzt `REDIS_URL=localhost:6379`.
- Hooks einmalig aktivieren: `git config core.hooksPath .githooks`.
- Release: `make release VERSION=X.Y.Z` (prüft `[Unreleased]`, schreibt den Block ins `CHANGELOG.md`), dann manuell `git tag vX.Y.Z && git push origin main vX.Y.Z`.

## Gotchas

- Lint = golangci-lint **v2** (`.golangci.yml` is `version: "2"`); runs in CI + pre-commit hook. `golangci-lint config verify` is strict — use v2 keys (`linters.settings`, `linters.exclusions.rules`), not v1.
- Cache-expiry tests assume UTC: use `time.UTC` explicitly when constructing/comparing expiry times (see `pkg/cache/http_test.go`). The integration tests use relative `time.Now().Add(...)`, so they are locale-agnostic — there is **no** `TZ` env var anywhere.
- `.githooks/`: `commit-msg` erzwingt **Conventional Commits**; `pre-commit` führt go vet + golangci-lint (beide blockierend) aus. Der Secret-Scan dort ist nur eine **nicht-blockierende Warnung**, kein hartes Gate.
- SemVer source of truth = `CHANGELOG.md` + git tags (`vX.Y.Z`); there is no VERSION file.
