# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- **Stale Rate-Limit-State drosselte dauerhaft (Prod-Incident 2026-06-07).** ESI liefert die `X-ESI-Error-Limit-*`-Header seit der `X-Ratelimit-*`-Migration nicht mehr — der in Redis gespeicherte `errors_remaining`-Wert konnte dadurch nie wieder steigen, wurde aber von jedem header-losen Fehler (`RecordError`) dekrementiert und lag mit **TTL -1** dauerhaft vor. Folge: ein einmal niedriger Wert (< 20) drosselte JEDEN Request (inkl. Cache-Hits) mit 1 s Sleep, tagelang und über Deploys hinweg; unter 5 hätte er alle Requests geblockt. Drei Schichten Fix: (1) `GetState` behandelt einen State mit **abgelaufenem Reset-Fenster als healthy** (`RateLimitState.IsExpired()` — EVE resettet das Budget alle 60 s); (2) `UpdateFromHeaders` schreibt alle drei State-Keys mit **TTL** (Fenster + 60 s Puffer); (3) das `RecordError`-Lua-Skript versieht TTL-lose Legacy-Keys mit einer 120-s-Ablaufzeit. Vergifteter Bestand heilt sich damit selbst.

## [0.5.0] - 2026-05-31

### Removed
- **Prometheus metrics feature** — `pkg/metrics`, the promauto instrumentation across `pkg/cache`, `pkg/client`, `pkg/ratelimit`, the `/metrics` endpoint, monitoring docs, and the `prometheus/client_golang` dependency. Observability now relies on structured logging.
- **Architecture Decision Records** and their enforcement (pre-commit/CI ADR checks, `check-adr*` scripts, ADR policy in copilot-instructions).
- **HTTP service-mode proxy** (`cmd/esi-proxy`) — removed entirely, including the Makefile `build`/`run`/`docker-*` targets and container-image references. This is now a library-only module; use `pkg/client` directly.
- **`VERSION` file** and `scripts/common/check-version-changelog.sh` — `CHANGELOG.md` plus Git tags (`vX.Y.Z`) are the SemVer source of truth.
- **GitHub Copilot governance artefacts** — `.github/copilot-instructions.md`, `scripts/common/check-normative.sh` (and its pre-commit step), and the Copilot-bot skip branch in the commit-message check. Commit-message and security checks otherwise remain.

### Changed
- README slimmed to a single Quick Start snippet; all other code moved to runnable `examples/`. Docs pruned to `configuration.md` + `troubleshooting.md`.
- Pagination (`pkg/pagination`) documented accurately: tested and usable standalone, not yet wired into the client's high-level API.

### Added
- `examples/ratelimit-usage/` — standalone rate-limit tracker example.
- `examples/pagination-usage/` — parallel batch-fetch example.
- Unit tests for `pkg/pagination` (batch fetcher: single/multi-page, partial failure, config clamping, context cancellation).

