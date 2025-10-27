# ADR-005: ESI Client Architecture

**Status**: Proposed  
**Datum**: 2025-10-27  
**Autor**: AI (basierend auf ESI Dokumentation)  
**Kontext**: Implementierung eines robusten ESI (EVE Swagger Interface) Clients

## Kontext

EVE Online's ESI API ist eine **shared resource** mit strikten Regeln und Ban-Risiko bei Missbrauch. Eine robuste Client-Architektur ist notwendig, um:

- Error Rate Limits zu respektieren (automatisches Discarding bei Überschreitung)
- Caching-Regeln einzuhalten (`expires` header, ETag)
- Spread Load zu gewährleisten (konstanter slow traffic statt spikes)
- IP-Bans zu vermeiden
- Skalierbarkeit für parallele Requests zu ermöglichen

**ESI Kritische Regeln:**
1. **Error Limiting**: `X-ESI-Error-Limit-Remain` / `X-ESI-Error-Limit-Reset` → Ban bei Überschreitung
2. **Caching**: `expires` header MUSS respektiert werden → Ban bei Circumvention
3. **No Discovery**: Kein Missbrauch für structure/character discovery
4. **User-Agent**: MUST include contact info

## Entscheidung

Wir implementieren einen **zentralisierten ESI Client Service** im Backend mit folgender Architektur:

### 1. Schichtenmodell

```
┌─────────────────────────────────────────┐
│         Frontend (Next.js)              │
│  (keine direkten ESI Calls außer SSO)   │
└─────────────┬───────────────────────────┘
              │ HTTP API
┌─────────────▼───────────────────────────┐
│       API Gateway (Go Backend)          │
│   • Authentication                      │
│   • Request Routing                     │
└─────────────┬───────────────────────────┘
              │
┌─────────────▼───────────────────────────┐
│       ESI Client Service (Go)           │
│   • Rate Limit Tracker                  │
│   • Cache Manager (ETag, expires)       │
│   • Request Queue & Throttling          │
│   • Error Handling & Circuit Breaker    │
│   • Retry Logic (exponential backoff)   │
└─────────────┬───────────────────────────┘
              │
┌─────────────▼───────────────────────────┐
│       Cache Layer (Redis)               │
│   • ETag Storage                        │
│   • Response Caching                    │
│   • Rate Limit State                    │
└─────────────────────────────────────────┘
```

### 2. ESI Client Core Components

**2.1 Rate Limit Tracker**
```go
type RateLimitTracker struct {
    ErrorsRemaining int
    ResetTimestamp  time.Time
    LastUpdate      time.Time
    mutex           sync.RWMutex
}
```

**2.2 Cache Manager**
```go
type CacheEntry struct {
    ETag        string
    Expires     time.Time
    Data        []byte
    LastModified time.Time
}
```

**2.3 Request Queue**
```go
type RequestQueue struct {
    maxConcurrent int           // Max parallele Requests
    queue         chan Request
    rateLimiter   *rate.Limiter // Token bucket
}
```

**2.4 Circuit Breaker**
```go
type CircuitBreaker struct {
    state         State // Closed, Open, HalfOpen
    failureCount  int
    successCount  int
    lastFailure   time.Time
}
```

### 3. Request Flow

```
1. Client Request
   ↓
2. Check Cache (Redis)
   ├─ Cache Hit (not expired) → Return cached data
   └─ Cache Miss / Expired → Continue
   ↓
3. Check Rate Limit State
   ├─ Errors Remaining < 10 → WAIT until reset
   └─ OK → Continue
   ↓
4. Queue Request (throttled)
   ↓
5. Execute Request
   ├─ Include: User-Agent, Authorization, If-None-Match (ETag)
   └─ Capture Headers: X-ESI-Error-Limit-*, expires, etag
   ↓
6. Handle Response
   ├─ 200 OK → Cache data + ETag, update rate limit
   ├─ 304 Not Modified → Return cached data
   ├─ 4xx/5xx → Update error count, trigger circuit breaker
   └─ 520 Rate Limit → Exponential backoff
   ↓
7. Return to Client
```

