# Getting Started with EVE ESI Client

This guide will help you get up and running with the EVE ESI Client library quickly.

## Table of Contents

- [Prerequisites](#prerequisites)
- [Installation](#installation)
- [Quick Start](#quick-start)
- [Basic Usage](#basic-usage)
- [Next Steps](#next-steps)

## Prerequisites

Before you begin, ensure you have:

- **Go 1.21 or higher** installed
- **Redis server** (version 7.0+ recommended)
- Basic understanding of EVE Online's ESI API
- A contact email for your ESI User-Agent (ESI requirement)

### Installing Redis

#### Using Docker (Recommended)

```bash
docker run -d --name eve-esi-redis -p 6379:6379 redis:7-alpine
```

#### Using Package Manager

```bash
# macOS
brew install redis
brew services start redis

# Ubuntu/Debian
sudo apt-get install redis-server
sudo systemctl start redis

# Arch Linux
sudo pacman -S redis
sudo systemctl start redis
```

## Installation

### As a Library

Install the ESI client package:

```bash
go get github.com/Sternrassler/eve-esi-client/pkg/client
```

### For Development

Clone the repository:

```bash
git clone https://github.com/Sternrassler/eve-esi-client.git
cd eve-esi-client
go mod download
```

## Quick Start

### 1. Create Your First ESI Client

Create a file `main.go`:

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
    // 1. Connect to Redis
    redisClient := redis.NewClient(&redis.Options{
        Addr: "localhost:6379",
    })
    defer redisClient.Close()

    // 2. Create ESI client
    cfg := client.DefaultConfig(
        redisClient,
        "MyApp/1.0.0 (your-email@example.com)", // Replace with your info
    )
    
    esiClient, err := client.New(cfg)
    if err != nil {
        log.Fatal(err)
    }
    defer esiClient.Close()

    // 3. Make your first request
    ctx := context.Background()
    resp, err := esiClient.Get(ctx, "/v1/status/")
    if err != nil {
        log.Fatal(err)
    }
    defer resp.Body.Close()

    // 4. Read the response
    body, _ := io.ReadAll(resp.Body)
    fmt.Printf("ESI Status: %s\n", body)
}
```

### 2. Run Your Application

```bash
# Make sure Redis is running
redis-cli ping  # Should return "PONG"

# Run your application
go run main.go
```

**Expected Output:**

```
ESI Status: {"players":35000,"server_version":"1234567","start_time":"2025-01-01T11:00:00Z"}
```

## Basic Usage

### Fetching Market Data

```go
func fetchMarketOrders(esiClient *client.Client, regionID int) error {
    endpoint := fmt.Sprintf("/v1/markets/%d/orders/", regionID)
    
    ctx := context.Background()
    resp, err := esiClient.Get(ctx, endpoint)
    if err != nil {
        return fmt.Errorf("request failed: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != 200 {
        return fmt.Errorf("unexpected status: %d", resp.StatusCode)
    }

    body, err := io.ReadAll(resp.Body)
    if err != nil {
        return fmt.Errorf("read body: %w", err)
    }

    var orders []MarketOrder
    if err := json.Unmarshal(body, &orders); err != nil {
        return fmt.Errorf("parse json: %w", err)
    }

    fmt.Printf("Retrieved %d orders\n", len(orders))
    return nil
}
```

### Using Context Timeouts

```go
// Set 30 second timeout
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

resp, err := esiClient.Get(ctx, "/v1/markets/10000002/orders/")
if err != nil {
    if errors.Is(err, context.DeadlineExceeded) {
        log.Println("Request timed out")
    }
    return err
}
```

### Handling Different Response Types

```go
resp, err := esiClient.Get(ctx, endpoint)
if err != nil {
    return err
}
defer resp.Body.Close()

switch resp.StatusCode {
case 200:
    // Success - process data
    body, _ := io.ReadAll(resp.Body)
    // ... process body

case 304:
    // Not Modified - cache is still valid
    log.Println("Using cached data")

case 404:
    // Not Found
    return fmt.Errorf("endpoint not found")

case 420:
    // Error Limited
    return fmt.Errorf("rate limited - slow down")

case 500, 502, 503:
    // Server errors - will auto-retry
    return fmt.Errorf("server error: %d", resp.StatusCode)

default:
    return fmt.Errorf("unexpected status: %d", resp.StatusCode)
}
```

## Configuration Options

### Default Configuration

```go
cfg := client.DefaultConfig(redisClient, userAgent)
// Uses safe defaults:
// - RateLimit: 10 req/s
// - ErrorThreshold: 10
// - MaxConcurrency: 5
// - MaxRetries: 3
// - RespectExpires: true (required)
```

### Custom Configuration

```go
cfg := client.Config{
    Redis:          redisClient,
    UserAgent:      "MyApp/1.0.0 (contact@example.com)",
    
    // Rate Limiting
    RateLimit:      10,   // requests per second
    ErrorThreshold: 10,   // block when < 10 errors remaining
    
    // Concurrency
    MaxConcurrency: 5,    // max parallel requests
    
    // Caching
    MemoryCacheTTL: 60 * time.Second,
    RespectExpires: true, // MUST be true for ESI
    
    // Retry
    MaxRetries:     3,
    InitialBackoff: 1 * time.Second,
}

esiClient, err := client.New(cfg)
```

## Understanding Rate Limiting

ESI uses **error-based** rate limiting (not request-based). The client automatically:

1. **Monitors** ESI error headers (`X-ESI-Error-Limit-Remain`)
2. **Blocks** requests when errors remaining < 5 (critical)
3. **Throttles** requests when errors remaining < 20 (warning)
4. **Allows** normal operation when errors remaining ≥ 50 (healthy)

**Important:** Exceeding the error limit results in **permanent IP ban**.

```
┌─────────────────────────────────────────────┐
│ ESI Error Limit States                     │
├─────────────────────────────────────────────┤
│ ≥ 50 errors:  🟢 Healthy    (no limits)    │
│ 20-49 errors: 🟡 Warning    (throttled)    │
│ < 5 errors:   🔴 Critical   (blocked)      │
└─────────────────────────────────────────────┘
```

## Understanding Caching

The client implements **ESI-compliant caching**:

### Request Flow with Caching

```
Request 1 (Cache Miss):
  User → Client → Rate Check → Cache Miss → ESI → Cache Store → User

Request 2 (Cache Hit):
  User → Client → Rate Check → Cache Hit → Conditional Request (If-None-Match) → ESI
    ↓
  ESI returns 304 Not Modified
    ↓
  Client returns cached data to User
```

### Cache Benefits

- ✅ **Reduced ESI load** - Fewer actual API calls
- ✅ **Faster responses** - Redis cache is milliseconds vs ESI's 100-500ms
- ✅ **Error budget protection** - Cached data doesn't count against error limit
- ✅ **Automatic TTL** - Cache respects ESI `Expires` header

## Next Steps

Now that you have the basics, explore:

1. **[Configuration Guide](configuration.md)** - Detailed configuration options
2. **[Monitoring Guide](monitoring.md)** - Prometheus metrics and logging
3. **[Troubleshooting Guide](troubleshooting.md)** - Common issues and solutions
4. **[Example Code](../examples/library-usage/)** - Complete working examples
5. **[ADRs](adr/)** - Architecture decision records

## Common Patterns

### Long-Running Application

```go
func main() {
    // Setup
    redisClient := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
    defer redisClient.Close()
    
    cfg := client.DefaultConfig(redisClient, "MyApp/1.0.0 (contact@example.com)")
    esiClient, _ := client.New(cfg)
    defer esiClient.Close()

    // Expose Prometheus metrics
    http.Handle("/metrics", promhttp.Handler())
    go http.ListenAndServe(":9090", nil)

    // Main processing loop
    ticker := time.NewTicker(5 * time.Minute)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            if err := fetchAndProcessData(esiClient); err != nil {
                log.Printf("Error: %v", err)
            }
        }
    }
}
```

### Batch Processing

```go
func processManyEndpoints(esiClient *client.Client, endpoints []string) error {
    ctx := context.Background()
    
    for _, endpoint := range endpoints {
        resp, err := esiClient.Get(ctx, endpoint)
        if err != nil {
            log.Printf("Failed %s: %v", endpoint, err)
            continue // Continue with other endpoints
        }
        defer resp.Body.Close()
        
        // Process response...
        
        // Small delay between requests (optional, rate limiter handles this)
        time.Sleep(100 * time.Millisecond)
    }
    
    return nil
}
```

## ESI User-Agent Requirements

ESI **requires** a properly formatted User-Agent header:

**Format:** `ApplicationName/Version (contact information)`

**Examples:**
- ✅ `MyTradingApp/1.0.0 (trader@example.com)`
- ✅ `EveMarketTool/2.1.0 (https://github.com/user/repo)`
- ✅ `CorpDashboard/1.0.0 (Discord: username#1234)`
- ❌ `MyApp` (missing version and contact)
- ❌ `curl/7.68.0` (generic, no contact)

**Why?** CCP uses this to contact developers if there are issues with your application.

## Getting Help

- 📖 **Documentation**: [docs/](.)
- 🐛 **Issues**: [GitHub Issues](https://github.com/Sternrassler/eve-esi-client/issues)
- 💬 **Discussions**: [GitHub Discussions](https://github.com/Sternrassler/eve-esi-client/discussions)
- 📚 **ESI Docs**: [https://docs.esi.evetech.net/](https://docs.esi.evetech.net/)

## License

MIT License - See [LICENSE](../LICENSE)
