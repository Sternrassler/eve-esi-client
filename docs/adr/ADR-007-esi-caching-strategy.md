# ADR-007: ESI Caching Strategy

**Status**: Proposed  
**Datum**: 2025-10-27  
**Autor**: AI (basierend auf ESI Dokumentation)  
**Kontext**: ESI Caching & Cache Invalidation  
**Supersedes**: -  
**Superseded By**: -

## Kontext

ESI setzt **striktes Caching** mit HTTP Standard Headers voraus:

### ESI Caching Rules (KRITISCH)

**Aus ESI Dokumentation:**
> "**All ESI routes have caching defined by the HTTP `expires` header.** This means that you should cache data for that long. **Circumventing this on purpose can get you banned from ESI.**"

**HTTP Cache Headers:**
- `expires`: Zeitpunkt, wann Daten veraltet sind (MUST respect)
- `last-modified`: Wann Daten zuletzt geändert wurden
- `ETag`: Eindeutiger Hash der Daten

**Conditional Requests:**
- `If-None-Match: <ETag>` → Response: `304 Not Modified` (bei Cache Hit)
- `If-Modified-Since: <Date>` → Response: `304 Not Modified` (bei keine Änderung)

**Wichtig:**
- `304 Not Modified` zählt **NICHT** als Error (Error Limit safe!)
- Cache Hit spart Bandwidth & Error Budget
- Cache Umgehung = **PERMANENT BAN RISK**

### Problem

Ohne ordentliches Caching:
1. Unnötige Requests → Error Limit Erschöpfung
2. Hohe Latenz (kein lokaler Cache)
3. Bandwidth Verschwendung
4. **Ban Risiko** bei absichtlicher Cache-Umgehung

## Entscheidung

Wir implementieren ein **mehrstufiges Caching System** mit strikter `expires` Header Einhaltung:

### 1. Cache Architecture (3 Layers)

```
┌─────────────────────────────────────────────────┐
│ Layer 1: In-Memory Cache (Application)         │
│ - TTL: 60 seconds                               │
│ - Purpose: Hot data, high-frequency requests   │
│ - Size: 100 MB                                  │
└─────────────────────────────────────────────────┘
                      ↓
┌─────────────────────────────────────────────────┐
│ Layer 2: Redis Cache (Shared)                  │
│ - TTL: From ESI expires header                 │
│ - Purpose: Shared cache across instances       │
│ - Size: 1 GB                                    │
└─────────────────────────────────────────────────┘
                      ↓
┌─────────────────────────────────────────────────┐
│ Layer 3: ESI (Remote)                           │
│ - Conditional Request: If-None-Match (ETag)    │
│ - Response: 304 Not Modified or 200 OK         │
└─────────────────────────────────────────────────┘
```

### 2. Cache Key Strategy

```go
type CacheKey struct {
    Endpoint     string            // "/v4/markets/{region_id}/orders/"
    PathParams   map[string]string // {"region_id": "10000002"}
    QueryParams  url.Values        // {"order_type": "all"}
    CharacterID  int64             // For authenticated endpoints
}

func (k CacheKey) String() string {
    // Format: esi:v4:markets:10000002:orders:order_type=all:char=123456
    parts := []string{"esi", k.Endpoint}
    
    // Add path params (sorted)
    for key, val := range k.PathParams {
        parts = append(parts, fmt.Sprintf("%s=%s", key, val))
    }
    
    // Add query params (sorted)
    keys := make([]string, 0, len(k.QueryParams))
    for key := range k.QueryParams {
        keys = append(keys, key)
    }
    sort.Strings(keys)
    
    for _, key := range keys {
        parts = append(parts, fmt.Sprintf("%s=%s", key, k.QueryParams.Get(key)))
    }
    
    // Add character ID if authenticated
    if k.CharacterID > 0 {
        parts = append(parts, fmt.Sprintf("char=%d", k.CharacterID))
    }
    
    return strings.Join(parts, ":")
}
```

### 3. Cache Entry Structure

```go
type CacheEntry struct {
    Data         []byte    `json:"data"`           // Response body
    ETag         string    `json:"etag"`           // For conditional requests
    Expires      time.Time `json:"expires"`        // From expires header
    LastModified time.Time `json:"last_modified"`  // From last-modified header
    StatusCode   int       `json:"status_code"`    // HTTP status
    Headers      http.Header `json:"headers"`      // Response headers
    CachedAt     time.Time `json:"cached_at"`      // When we cached it
}

func (e *CacheEntry) IsExpired() bool {
    return time.Now().After(e.Expires)
}

func (e *CacheEntry) TTL() time.Duration {
    return time.Until(e.Expires)
}
```