### 4. Konfiguration

```yaml
esi:
  base_url: "https://esi.evetech.net"
  version: "latest"
  user_agent: "EVE-O-Provit/0.1.0 (contact@example.com)"
  
  rate_limiting:
    max_concurrent_requests: 20
    requests_per_second: 10
    error_threshold: 10  # Stop requests wenn < 10 errors remaining
  
  caching:
    respect_expires_header: true  # MUST
    default_ttl: 300s
    etag_enabled: true
  
  retry:
    max_attempts: 3
    initial_backoff: 1s
    max_backoff: 30s
    backoff_multiplier: 2
  
  circuit_breaker:
    failure_threshold: 5
    success_threshold: 2
    timeout: 60s
```

## Konsequenzen

### Positiv

✅ **Ban-Sicherheit**: Zentrale Kontrolle über alle ESI Requests  
✅ **Performance**: Redis Cache reduziert ESI Load  
✅ **Observability**: Zentrale Metrics & Logging  
✅ **Scalability**: Queue-basiertes Design für Load Spikes  
✅ **Compliance**: Automatische Einhaltung aller ESI Rules  

### Negativ

⚠️ **Komplexität**: Zusätzliche Infrastruktur (Redis, Queue Management)  
⚠️ **Single Point of Failure**: ESI Client Service critical  
⚠️ **Latency**: Zusätzlicher Hop (Frontend → Backend → ESI)  

### Risiken

🔴 **IP Ban**: Bei Fehlkonfiguration (z.B. Cache Bypass)  
→ Mitigation: Extensive Testing, Feature Flags, Monitoring

🟡 **Cache Invalidation**: Komplexe Cache-Logik  
→ Mitigation: TTL-basiert + `expires` header

🟡 **State Management**: Rate Limit State bei Multi-Instance  
→ Mitigation: Redis als Shared State Store

## Alternativen

**Alternative 1: Direct Frontend → ESI**  
❌ Rejected: Keine zentrale Rate Limit Control, Secrets Exposure

**Alternative 2: ESI SDK (Third-Party)**  
❌ Rejected: Keine Go SDKs mit voller Feature-Parity, eigene Kontrolle bevorzugt

**Alternative 3: ESI Proxy (Dedicated Service)**  
⚠️ Considered: Bessere Isolation, aber Overhead für jetzige Größe

## Implementierungs-Phasen

### Phase 1: Foundation (v0.2.0)
- [ ] ESI Client Core (single request)
- [ ] Rate Limit Tracker (in-memory)
- [ ] Basic Error Handling
- [ ] User-Agent Header

### Phase 2: Caching (v0.3.0)
- [ ] Redis Integration
- [ ] ETag Support
- [ ] `expires` Header Respect
- [ ] Cache Invalidation Logic

### Phase 3: Advanced (v0.4.0)
- [ ] Request Queue & Throttling
- [ ] Circuit Breaker Pattern
- [ ] Retry Logic (exponential backoff)
- [ ] Pagination Helper

### Phase 4: Production Hardening (v0.5.0)
- [ ] Metrics (Prometheus)
- [ ] Distributed Tracing
- [ ] Health Checks
- [ ] Load Testing & Tuning

## Referenzen

- [ESI Introduction](https://docs.esi.evetech.net/docs/esi_introduction.html)
- [ESI Rules (Error Limiting, Caching)](https://docs.esi.evetech.net/docs/esi_introduction.html#rules)
- [ESI Best Practices (Community)](https://docs.esi.evetech.net/docs/best_practices.html)
- ADR-006: ESI Error & Rate Limit Handling (Details)
- ADR-007: ESI Caching Strategy (ETag, expires)
- ADR-008: ESI Pagination & Batch Processing

## Tags

`#esi` `#architecture` `#rate-limiting` `#caching` `#infrastructure`
