# ADR-006: ESI Error & Rate Limit Handling

**Status**: Proposed  
**Datum**: 2025-10-27  
**Autor**: AI (basierend auf ESI Dokumentation)  
**Kontext**: ESI Error Limiting & Rate Limit Management  
**Supersedes**: -  
**Superseded By**: -

## Kontext

ESI verwendet **Error Rate Limiting** statt klassischem Request Rate Limiting:

### ESI Error Limit System

**Kritische Regel** (aus ESI Docs):
> "ESI limits how many **errors** you're allowed to get within a set time frame. Once you reach the error limit, all your requests are automatically discarded until the end of the time frame. **Failing to respect the error limit can get you banned from ESI.**"

**HTTP Headers:**
- `X-ESI-Error-Limit-Remain`: Verbleibende Errors in diesem Zeitfenster
- `X-ESI-Error-Limit-Reset`: Sekunden bis zum nächsten Reset

**Was zählt als Error:**
- HTTP 4xx (Client Errors)
- HTTP 5xx (Server Errors)  
- HTTP 520 (Endpoint-specific Rate Limit)

**Was zählt NICHT als Error:**
- HTTP 200 OK
- HTTP 304 Not Modified (Cache Hit)

### Problem

Ohne aktives Error Management:
1. Fehlerhafte Requests führen zu **automatischem Discarding** aller Requests
2. **IP Ban** bei wiederholter Überschreitung
3. Keine Möglichkeit für Wiederherstellung

## Entscheidung

Wir implementieren ein **mehrstufiges Error & Rate Limit Management System**:

### 1. Rate Limit Tracker (Shared State)

```go
// Redis Keys:
// esi:rate_limit:errors_remaining
// esi:rate_limit:reset_timestamp
// esi:rate_limit:last_update

type RateLimitState struct {
    ErrorsRemaining int       `json:"errors_remaining"`
    ResetAt         time.Time `json:"reset_at"`
    LastUpdate      time.Time `json:"last_update"`
    IsHealthy       bool      `json:"is_healthy"`
}

const (
    // Thresholds
    ErrorThresholdCritical = 5   // Stop all requests
    ErrorThresholdWarning  = 20  // Slow down requests
    ErrorThresholdHealthy  = 50  // Normal operation
)
```

### 2. Request Gating Logic

```go
func (c *ESIClient) ShouldAllowRequest() (bool, error) {
    state, err := c.getRateLimitState()
    if err != nil {
        return false, err
    }
    
    // Critical: Stop all requests
    if state.ErrorsRemaining < ErrorThresholdCritical {
        waitDuration := time.Until(state.ResetAt)
        log.Warn().
            Int("errors_remaining", state.ErrorsRemaining).
            Dur("wait_duration", waitDuration).
            Msg("ESI error limit critical - blocking requests")
        return false, ErrRateLimitCritical
    }
    
    // Warning: Apply throttling
    if state.ErrorsRemaining < ErrorThresholdWarning {
        log.Warn().
            Int("errors_remaining", state.ErrorsRemaining).
            Msg("ESI error limit warning - throttling requests")
        time.Sleep(1 * time.Second) // Slow down
    }
    
    return true, nil
}
```

### 3. Response Header Tracking

```go
func (c *ESIClient) updateRateLimitFromHeaders(resp *http.Response) error {
    // Parse headers
    remainStr := resp.Header.Get("X-ESI-Error-Limit-Remain")
    resetStr := resp.Header.Get("X-ESI-Error-Limit-Reset")
    
    if remainStr == "" || resetStr == "" {
        return nil // Headers not present (non-ESI response)
    }
    
    remain, err := strconv.Atoi(remainStr)
    if err != nil {
        return fmt.Errorf("invalid X-ESI-Error-Limit-Remain: %w", err)
    }
    
    resetSeconds, err := strconv.Atoi(resetStr)
    if err != nil {
        return fmt.Errorf("invalid X-ESI-Error-Limit-Reset: %w", err)
    }
    
    state := RateLimitState{
        ErrorsRemaining: remain,
        ResetAt:         time.Now().Add(time.Duration(resetSeconds) * time.Second),
        LastUpdate:      time.Now(),
        IsHealthy:       remain >= ErrorThresholdHealthy,
    }
    
    // Store in Redis (shared across instances)
    return c.storeRateLimitState(state)
}
```