### 4. Cache Manager Implementation

```go
type CacheManager struct {
    redis       *redis.Client
    memCache    *sync.Map // In-memory cache (Layer 1)
    memCacheTTL time.Duration
}

// Get retrieves from cache hierarchy
func (cm *CacheManager) Get(ctx context.Context, key CacheKey) (*CacheEntry, error) {
    cacheKey := key.String()
    
    // Layer 1: In-Memory Cache
    if val, ok := cm.memCache.Load(cacheKey); ok {
        entry := val.(*CacheEntry)
        if !entry.IsExpired() {
            log.Debug().Str("key", cacheKey).Msg("Cache hit (memory)")
            return entry, nil
        }
        cm.memCache.Delete(cacheKey) // Cleanup expired
    }
    
    // Layer 2: Redis Cache
    data, err := cm.redis.Get(ctx, cacheKey).Bytes()
    if err == nil {
        var entry CacheEntry
        if err := json.Unmarshal(data, &entry); err == nil {
            if !entry.IsExpired() {
                log.Debug().Str("key", cacheKey).Msg("Cache hit (redis)")
                // Promote to memory cache
                cm.memCache.Store(cacheKey, &entry)
                return &entry, nil
            }
        }
    }
    
    log.Debug().Str("key", cacheKey).Msg("Cache miss")
    return nil, ErrCacheMiss
}

// Set stores in cache hierarchy
func (cm *CacheManager) Set(ctx context.Context, key CacheKey, entry *CacheEntry) error {
    cacheKey := key.String()
    
    // Layer 1: In-Memory (with shorter TTL)
    memTTL := entry.TTL()
    if memTTL > cm.memCacheTTL {
        memTTL = cm.memCacheTTL
    }
    cm.memCache.Store(cacheKey, entry)
    
    // Layer 2: Redis (with ESI expires TTL)
    data, err := json.Marshal(entry)
    if err != nil {
        return fmt.Errorf("marshal cache entry: %w", err)
    }
    
    ttl := entry.TTL()
    if ttl < 0 {
        ttl = 0 // Expired, don't cache
    }
    
    return cm.redis.Set(ctx, cacheKey, data, ttl).Err()
}
```

### 5. Conditional Request Logic

```go
func (c *ESIClient) DoWithCache(req *http.Request, key CacheKey) (*http.Response, error) {
    ctx := req.Context()
    
    // Check cache
    cached, err := c.cache.Get(ctx, key)
    if err == nil {
        // Cache hit - make conditional request
        if cached.ETag != "" {
            req.Header.Set("If-None-Match", cached.ETag)
            log.Debug().
                Str("etag", cached.ETag).
                Msg("Making conditional request with ETag")
        } else if !cached.LastModified.IsZero() {
            req.Header.Set("If-Modified-Since", 
                cached.LastModified.Format(http.TimeFormat))
            log.Debug().
                Time("last_modified", cached.LastModified).
                Msg("Making conditional request with Last-Modified")
        }
    }
    
    // Execute request
    resp, err := c.Do(req)
    if err != nil {
        // On error, return cached data if available
        if cached != nil && !cached.IsExpired() {
            log.Warn().Err(err).Msg("ESI error, serving stale cache")
            return c.cacheEntryToResponse(cached), nil
        }
        return nil, err
    }
    
    // Handle 304 Not Modified
    if resp.StatusCode == http.StatusNotModified {
        log.Debug().Msg("304 Not Modified - cache still valid")
        
        // Update TTL from new expires header
        if expiresStr := resp.Header.Get("Expires"); expiresStr != "" {
            if newExpires, err := http.ParseTime(expiresStr); err == nil {
                cached.Expires = newExpires
                c.cache.Set(ctx, key, cached)
            }
        }
        
        return c.cacheEntryToResponse(cached), nil
    }
    
    // Handle 200 OK - update cache
    if resp.StatusCode == http.StatusOK {
        entry := c.responseToEntry(resp)
        if entry.TTL() > 0 {
            c.cache.Set(ctx, key, entry)
        }
    }
    
    return resp, nil
}
```

### 6. Expires Header Parsing

