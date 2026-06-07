# EVE ESI Client

[![Go Version](https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**Production-ready ESI (EVE Swagger Interface) client infrastructure for EVE Online third-party applications.**

## Features

- 🚀 **High Performance**: Redis-backed caching with ETag support
- 🛡️ **Ban Protection**: ESI error rate limiting (3-tier threshold system)
- 📊 **Pagination**: parallel page fetching with worker pools (`pkg/pagination`, tested) — usable standalone with any `PageFetcher` (the client implements it); not yet wired into the client's high-level API
- 🔄 **Cache Optimization**: ETag (If-None-Match), `expires` header compliance, 304 Not Modified
- 📈 **Observability**: structured logging (Zerolog)
- 🔌 **Library**: import as a Go package

**Status**: ✅ Rate Limiter, Cache Manager and the ESI Client Core are stable. Pagination (`pkg/pagination`) is tested and usable standalone, but not yet wired into the client's high-level API.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Your Application                         │
└─────────────┬───────────────────────────────────────────────┘
              │
              └─ Library Mode (Go import)
                 import "github.com/Sternrassler/eve-esi-client/pkg/client"
                              │
┌─────────────────────────────┴───────────────────────────────┐
│              EVE ESI Client Infrastructure                  │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐       │
│  │ Rate Limiter │  │ Cache Manager│  │  Pagination  │       │
│  │ Error Limit  │  │ ETag Support │  │ Worker Pool  │       │
│  └──────────────┘  └──────────────┘  └──────────────┘       │
└─────────────────────────────┬───────────────────────────────┘
                              │
              ┌───────────────┼───────────────┐
              ▼               ▼               ▼
        ┌─────────┐     ┌─────────┐    ┌──────────┐
        │ Memory  │     │  Redis  │    │ ESI API  │
        │  Cache  │     │  Cache  │    │ (Remote) │
        └─────────┘     └─────────┘    └──────────┘
```

## Quick Start

Create a client and make a request — rate limiting and caching happen automatically:

```go
redisClient := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
defer redisClient.Close()

esiClient, _ := client.New(client.DefaultConfig(redisClient, "MyApp/1.0 (contact@example.com)"))
defer esiClient.Close()

resp, _ := esiClient.Get(context.Background(), "/v1/status/")
defer resp.Body.Close()
```

→ Full runnable program: **[examples/library-usage/](examples/library-usage/)**

### Foundation Components (also usable standalone)

The Rate Limiter and Cache Manager can be used on their own, without the integrated client:

- **Rate Limit Tracker** — proactively blocks requests when ESI's error budget runs low. See **[examples/ratelimit-usage/](examples/ratelimit-usage/)**.
- **Cache Manager** — Redis-backed cache with ETag / conditional-request handling. See **[examples/cache-usage/](examples/cache-usage/)**.

## Installation

```bash
# Full ESI client with integrated components
go get github.com/Sternrassler/eve-esi-client/pkg/client

# Or individual components
go get github.com/Sternrassler/eve-esi-client/pkg/ratelimit
go get github.com/Sternrassler/eve-esi-client/pkg/cache
```

## Configuration

`client.DefaultConfig(redis, userAgent)` returns sensible defaults; override fields as needed:

| Field | Default | Purpose |
|-------|---------|---------|
| `UserAgent` | — (required) | ESI requires `AppName/Version (contact@example.com)` |
| `RateLimit` | 10 | Requests per second |
| `ErrorThreshold` | 10 | Block when fewer errors remain in the ESI budget |
| `MaxConcurrency` | 5 | Max parallel requests |
| `MemoryCacheTTL` | 60s | In-memory cache TTL |
| `RespectExpires` | true | Honor ESI `expires` header (MUST stay true) |
| `MaxRetries` | 3 | Retry attempts for transient errors |
| `InitialBackoff` | 1s | First retry backoff (grows exponentially) |

Full reference: **[docs/configuration.md](docs/configuration.md)**.

## ESI Compliance

This client strictly follows ESI rules to prevent bans:

✅ **Group Rate Limiting**: Tracks `X-Ratelimit-*` headers + honors `Retry-After` on 429  
✅ **Error Rate Limiting**: Tracks `X-ESI-Error-Limit-Remain` header (legacy routes)  
✅ **Cache Respect**: Always honors `expires` header  
✅ **Conditional Requests**: Uses `If-None-Match` (ETag)  
✅ **Spread Load**: Rate limiting prevents spiky traffic  
✅ **User-Agent**: Required with contact info  

## Rate Limiting

ESI runs **two coexisting rate limit systems** (rollout of the new system started October 2025); the client handles both automatically:

### 1. Group rate limiting (`X-Ratelimit-*`, migrated routes)

Token buckets per **route group × consumer** (`ApplicationID:CharacterID` for authed, source IP otherwise) with a floating window (`X-Ratelimit-Limit: 3600/15m`). Token costs: 2xx = 2, 3xx = 1 (conditional requests pay off), 4xx = 5, 5xx = 0. Exceeding the bucket returns **429 with `Retry-After`**.

The `GroupTracker` learns each endpoint's group from response headers, shares per-group state via Redis and gates requests:

| Condition | Behavior |
|-----------|----------|
| Budget healthy | Normal operation |
| `Remaining` < max(5 % of limit, 10) | Requests throttled (1s delay) |
| 429 received | Group blocked until `Retry-After`; waits up to 60s inline, longer blocks fail fast with `GroupRateLimitedError` |

Docs: [ESI rate limiting](https://developers.eveonline.com/docs/services/esi/rate-limiting/).

### 2. Legacy error rate limiting (`X-ESI-Error-Limit-*`, not-yet-migrated routes)

100 non-2xx/3xx per minute, enforced via 420. The tracker watches `X-ESI-Error-Limit-Remain` / `-Reset` and operates in three states:

| State | Errors Remaining | Behavior |
|-------|-----------------|----------|
| 🟢 **Healthy** | ≥ 50 | Normal operation, no restrictions |
| 🟡 **Warning** | 20-49 | Requests throttled (1s delay between calls) |
| 🔴 **Critical** | < 5 | All requests blocked until reset |

A state whose 60s window has passed is treated as healthy and all state keys carry TTLs — stale low values cannot throttle indefinitely.

State for both systems is shared across all client instances via Redis, ensuring coordinated behavior in multi-instance deployments. For standalone use see **[examples/ratelimit-usage/](examples/ratelimit-usage/)**.

## Error Handling & Retry Logic

The client retries transient errors with exponential backoff while never wasting the error budget on client errors.

### Error Classification

| Error Class | HTTP Status | Retry? | Description |
|------------|-------------|--------|-------------|
| **Client** | 4xx (except 429) | ❌ No | Client errors (invalid request, not found, etc.) |
| **Server** | 5xx | ✅ Yes | ESI server errors (temporary issues) |
| **Rate Limit** | 429, 520 | ✅ Yes | Group rate limit (429: retry waits for `Retry-After` via the group gate) / endpoint-specific limit (520) |
| **Network** | - | ✅ Yes | Connection timeouts, DNS failures, etc. |

### Retry Strategy

Retries use exponential backoff with ±20% jitter (to avoid thundering herd) and respect `context` cancellation/timeouts:

- **Server (5xx)**: 3 attempts, 1s → 10s backoff
- **Rate Limit (520)**: 3 attempts, 5s → 60s backoff (longer wait)
- **Network**: 3 attempts, 2s → 30s backoff

**Client errors (4xx) are never retried** — retrying can't fix an invalid request and would waste the error budget (risking an IP ban). Errors are surfaced as `client.ErrRetryExhausted` and `client.ErrContextCancelled` for precise handling.

→ Error-handling and timeout patterns in context: **[examples/library-usage/](examples/library-usage/)**.

## Examples

Runnable examples in [examples/](examples/):

- **[library-usage/](examples/library-usage/)** — integrated client: config, request, caching, error handling
- **[cache-usage/](examples/cache-usage/)** — standalone Cache Manager
- **[ratelimit-usage/](examples/ratelimit-usage/)** — standalone Rate Limit Tracker
- **[pagination-usage/](examples/pagination-usage/)** — parallel page fetching with the batch fetcher

## Development

```bash
git clone https://github.com/Sternrassler/eve-esi-client.git
cd eve-esi-client
go mod download
make test    # run tests
make lint    # run linter
```

## Logging

The client uses [zerolog](https://github.com/rs/zerolog) for structured JSON logging via the `pkg/logging` package (`logging.Setup` / `logging.NewLogger`).

- **Levels**: Debug (cache/request flow), Info (successful requests, 304s, rate-limit updates), Warn (rate-limit warnings, retries), Error (failed requests, critical blocks)
- **Context fields**: `endpoint`, `status_code`, `duration`, `error_class`, `cache_hit`, `errors_remaining`, `etag`

## License

MIT License - see [LICENSE](LICENSE) file.

## Contributing

Contributions welcome! Conventional Commits sind Pflicht (via `.githooks`), und `make lint test` muss grün sein.

## Related Projects

- [eve-sde](https://github.com/Sternrassler/eve-sde) - EVE Online Static Data Export tools
- [eve-o-provit](https://github.com/Sternrassler/eve-o-provit) - EVE Online profit calculator

## Support

- 📖 [Documentation](docs/)
- 🐛 [Issue Tracker](https://github.com/Sternrassler/eve-esi-client/issues)
- 💬 [Discussions](https://github.com/Sternrassler/eve-esi-client/discussions)

## References

- [ESI Documentation](https://docs.esi.evetech.net/)
- [ESI Best Practices](https://docs.esi.evetech.net/docs/best_practices.html)
- [EVE Third Party Developer License](https://developers.eveonline.com/resource/license-agreement)