### 4. Error Classification & Handling

```go
type ErrorClass string

const (
    ErrorClassClient     ErrorClass = "client"      // 4xx
    ErrorClassServer     ErrorClass = "server"      // 5xx
    ErrorClassRateLimit  ErrorClass = "rate_limit"  // 520
    ErrorClassNetwork    ErrorClass = "network"     // Timeout, Connection
)

func (c *ESIClient) classifyError(resp *http.Response, err error) ErrorClass {
    if err != nil {
        return ErrorClassNetwork
    }
    
    switch {
    case resp.StatusCode == 520:
        return ErrorClassRateLimit
    case resp.StatusCode >= 400 && resp.StatusCode < 500:
        return ErrorClassClient
    case resp.StatusCode >= 500:
        return ErrorClassServer
    default:
        return ""
    }
}

func (c *ESIClient) handleError(errClass ErrorClass, resp *http.Response) error {
    switch errClass {
    case ErrorClassRateLimit:
        // 520: Endpoint-specific rate limit (mail, contracts)
        // Exponential backoff (start: 5s, max: 60s)
        return c.retryWithBackoff(resp, 5*time.Second, 60*time.Second)
        
    case ErrorClassServer:
        // 5xx: ESI server error - short retry
        return c.retryWithBackoff(resp, 1*time.Second, 10*time.Second)
        
    case ErrorClassClient:
        // 4xx: Client error - NO RETRY (will count as error again!)
        return fmt.Errorf("ESI client error %d: %s", resp.StatusCode, resp.Status)
        
    case ErrorClassNetwork:
        // Network error - retry with backoff
        return c.retryWithBackoff(resp, 2*time.Second, 30*time.Second)
    }
    
    return nil
}
```

### 5. Circuit Breaker Pattern

```go
type CircuitBreakerState string

const (
    StateClosed   CircuitBreakerState = "closed"    // Normal
    StateOpen     CircuitBreakerState = "open"      // Errors too high
    StateHalfOpen CircuitBreakerState = "half_open" // Testing recovery
)

type CircuitBreaker struct {
    state           CircuitBreakerState
    failureCount    int
    successCount    int
    lastFailure     time.Time
    
    failureThreshold int           // Open circuit after N failures
    successThreshold int           // Close circuit after N successes
    timeout          time.Duration // Reset attempt after timeout
    
    mutex sync.RWMutex
}

func (cb *CircuitBreaker) Call(fn func() error) error {
    cb.mutex.RLock()
    state := cb.state
    cb.mutex.RUnlock()
    
    switch state {
    case StateOpen:
        // Check if timeout elapsed
        if time.Since(cb.lastFailure) > cb.timeout {
            cb.transitionTo(StateHalfOpen)
            return cb.Call(fn) // Retry
        }
        return ErrCircuitOpen
        
    case StateHalfOpen:
        err := fn()
        if err != nil {
            cb.recordFailure()
            return err
        }
        cb.recordSuccess()
        return nil
        
    case StateClosed:
        err := fn()
        if err != nil {
            cb.recordFailure()
            return err
        }
        cb.recordSuccess()
        return nil
    }
    
    return nil
}
```

### 6. Retry Logic (Exponential Backoff)

```go
type RetryConfig struct {
    MaxAttempts      int
    InitialBackoff   time.Duration
    MaxBackoff       time.Duration
    BackoffMultiplier float64
}

func (c *ESIClient) retryWithBackoff(resp *http.Response, initial, max time.Duration) error {
    config := RetryConfig{
        MaxAttempts:      3,
        InitialBackoff:   initial,
        MaxBackoff:       max,
        BackoffMultiplier: 2.0,
    }
    
    var lastErr error
    backoff := config.InitialBackoff
    
    for attempt := 1; attempt <= config.MaxAttempts; attempt++ {
        // Wait before retry
        if attempt > 1 {
            log.Debug().
                Int("attempt", attempt).
                Dur("backoff", backoff).
                Msg("Retrying ESI request")
            time.Sleep(backoff)
        }
        
        // Retry request
        newResp, err := c.executeRequest(resp.Request)
        if err == nil && newResp.StatusCode < 400 {
            return nil // Success
        }
        
        lastErr = err
        
        // Exponential backoff
        backoff = time.Duration(float64(backoff) * config.BackoffMultiplier)
        if backoff > config.MaxBackoff {
            backoff = config.MaxBackoff
        }
    }
    
    return fmt.Errorf("retry exhausted after %d attempts: %w", config.MaxAttempts, lastErr)
}
```

