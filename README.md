# EVE ESI Client

[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)](https://go.dev/)
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

### Integrated Client (Available Now)

```go
package main

import (
    "context"
    "fmt"
    "io"
    "log"
    
    "github.com/Sternrassler/eve-esi-client/pkg/client"
    "github.com/redis/go-redis/v9"
)

func main() {
    // Create Redis client
    redisClient := redis.NewClient(&redis.Options{
        Addr: "localhost:6379",
    })
    defer redisClient.Close()
    
    // Create ESI client with default configuration
    cfg := client.DefaultConfig(redisClient, "MyApp/1.0.0 (contact@example.com)")
    esiClient, err := client.New(cfg)
    if err != nil {
        log.Fatalf("Failed to create ESI client: %v", err)
    }
    defer esiClient.Close()
    
    // Make a request (automatic rate limiting + caching)
    ctx := context.Background()
    resp, err := esiClient.Get(ctx, "/v1/status/")
    if err != nil {
        log.Fatalf("Request failed: %v", err)
    }
    defer resp.Body.Close()
    
    // Read response
    body, _ := io.ReadAll(resp.Body)
    fmt.Printf("ESI Status: %s\n", body)
}
```

See [docs/CLIENT_USAGE.md](docs/CLIENT_USAGE.md) for complete usage examples and best practices.

### Foundation Components (Also Available Separately)

You can also use the Rate Limiter and Cache Manager as standalone components if needed.

#### Rate Limit Tracker

```go
package main

import (
    "context"
    "github.com/Sternrassler/eve-esi-client/pkg/ratelimit"
    "github.com/redis/go-redis/v9"
    "github.com/rs/zerolog"
    "os"
)

func main() {
    redisClient := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
    logger := zerolog.New(os.Stderr).With().Timestamp().Logger()
    tracker := ratelimit.NewTracker(redisClient, logger)
    
    ctx := context.Background()
    
    // Check if request should be allowed
    allowed, err := tracker.ShouldAllowRequest(ctx)
    if !allowed {
        // Request blocked - wait for rate limit reset
        state, _ := tracker.GetState(ctx)
        logger.Warn().
            Int("errorsRemaining", state.ErrorsRemaining).
            Msg("Rate limit reached - request blocked")
        return
    }
    
    // Make your ESI request...
    // resp, err := http.Get("https://esi.evetech.net/...")
    
    // Update tracker from ESI response headers
    // tracker.UpdateFromHeaders(ctx, resp.Header)
}
```

#### Cache Manager

```go
package main

import (
    "context"
    "github.com/Sternrassler/eve-esi-client/pkg/cache"
    "github.com/redis/go-redis/v9"
    "net/http"
)

func main() {
    redisClient := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
    manager := cache.NewManager(redisClient)
    
    ctx := context.Background()
    endpoint := "/v1/markets/10000002/orders/"
    params := map[string]string{"order_type": "sell"}
    
    // Try cache first
    cacheKey := cache.GenerateKey(endpoint, params)
    entry, err := manager.Get(ctx, cacheKey)
    
    if err == nil && !entry.IsExpired() {
        // Cache hit - use cached response
        println("Cache hit!")
        // Use entry.Body, entry.StatusCode, etc.
        return
    }
    
    // Cache miss - make request
    req, _ := http.NewRequest("GET", "https://esi.evetech.net"+endpoint, nil)
    
    // Add conditional headers if we have a cached entry
    if entry != nil {
        cache.AddConditionalHeaders(req, entry)
    }
    
    resp, _ := http.DefaultClient.Do(req)
    defer resp.Body.Close()
    
    // Convert response to cache entry and store
    newEntry, _ := cache.ResponseToEntry(resp, endpoint, params)
    manager.Set(ctx, cacheKey, newEntry)
}
```

See [examples/cache-usage/](examples/cache-usage/) for complete standalone examples.

### Service Mode (HTTP Proxy) - Coming in Phase 2

```bash
# Coming in Phase 2
# docker run -p 8080:8080 \
#     -e REDIS_URL=redis:6379 \
#     ghcr.io/sternrassler/eve-esi-client:latest
```

## Installation

### As Library (Complete Client Available Now)

```bash
# Install full ESI client with integrated components
go get github.com/Sternrassler/eve-esi-client/pkg/client

# Or install individual components
go get github.com/Sternrassler/eve-esi-client/pkg/ratelimit
go get github.com/Sternrassler/eve-esi-client/pkg/cache
```

### As Service (Docker)

```yaml
# docker-compose.yml
services:
  esi-proxy:
    image: ghcr.io/sternrassler/eve-esi-client:latest
    ports:
      - "8080:8080"
    environment:
      REDIS_URL: redis:6379
      LOG_LEVEL: info
    depends_on:
      - redis
  
  redis:
    image: redis:7-alpine
    volumes:
      - redis-data:/data

volumes:
  redis-data:
```

## Configuration

### Library Mode

```go
config := client.Config{
    // Required
    Redis:     redisClient,
    UserAgent: "MyApp/1.0 (contact@example.com)",
    
    // Rate Limiting
    RateLimit:         10,   // requests per second
    ErrorThreshold:    10,   // stop when < 10 errors remaining
    
    // Concurrency
    MaxConcurrency:    5,    // parallel requests
    
    // Caching
    MemoryCacheTTL:    60,   // seconds
    RespectExpires:    true, // MUST be true (ESI requirement)
    
    // Retry
    MaxRetries:        3,
    InitialBackoff:    1,    // seconds
}
```

### Service Mode (Environment Variables)

```bash
REDIS_URL=localhost:6379
RATE_LIMIT=10
MAX_CONCURRENCY=5
USER_AGENT="MyApp/1.0 (contact@example.com)"
LOG_LEVEL=info
```

## ESI Compliance

This client strictly follows ESI rules to prevent bans:

✅ **Error Rate Limiting**: Tracks `X-ESI-Error-Limit-Remain` header  
✅ **Cache Respect**: Always honors `expires` header  
✅ **Conditional Requests**: Uses `If-None-Match` (ETag)  
✅ **Spread Load**: Rate limiting prevents spiky traffic  
✅ **User-Agent**: Required with contact info  

## Rate Limiting

ESI uses **error rate limiting** instead of request rate limiting. The client automatically monitors ESI's error limit headers to prevent IP bans.

### How It Works

The rate limit tracker monitors two critical headers:
- `X-ESI-Error-Limit-Remain`: Number of errors remaining before ESI blocks requests
- `X-ESI-Error-Limit-Reset`: Seconds until the error limit resets

### Thresholds

The tracker operates in three states:

| State | Errors Remaining | Behavior |
|-------|-----------------|----------|
| 🟢 **Healthy** | ≥ 50 | Normal operation, no restrictions |
| 🟡 **Warning** | 20-49 | Requests throttled (1s delay between calls) |
| 🔴 **Critical** | < 5 | All requests blocked until reset |

### State Storage

Rate limit state is shared across all client instances via Redis, ensuring coordinated behavior in multi-instance deployments.

### Library Usage

```go
import (
    "github.com/Sternrassler/eve-esi-client/pkg/ratelimit"
    "github.com/redis/go-redis/v9"
    "github.com/rs/zerolog"
)

// Create tracker
redisClient := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
logger := zerolog.New(os.Stderr)
tracker := ratelimit.NewTracker(redisClient, logger)

// Check if request should be allowed
allowed, err := tracker.ShouldAllowRequest(ctx)
if !allowed {
    // Request blocked - wait for rate limit reset
    return
}

// After receiving ESI response, update state from headers
tracker.UpdateFromHeaders(ctx, resp.Header)
```

**Important**: Exceeding the error limit results in **permanent IP ban** from ESI. The tracker prevents this by proactively blocking requests when the limit becomes critical.

## Error Handling & Retry Logic

The client implements intelligent retry logic with exponential backoff to handle transient errors while protecting against wasting the error budget.

### Error Classification

All errors are classified into four categories:

| Error Class | HTTP Status | Retry? | Description |
|------------|-------------|--------|-------------|
| **Client** | 4xx | ❌ No | Client errors (invalid request, not found, etc.) |
| **Server** | 5xx | ✅ Yes | ESI server errors (temporary issues) |
| **Rate Limit** | 520 | ✅ Yes | Endpoint-specific rate limit exceeded |
| **Network** | - | ✅ Yes | Connection timeouts, DNS failures, etc. |

### Retry Strategies

Different error classes use different retry strategies:

**Server Errors (5xx)**
- Max Attempts: 3
- Initial Backoff: 1s
- Max Backoff: 10s
- Multiplier: 2.0x

**Rate Limit (520)**
- Max Attempts: 3
- Initial Backoff: 5s (longer wait)
- Max Backoff: 60s
- Multiplier: 2.0x

**Network Errors**
- Max Attempts: 3
- Initial Backoff: 2s
- Max Backoff: 30s
- Multiplier: 2.0x

### Exponential Backoff with Jitter

The client uses exponential backoff with ±20% jitter to prevent thundering herd:

```
Attempt 1: Immediate
Attempt 2: Wait ~1s (0.8s - 1.2s with jitter)
Attempt 3: Wait ~2s (1.6s - 2.4s with jitter)
```

### Context Cancellation

All retry operations respect context cancellation, allowing you to set timeouts:

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

resp, err := client.Get(ctx, "/v1/status/")
if errors.Is(err, client.ErrContextCancelled) {
    // Request cancelled or timed out
}
```

### Why NO Retry for 4xx?

**Client errors (4xx) are NEVER retried** because:
1. They count against your error budget
2. Retrying won't fix the problem (invalid request)
3. Wasting error budget can lead to IP ban

### Error Handling Example

```go
resp, err := client.Get(ctx, "/v1/markets/10000002/orders/")
if err != nil {
    if errors.Is(err, client.ErrRetryExhausted) {
        // All retry attempts failed
        log.Error().Err(err).Msg("Request failed after retries")
    } else if errors.Is(err, client.ErrContextCancelled) {
        // Context timeout or cancellation
        log.Warn().Msg("Request cancelled")
    } else {
        // Other error (e.g., 4xx client error - no retry)
        log.Error().Err(err).Msg("Request failed")
    }
    return
}
defer resp.Body.Close()
```

## Examples

See [examples/](examples/) directory:

- [Library Usage](examples/library-usage/) - Go import example
- [Service Usage](examples/service-usage/) - HTTP client examples (Python, Node.js, curl)
- [Pagination](examples/pagination/) - Batch fetching market data

## Development

```bash
# Clone repository
git clone https://github.com/Sternrassler/eve-esi-client.git
cd eve-esi-client

# Install dependencies
go mod download

# Run tests
make test

# Run linter
make lint

# Start development service
make run
```

## Health Checks & Logging

### Health Checks

#### `/health` - Basic Health Check
Returns `200 OK` when service is running.

```bash
curl http://localhost:8080/health
# Response: OK
```

#### `/ready` - Readiness Check
Checks critical dependencies (Redis connection, rate limit state).

```bash
curl http://localhost:8080/ready
# Response: OK (200) or Service Unavailable (503)
```

### Structured Logging

The client uses [zerolog](https://github.com/rs/zerolog) for structured JSON logging.

#### Log Levels
- **Debug**: Cache operations, request flow, conditional requests
- **Info**: Successful requests, 304 responses, rate limit updates
- **Warn**: Rate limit warnings, retry attempts, non-critical errors
- **Error**: Failed requests, critical rate limit blocks, service errors

#### Configuration

```go
import "github.com/Sternrassler/eve-esi-client/pkg/logging"

// Setup global logger
logger := logging.Setup(logging.Config{
    Level:  logging.LevelInfo,
    Pretty: false, // Set to true for human-readable console output
})

// Create component logger
logger := logging.NewLogger("my-component")
logger.Info().Str("key", "value").Msg("message")
```

#### Log Context Fields
- `endpoint` - ESI endpoint path
- `status_code` - HTTP status code
- `duration` - Request duration
- `error_class` - Error classification
- `cache_hit` - Cache hit indicator
- `errors_remaining` - Current ESI error limit
- `etag` - ETag for conditional requests

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
