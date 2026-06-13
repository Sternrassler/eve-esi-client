# eve-esi-client

Library-only Go module — no service/proxy binary. Consumers import `pkg/client`;
`pkg/ratelimit`, `pkg/cache`, `pkg/pagination`, `pkg/logging` are also usable standalone.
Beispiele in `examples/`, Details in `docs/configuration.md` + `docs/troubleshooting.md`.

## Commands

- `make help` listet alle Targets. `make test` = `go test -v -race -coverprofile=coverage.out ./...`; `make lint` = `golangci-lint run ./...`.
- Integration tests (`tests/integration/`) starten Redis selbst via **testcontainers** → brauchen einen laufenden **Docker-Daemon** (kein `REDIS_URL` nötig). Nur ausführen: `go test ./tests/integration/...`. CI nutzt stattdessen einen Redis-Service-Container und setzt `REDIS_URL=localhost:6379`.
- Hooks einmalig aktivieren: `git config core.hooksPath .githooks`.
- Release: `make release VERSION=X.Y.Z` (prüft `[Unreleased]`, schreibt den Block ins `CHANGELOG.md`), dann manuell `git tag vX.Y.Z && git push origin main vX.Y.Z`.

## Architektur

- **Zwei Rate-Limit-Pfade koexistieren.** Legacy-Error-Limit-Tracker (`pkg/ratelimit/tracker.go`): zählt `X-ESI-Error-Limit-*`, 60s-ESI-Fenster, State-TTL via `staleStateTTLSeconds=120`. GroupTracker (`pkg/ratelimit/groups.go`, X-Ratelimit-* Rollout ab Okt 2025): Token-Bucket pro Gruppe×Consumer, Floating Window (`"3600/15m"`), Kosten 2xx=2 / 3xx=1 / 4xx=5 / 5xx=0, 429 ⇒ Retry-After. Endpoint→Gruppe wird aus `X-Ratelimit-Group`-Responses **gelernt** (Redis: `esi:ratelimit:group:` / `esi:ratelimit:endpoint:`).

## Gotchas

- **Fresh-Serve (v0.7.0):** frische Cache-Treffer (Expires in der Zukunft) werden direkt aus Redis serviert — GET-Cache-Lookup läuft **vor** allen Gates (kein 304-Roundtrip, kein Token, kein Gate-Pass). Cache ist strikt **GET-only** (Lookup + Write). Es werden keine `If-None-Match`-Header mehr gesendet ⇒ ein **304 von ESI ist fail-loud** (Protokollverletzung, klarer Fehler). ETags werden weiter gespeichert (Basis für künftige Stale-Revalidierung).
- **Rate-Limit-State ohne TTL drosselt dauerhaft.** Prod-Incident 2026-06-07: `errors_remaining=5` mit TTL -1 drosselte tagelang jeden Request. Das Lua-Skript `decrIfExists` (`tracker.go`) dekrementiert nur existierende Keys und setzt fehlenden TTLs eine Ablaufzeit. Bei „Requests werden grundlos gedrosselt" hier ansetzen.
- Lint = golangci-lint **v2** (`.golangci.yml` is `version: "2"`); runs in CI + pre-commit hook. `golangci-lint config verify` is strict — use v2 keys (`linters.settings`, `linters.exclusions.rules`), not v1.
- Cache-expiry tests assume UTC: use `time.UTC` explicitly when constructing/comparing expiry times (see `pkg/cache/http_test.go`). The integration tests use relative `time.Now().Add(...)`, so they are locale-agnostic — there is **no** `TZ` env var anywhere.
- `.githooks/`: `commit-msg` erzwingt **Conventional Commits**; `pre-commit` führt go vet + golangci-lint (beide blockierend) aus. Der Secret-Scan dort ist nur eine **nicht-blockierende Warnung**, kein hartes Gate.
- SemVer source of truth = `CHANGELOG.md` + git tags (`vX.Y.Z`); there is no VERSION file.