### 7. Monitoring & Alerts

```go
// Prometheus Metrics
var (
    esiErrorsRemaining = promauto.NewGauge(prometheus.GaugeOpts{
        Name: "esi_errors_remaining",
        Help: "Number of errors remaining in current ESI rate limit window",
    })
    
    esiErrorTotal = promauto.NewCounterVec(prometheus.CounterOpts{
        Name: "esi_errors_total",
        Help: "Total ESI errors by class",
    }, []string{"class"})
    
    esiCircuitBreakerState = promauto.NewGaugeVec(prometheus.GaugeOpts{
        Name: "esi_circuit_breaker_state",
        Help: "Current circuit breaker state (0=closed, 1=open, 2=half_open)",
    }, []string{"endpoint"})
)

// Alert Rules (Prometheus)
// ALERT ESIErrorLimitCritical
// IF esi_errors_remaining < 10
// FOR 1m
// LABELS { severity="critical" }
// ANNOTATIONS { summary="ESI error limit critical - requests blocked" }

// ALERT ESICircuitBreakerOpen
// IF esi_circuit_breaker_state == 1
// FOR 5m
// LABELS { severity="warning" }
// ANNOTATIONS { summary="ESI circuit breaker open for {{ $labels.endpoint }}" }
```

## Konsequenzen

### Positiv

✅ **Ban Prevention**: Proaktives Blocking bei kritischem Error Limit  
✅ **Graceful Degradation**: Circuit Breaker verhindert Error Cascades  
✅ **Observability**: Metrics & Alerts für ESI Health  
✅ **Multi-Instance Safe**: Redis Shared State  
✅ **Smart Retries**: Exponential Backoff für transiente Errors  

### Negativ

⚠️ **Komplexität**: Mehrschichtiges Error Handling  
⚠️ **Latency**: Throttling + Retry erhöht Response Time  
⚠️ **False Positives**: Circuit Breaker kann bei Spike öffnen  

### Risiken

🔴 **Shared State Race Condition**: Redis State Updates  
→ Mitigation: Atomic Redis Operations (INCR, EXPIRE)

🟡 **Retry Storm**: Viele Clients retrying gleichzeitig  
→ Mitigation: Jitter in Backoff, Request Queue

## Implementierung

```go
// pkg/esi/client.go
type ESIClient struct {
    httpClient      *http.Client
    rateLimitTracker *RateLimitTracker
    circuitBreaker  *CircuitBreaker
    cache           *CacheManager
    redis           *redis.Client
}

func (c *ESIClient) Do(req *http.Request) (*http.Response, error) {
    // 1. Check rate limit state
    if allowed, err := c.ShouldAllowRequest(); !allowed {
        return nil, err
    }
    
    // 2. Circuit breaker
    var resp *http.Response
    var err error
    
    cbErr := c.circuitBreaker.Call(func() error {
        resp, err = c.httpClient.Do(req)
        return err
    })
    
    if cbErr != nil {
        return nil, cbErr
    }
    
    // 3. Update rate limit from headers
    c.updateRateLimitFromHeaders(resp)
    
    // 4. Handle errors
    if resp.StatusCode >= 400 {
        errClass := c.classifyError(resp, nil)
        return resp, c.handleError(errClass, resp)
    }
    
    return resp, nil
}
```

## Referenzen

- [ESI Error Limiting](https://developers.eveonline.com/blog/article/error-limiting-imminent)
- [ESI Rules - Error Limit](https://docs.esi.evetech.net/docs/esi_introduction.html#error-limit)
- ADR-005: ESI Client Architecture
- ADR-007: ESI Caching Strategy

## Tags

`#esi` `#rate-limiting` `#error-handling` `#circuit-breaker` `#resilience`
