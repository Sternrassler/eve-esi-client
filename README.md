# EVE ESI Client

[![Go Version](https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**Production-ready ESI (EVE Swagger Interface) client infrastructure for EVE Online third-party applications.**

## Features

- 🚀 **High Performance**: Redis-backed caching with ETag support
- 🛡️ **Ban Protection**: ESI error rate limiting (3-tier threshold system)
- 📊 **Pagination Support**: *(Coming in Phase 2)* Parallel page fetching with worker pools
- 🔄 **Cache Optimization**: ETag (If-None-Match), `expires` header compliance, 304 Not Modified
- 📈 **Observability**: structured logging (Zerolog)
- 🔌 **Flexible**: *(Phase 1)* Go library mode | *(Phase 2)* HTTP service mode

**Phase 1 Status (Foundation)**: ✅ **Rate Limiter, Cache Manager & ESI Client Core COMPLETED**  
**Next**: Pagination Support (Issue #4) and Service Mode (Phase 2)

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Your Application                         │
└─────────────┬───────────────────────────────────────────────┘
              │
              ├─ Option A: Library Mode (Go import)
              │  import "github.com/Sternrassler/eve-esi-client/pkg/client"
              │
              └─ Option B: Service Mode (HTTP API)
                 http://localhost:8080/esi/v4/markets/.../orders/
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

### Service Mode (HTTP Proxy)

*Coming in Phase 2* — a containerized HTTP proxy (`ghcr.io/sternrassler/eve-esi-client`).

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

Full reference: **[docs/configuration.md](docs/configuration.md)**. Service-mode environment variables (`REDIS_URL`, `RATE_LIMIT`, `USER_AGENT`, `LOG_LEVEL`, …) are documented there too.

## ESI Compliance

This client strictly follows ESI rules to prevent bans:

✅ **Error Rate Limiting**: Tracks `X-ESI-Error-Limit-Remain` header  
✅ **Cache Respect**: Always honors `expires` header  
✅ **Conditional Requests**: Uses `If-None-Match` (ETag)  
✅ **Spread Load**: Rate limiting prevents spiky traffic  
✅ **User-Agent**: Required with contact info  

## Rate Limiting

ESI uses **error rate limiting** instead of request rate limiting. The client automatically monitors ESI's error limit headers to prevent IP bans.

The tracker watches `X-ESI-Error-Limit-Remain` / `X-ESI-Error-Limit-Reset` and operates in three states:

| State | Errors Remaining | Behavior |
|-------|-----------------|----------|
| 🟢 **Healthy** | ≥ 50 | Normal operation, no restrictions |
| 🟡 **Warning** | 20-49 | Requests throttled (1s delay between calls) |
| 🔴 **Critical** | < 5 | All requests blocked until reset |

State is shared across all client instances via Redis, ensuring coordinated behavior in multi-instance deployments. **Exceeding the error limit results in a permanent IP ban** — the integrated client handles this for you; for standalone use see **[examples/ratelimit-usage/](examples/ratelimit-usage/)**.

## Error Handling & Retry Logic

The client retries transient errors with exponential backoff while never wasting the error budget on client errors.

### Error Classification

| Error Class | HTTP Status | Retry? | Description |
|------------|-------------|--------|-------------|
| **Client** | 4xx | ❌ No | Client errors (invalid request, not found, etc.) |
| **Server** | 5xx | ✅ Yes | ESI server errors (temporary issues) |
| **Rate Limit** | 520 | ✅ Yes | Endpoint-specific rate limit exceeded |
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

*(Phase 2 will add service-mode and pagination examples.)*

## Development

```bash
git clone https://github.com/Sternrassler/eve-esi-client.git
cd eve-esi-client
go mod download
make test    # run tests
make lint    # run linter
make run     # start development service
```

## Health Checks & Logging

### Health Checks (Service Mode)

- `GET /health` — basic liveness, returns `200 OK`.
- `GET /ready` — readiness; checks Redis connection and rate-limit state (`200` or `503`).

### Structured Logging

The client uses [zerolog](https://github.com/rs/zerolog) for structured JSON logging via the `pkg/logging` package (`logging.Setup` / `logging.NewLogger`).

- **Levels**: Debug (cache/request flow), Info (successful requests, 304s, rate-limit updates), Warn (rate-limit warnings, retries), Error (failed requests, critical blocks)
- **Context fields**: `endpoint`, `status_code`, `duration`, `error_class`, `cache_hit`, `errors_remaining`, `etag`

## License

MIT License - see [LICENSE](LICENSE) file.

## Contributing

Contributions welcome! Please read [CONTRIBUTING.md](CONTRIBUTING.md) first.

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