```go
func (c *ESIClient) responseToEntry(resp *http.Response) *CacheEntry {
    // Read body
    body, err := io.ReadAll(resp.Body)
    if err != nil {
        log.Error().Err(err).Msg("Failed to read response body")
        return nil
    }
    resp.Body.Close()
    
    // Create new ReadCloser for response
    resp.Body = io.NopCloser(bytes.NewReader(body))
    
    entry := &CacheEntry{
        Data:       body,
        ETag:       resp.Header.Get("ETag"),
        StatusCode: resp.StatusCode,
        Headers:    resp.Header.Clone(),
        CachedAt:   time.Now(),
    }
    
    // Parse Expires header (MUST respect)
    if expiresStr := resp.Header.Get("Expires"); expiresStr != "" {
        if expires, err := http.ParseTime(expiresStr); err == nil {
            entry.Expires = expires
            log.Debug().
                Time("expires", expires).
                Dur("ttl", entry.TTL()).
                Msg("Parsed expires header")
        } else {
            log.Warn().
                Str("expires", expiresStr).
                Err(err).
                Msg("Failed to parse expires header")
            // Fallback: 5 minutes
            entry.Expires = time.Now().Add(5 * time.Minute)
        }
    } else {
        // No expires header - use default
        entry.Expires = time.Now().Add(5 * time.Minute)
        log.Debug().Msg("No expires header, using default TTL (5m)")
    }
    
    // Parse Last-Modified header
    if lastModStr := resp.Header.Get("Last-Modified"); lastModStr != "" {
        if lastMod, err := http.ParseTime(lastModStr); err == nil {
            entry.LastModified = lastMod
        }
    }
    
    return entry
}
```

### 7. Cache Invalidation Strategies

```go
// Strategy 1: TTL-based (Automatic via Redis EXPIRE)
// - Respects ESI expires header
// - No manual invalidation needed

// Strategy 2: Event-based (Manual Invalidation)
func (cm *CacheManager) InvalidatePattern(ctx context.Context, pattern string) error {
    // Example: Invalidate all market orders for region
    // Pattern: "esi:v4:markets:10000002:orders:*"
    
    iter := cm.redis.Scan(ctx, 0, pattern, 0).Iterator()
    for iter.Next(ctx) {
        if err := cm.redis.Del(ctx, iter.Val()).Err(); err != nil {
            log.Error().Err(err).Str("key", iter.Val()).Msg("Failed to delete cache key")
        }
    }
    return iter.Err()
}

// Strategy 3: Stale-While-Revalidate
func (cm *CacheManager) GetStale(ctx context.Context, key CacheKey) (*CacheEntry, bool) {
    entry, err := cm.Get(ctx, key)
    if err != nil {
        return nil, false
    }
    
    isStale := entry.IsExpired()
    return entry, isStale
}

func (c *ESIClient) DoWithStaleWhileRevalidate(req *http.Request, key CacheKey) (*http.Response, error) {
    entry, isStale := c.cache.GetStale(req.Context(), key)
    
    if entry != nil && !isStale {
        // Fresh cache hit
        return c.cacheEntryToResponse(entry), nil
    }
    
    if entry != nil && isStale {
        // Stale cache - serve immediately, revalidate in background
        go func() {
            ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
            defer cancel()
            
            req := req.Clone(ctx)
            c.DoWithCache(req, key) // Revalidate
        }()
        
        return c.cacheEntryToResponse(entry), nil
    }
    
    // Cache miss - blocking request
    return c.DoWithCache(req, key)
}
```

### 8. Cache Warming & Preloading

```go
// Preload frequently accessed data
func (c *ESIClient) WarmCache(ctx context.Context) error {
    warmupEndpoints := []struct{
        Path   string
        Params map[string]string
    }{
        // Market hubs
        {"/v1/markets/10000002/orders/", map[string]string{"order_type": "all"}}, // The Forge
        {"/v1/markets/10000043/orders/", map[string]string{"order_type": "all"}}, // Domain
        
        // Static data
        {"/v3/universe/types/", nil},
        {"/v1/universe/categories/", nil},
    }
    
    for _, endpoint := range warmupEndpoints {
        req, err := c.buildRequest("GET", endpoint.Path, endpoint.Params)
        if err != nil {
            log.Error().Err(err).Str("path", endpoint.Path).Msg("Failed to build warmup request")
            continue
        }
        
        key := CacheKey{
            Endpoint:   endpoint.Path,
            PathParams: endpoint.Params,
        }
        
        _, err = c.DoWithCache(req, key)
        if err != nil {
            log.Error().Err(err).Str("path", endpoint.Path).Msg("Cache warmup failed")
        } else {
            log.Info().Str("path", endpoint.Path).Msg("Cache warmed")
        }
        
        // Rate limit friendly
        time.Sleep(100 * time.Millisecond)
    }
    
    return nil
}
```

### 9. Monitoring & Metrics

