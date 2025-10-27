# EVE ESI Client

[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**Production-ready ESI (EVE Swagger Interface) client infrastructure for EVE Online third-party applications.**

## Features

- 🚀 **High Performance**: Multi-layer caching (Memory → Redis → ESI)
- 🛡️ **Ban Protection**: Error rate limiting, circuit breaker, exponential backoff
- 📊 **Pagination Support**: Parallel page fetching with worker pools
- 🔄 **Cache Optimization**: ETag (If-None-Match), `expires` header compliance
- 📈 **Observability**: Prometheus metrics, structured logging
- 🔌 **Flexible**: Use as Go library or standalone HTTP service

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

### Library Mode (Recommended for Go Applications)

```go
package main

import (
    "context"
    "github.com/Sternrassler/eve-esi-client/pkg/client"
    "github.com/redis/go-redis/v9"
)

func main() {
    // Setup Redis
    redisClient := redis.NewClient(&redis.Options{
        Addr: "localhost:6379",
    })
    
    // Create ESI client
    esiClient := client.New(client.Config{
        Redis:           redisClient,
        RateLimit:       10,  // requests per second
        MaxConcurrency:  5,   // parallel requests
        UserAgent:       "MyApp/1.0 (contact@example.com)",
    })
    
    // Fetch market orders
    resp, err := esiClient.Get(context.Background(), "/v1/markets/10000002/orders/")
    if err != nil {
        panic(err)
    }
    
    defer resp.Body.Close()
    // Process response...
}
```

### Service Mode (HTTP Proxy)

```bash
# Start service
docker run -p 8080:8080 \
    -e REDIS_URL=redis:6379 \
    ghcr.io/sternrassler/eve-esi-client:latest

# Use from any language
curl http://localhost:8080/esi/v4/markets/10000002/orders/
```

## Installation

### As Library

```bash
go get github.com/Sternrassler/eve-esi-client/pkg/client
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
METRICS_PORT=9090
```

## ESI Compliance

This client strictly follows ESI rules to prevent bans:

✅ **Error Rate Limiting**: Tracks `X-ESI-Error-Limit-Remain` header  
✅ **Cache Respect**: Always honors `expires` header  
✅ **Conditional Requests**: Uses `If-None-Match` (ETag)  
✅ **Spread Load**: Rate limiting prevents spiky traffic  
✅ **User-Agent**: Required with contact info  

## Caching

The cache manager implements ESI-compliant caching with Redis:

### Features

- **Strict expires header compliance** - Prevents IP bans
- **ETag support** - Conditional requests with `If-None-Match`
- **Last-Modified support** - Conditional requests with `If-Modified-Since`
- **Automatic TTL management** - Based on ESI `expires` header
- **Prometheus metrics** - Cache hits, misses, and 304 responses

### Usage

```go
import (
    "github.com/Sternrassler/eve-esi-client/pkg/cache"
    "github.com/redis/go-redis/v9"
)

// Create cache manager
redisClient := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
cacheManager := cache.NewManager(redisClient)

// Create cache key
key := cache.CacheKey{
    Endpoint: "/v1/markets/10000002/orders/",
    QueryParams: url.Values{"order_type": []string{"all"}},
}

// Try to get from cache
entry, err := cacheManager.Get(ctx, key)
if err == cache.ErrCacheMiss {
    // Cache miss - fetch from ESI
    resp, err := client.Get(ctx, endpoint)
    
    // Convert response to cache entry
    entry, _ = cache.ResponseToEntry(resp)
    
    // Store in cache
    cacheManager.Set(ctx, key, entry)
}

// Use cached data
fmt.Println(string(entry.Data))
```

### Conditional Requests

```go
// Check cached entry
entry, err := cacheManager.Get(ctx, key)
if err == nil && cache.ShouldMakeConditionalRequest(entry) {
    // Add If-None-Match header
    cache.AddConditionalHeaders(req, entry)
    
    // Make request
    resp, _ := client.Do(req)
    
    // Handle 304 Not Modified
    if resp.StatusCode == http.StatusNotModified {
        // Update TTL from new expires header
        newExpires := cache.parseExpires(resp.Header)
        cacheManager.UpdateTTL(ctx, key, newExpires)
        
        // Use cached data
        return entry.Data
    }
}
```

### Metrics

Available Prometheus metrics:

```
esi_cache_hits_total{layer="redis"}      # Cache hits
esi_cache_misses_total                   # Cache misses
esi_cache_size_bytes{layer="redis"}      # Cache size in bytes
esi_304_responses_total                  # 304 Not Modified responses
esi_cache_errors_total{operation}        # Cache operation errors
```

## Architecture Decision Records

See [docs/adr/](docs/adr/) for detailed design decisions:

- [ADR-005: ESI Client Architecture](docs/adr/ADR-005-esi-client-architecture.md)
- [ADR-006: Error & Rate Limit Handling](docs/adr/ADR-006-esi-error-rate-limit-handling.md)
- [ADR-007: Caching Strategy](docs/adr/ADR-007-esi-caching-strategy.md)
- [ADR-008: Pagination & Batch Processing](docs/adr/ADR-008-esi-pagination-batch-processing.md)

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

## Monitoring

Prometheus metrics available at `/metrics`:

```
esi_errors_remaining          # Current error limit remaining
esi_cache_hits_total          # Cache hits by layer (memory, redis)
esi_cache_misses_total        # Cache misses
esi_requests_total            # Total requests by endpoint
esi_circuit_breaker_state     # Circuit breaker state
esi_pagination_duration       # Pagination fetch duration
```

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
