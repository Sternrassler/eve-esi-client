# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Rate limit tracker with ESI error limit monitoring (`pkg/ratelimit/`)
  - Three-tier threshold system (5/20/50 errors remaining)
  - Redis-backed state persistence across instances
  - Prometheus metrics: `esi_errors_remaining`, `esi_rate_limit_blocks_total`, `esi_rate_limit_throttles_total`
  - Structured logging with Zerolog
  - Integration tests with testcontainers-go
  - 89.5% test coverage
- Cache manager with ESI compliance (`pkg/cache/`)
  - Immutable cache entry model with TTL handling
  - Deterministic cache key generation (sorted parameters)
  - Redis-backed cache storage
  - HTTP integration: Expires header parsing, ETag/Last-Modified support
  - 304 Not Modified conditional request handling
  - Prometheus metrics: cache hits/misses, size, 304 responses, errors
  - Usage examples in `examples/cache-usage/`
  - 85.6% test coverage
- Initial project structure
- Architecture Decision Records (ADR-005 to ADR-008)
- Go module setup
- README with architecture overview and usage examples

### Changed
- README updated with rate limiting and caching sections
- Dependencies: Added prometheus/client_golang v1.23.2, rs/zerolog v1.34.0, testcontainers-go v0.39.0

### Fixed
- Test timezone issue in cache HTTP parser (UTC conversion for http.TimeFormat compliance)

[Unreleased]: https://github.com/Sternrassler/eve-esi-client/compare/v0.1.0...HEAD