```go
// Prometheus Metrics
var (
    esiCacheHits = promauto.NewCounterVec(prometheus.CounterOpts{
        Name: "esi_cache_hits_total",
        Help: "Total ESI cache hits by layer",
    }, []string{"layer"}) // memory, redis
    
    esiCacheMisses = promauto.NewCounter(prometheus.CounterOpts{
        Name: "esi_cache_misses_total",
        Help: "Total ESI cache misses",
    })
    
    esiCacheSize = promauto.NewGaugeVec(prometheus.GaugeOpts{
        Name: "esi_cache_size_bytes",
        Help: "ESI cache size in bytes",
    }, []string{"layer"})
    
    esiConditionalRequests = promauto.NewCounter(prometheus.CounterOpts{
        Name: "esi_conditional_requests_total",
        Help: "Total ESI conditional requests (If-None-Match)",
    })
    
    esi304Responses = promauto.NewCounter(prometheus.CounterOpts{
        Name: "esi_304_responses_total",
        Help: "Total ESI 304 Not Modified responses",
    })
)

// Cache Hit Rate
func (cm *CacheManager) HitRate() float64 {
    hits := esiCacheHits.WithLabelValues("memory").Get() + 
            esiCacheHits.WithLabelValues("redis").Get()
    misses := esiCacheMisses.Get()
    total := hits + misses
    
    if total == 0 {
        return 0.0
    }
    
    return hits / total
}
```

### 10. Configuration

```yaml
# config/esi-cache.yaml
cache:
  # Layer 1: In-Memory Cache
  memory:
    enabled: true
    ttl: 60s              # Max TTL (will use ESI expires if shorter)
    max_size: 100MB       # Max memory usage
    eviction: lru         # LRU eviction policy
  
  # Layer 2: Redis Cache
  redis:
    enabled: true
    host: localhost:6379
    db: 0
    password: ""
    max_connections: 100
    key_prefix: "esi:"
  
  # Conditional Requests
  conditional:
    enabled: true         # MUST be true (ESI requirement)
    use_etag: true        # Use If-None-Match
    use_last_modified: true # Use If-Modified-Since
  
  # Stale-While-Revalidate
  stale_while_revalidate:
    enabled: true
    max_stale: 300s       # Serve stale cache for max 5 minutes
  
  # Cache Warming
  warmup:
    enabled: true
    on_startup: true
    endpoints:
      - "/v1/markets/10000002/orders/"
      - "/v1/markets/10000043/orders/"
      - "/v3/universe/types/"
```

## Konsequenzen

### Positiv

✅ **Ban Prevention**: Strikte `expires` Header Einhaltung  
✅ **Performance**: 3-Layer Cache (Memory → Redis → ESI)  
✅ **Bandwidth Savings**: Conditional Requests (304 Not Modified)  
✅ **Error Budget Savings**: Cache Hits zählen nicht als Errors  
✅ **Scalability**: Shared Redis Cache für Multi-Instance  
✅ **Resilience**: Stale-While-Revalidate für Graceful Degradation  

### Negativ

⚠️ **Complexity**: Mehrstufiges Cache System  
⚠️ **Memory Usage**: In-Memory Cache Layer  
⚠️ **Redis Dependency**: Infrastruktur Anforderung  

### Risiken

🔴 **Cache Stampede**: Viele Clients fetching expired key gleichzeitig  
→ Mitigation: Request Coalescing (single-flight pattern)

🟡 **Stale Data**: User sieht veraltete Daten  
→ Mitigation: Respektiert ESI TTL, optional manuelles Refresh

## Implementierung

### Phase 1 (v0.3.0): Basic Caching
- Redis integration
- Expires header parsing
- Basic cache Get/Set

### Phase 2 (v0.3.1): Conditional Requests
- ETag support (If-None-Match)
- Last-Modified support (If-Modified-Since)
- 304 Not Modified handling

### Phase 3 (v0.4.0): Advanced Caching
- In-Memory Layer
- Stale-While-Revalidate
- Cache warming

### Phase 4 (v0.5.0): Production Hardening
- Monitoring & Metrics
- Cache invalidation patterns
- Request coalescing

## Referenzen

- [ESI Caching Rules](https://docs.esi.evetech.net/docs/esi_introduction.html#caching)
- [HTTP Caching (RFC 7234)](https://tools.ietf.org/html/rfc7234)
- [Conditional Requests (RFC 7232)](https://tools.ietf.org/html/rfc7232)
- ADR-005: ESI Client Architecture
- ADR-006: ESI Error & Rate Limit Handling

## Tags

`#esi` `#caching` `#redis` `#performance` `#etag` `#conditional-requests`