### Fixed
- **Silent fallbacks made fail-loud (#27).** Errors that were previously swallowed behind degraded defaults are now propagated or logged: the retry path returns a clear error instead of silently reusing a consumed body when `GetBody` fails; a malformed (present-but-unparsable) `max-age`/`Expires` cache header returns `ErrMalformedCacheHeader` instead of silently substituting the default TTL (an absent header still legitimately uses the default); the rate-limit tracker returns `ErrIncompleteState` on a partial Redis read instead of assuming a healthy default; cache delete-on-expiry and response-body `Close()` errors are no longer discarded silently.
- CI builds on Go 1.25 and runs `golangci-lint` v2 (matching `.golangci.yml`); fixed all lint findings and `gofmt`.

## [0.4.0] - 2026-05-25

### Added
- Per-second rate limiter and concurrency semaphore enforcement.
- Cache-Control handling (`no-store` / `max-age`).

### Fixed
- Atomic error-budget decrement with context-aware throttling.
- Auth-aware cache key (hashes the `Authorization` header).
- Retry resends the request body via `GetBody`.
- Pagination `FetchPage` appends the `page` param without dropping existing query values.

## [0.3.0] - 2025-11-04

### Added
- Parallel `BatchFetcher` with worker pool for paginated ESI endpoints (`pkg/pagination`).

## [0.2.0] - 2025-10-27

### Added
- **ESI Client Core** (`pkg/client/`)
  - Complete ESI client with integrated rate limiting, caching, and error handling
  - Automatic rate limit checking before requests
  - Redis-backed response caching with ETag support
  - Intelligent retry logic with exponential backoff (5xx, 520, network errors)
  - No retry for 4xx client errors (protects error budget)
  - Error classification: client, server, rate_limit, network
  - Context support with timeout handling
  - Prometheus metrics integration
  - 8 comprehensive integration tests
  
- **Mock ESI Server** (`internal/testutil/mock_esi.go`)
  - Controllable mock server for testing
  - Supports all ESI response types (200, 304, 429, 500, 520)
  - Request tracking and conditional request support
  - Configurable delays and custom handlers
  
- **Integration Tests** (`tests/integration/`)
  - Full request flow (Rate Limit → Cache → ESI → Cache Update)
  - Cache hit and 304 Not Modified tests
  - Rate limit blocking verification
  - Retry logic validation (5xx retries, 4xx no retry)
  - Cache expiration handling
  - All tests passing with Redis test containers
  
- **Library Usage Example** (`examples/library-usage/`)
  - Realistic market orders fetching example
  - Error handling demonstration
  - Caching behavior showcase
  - Complete README with best practices

- **Comprehensive Documentation**
  - `docs/getting-started.md` - Quick start guide with examples
  - `docs/configuration.md` - Complete configuration reference
  - `docs/monitoring.md` - Prometheus metrics, logging, alerting, dashboards
  - `docs/troubleshooting.md` - Common issues and debugging guide
  
- **Rate Limit Tracker** (`pkg/ratelimit/`) - *(from v0.1.0)*
  - Three-tier threshold system (5/20/50 errors remaining)
  - Redis-backed state persistence across instances
  - Prometheus metrics: `esi_errors_remaining`, `esi_rate_limit_blocks_total`, `esi_rate_limit_throttles_total`
  - Structured logging with Zerolog
  - Integration tests with testcontainers-go
  - 87.5% test coverage
  
- **Cache Manager** (`pkg/cache/`) - *(from v0.1.0)*
  - Immutable cache entry model with TTL handling
  - Deterministic cache key generation (sorted parameters)
  - Redis-backed cache storage
  - HTTP integration: Expires header parsing, ETag/Last-Modified support
  - 304 Not Modified conditional request handling
  - Prometheus metrics: cache hits/misses, size, 304 responses, errors
  - Response to entry conversion with body restoration
  - 85.6% test coverage

- **Retry Logic** (`pkg/client/retry.go`)
  - Configurable retry with exponential backoff
  - Error class-based retry strategies
  - Jitter to prevent thundering herd
  - Context cancellation support
  - Prometheus metrics for retry attempts and exhaustion

- **Metrics System** (`pkg/metrics/`)
  - Request metrics (total, duration, errors)
  - Retry metrics (attempts, backoff, exhaustion)
  - 8+ Prometheus metrics total
  - Histogram buckets optimized for ESI latency

### Changed
- README updated with complete library usage examples
- README updated with rate limiting and error handling sections
- Dependencies: Added prometheus/client_golang v1.23.2, rs/zerolog v1.34.0, testcontainers-go v0.39.0
- Improved test structure with dedicated integration test directory

### Fixed
- Test timezone issue in cache HTTP parser (UTC conversion for http.TimeFormat compliance)
- Self-assignment warning in testTransport
- Cache entry body restoration for 304 Not Modified responses

### Documentation
- Complete getting started guide
- Detailed configuration reference
- Monitoring and observability guide with Prometheus queries
- Troubleshooting guide with common issues and solutions
- Library usage example with README
- All ADRs (005-008) available in docs/adr/

### Performance
- Efficient caching reduces ESI load by >60% (typical)
- P95 request latency < 1s with cache
- Automatic rate limit protection prevents IP bans
- Concurrent request support with configurable limits

### ESI Compliance
- ✅ Error rate limiting with automatic blocking
- ✅ Cache respects Expires header (required)
- ✅ Conditional requests with If-None-Match (ETag)
- ✅ User-Agent header (required format enforced)
- ✅ Spread load with rate limiting

## [0.1.0] - 2025-10-15

### Added
- Initial project structure
- Architecture Decision Records (ADR-005 to ADR-009)
- Go module setup
- README with architecture overview
- Basic rate limiter and cache manager
- Prometheus metrics foundation

[Unreleased]: https://github.com/Sternrassler/eve-esi-client/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/Sternrassler/eve-esi-client/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/Sternrassler/eve-esi-client/releases/tag/v0.1.0
