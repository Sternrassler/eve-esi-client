# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
