# ADR-009: Redis Dependency Injection & Key Namespacing

**Status**: Proposed  
**Datum**: 2025-10-27  
**Kontext**: ESI Client Infrastructure  
**Entscheider**: Architecture Team  

## Kontext

Die ESI Client Library benötigt Redis für Caching (ADR-007) und Rate Limiting (ADR-006). Es stellt sich die Frage, wie Redis bereitgestellt wird und wie Key-Konflikte mit anderen Anwendungen vermieden werden.

### Anforderungen

1. **Flexibilität**: Library muss mit verschiedenen Redis-Setups funktionieren
2. **Testbarkeit**: Mock-Redis für Unit/Integration Tests
3. **Isolation**: Keine Key-Konflikte mit anderen Anwendungen
4. **Ressourceneffizienz**: Mehrere Anwendungen sollen eine Redis-Instanz teilen können
5. **Production-Ready**: Unterstützung für Redis Cluster, Sentinel, Standalone

### Betrachtete Optionen

#### Option A: Interne Redis-Verwaltung
Library erstellt eigene Redis-Verbindung.

**Vorteile**:
- Einfache API für Nutzer
- Vollständige Kontrolle über Verbindungsparameter

**Nachteile**:
- Erzwingt separate Redis-Instanz pro Anwendung
- Schwer testbar (keine Dependency Injection)
- Inflexibel bei komplexeren Setups (Cluster, Sentinel)
- Ressourcenverschwendung

#### Option B: Redis-Client als Dependency Injection
Caller übergibt konfigurierte `*redis.Client` Instanz.

**Vorteile**:
- Maximale Flexibilität (Caller entscheidet über Setup)
- Testbar (Mock-Redis einfach injizierbar)
- Shared Redis möglich (Ressourceneffizienz)
- Unterstützt alle Redis-Topologien

**Nachteile**:
- Caller muss Redis verwalten
- Zusätzliche Setup-Komplexität für Nutzer

#### Option C: Beide Ansätze (Helper + Injection)
Library bietet Helper-Funktion für einfache Fälle + Injection für erweiterte Nutzung.

**Vorteile**:
- Beste Developer Experience
- Flexibilität für fortgeschrittene Nutzer

**Nachteile**:
- Komplexere API-Oberfläche
- Wartungsaufwand für zwei Code-Pfade

## Entscheidung

**Option B: Redis als Dependency Injection mit Key-Namespacing**

### Begründung

1. **Hybrid Architecture**: Als Library (ADR-005) muss eve-esi-client flexibel integrierbar sein
2. **Production Pattern**: Shared Redis ist etabliertes Pattern (Cost-Effective)
3. **Testability**: Dependency Injection ermöglicht einfaches Mocking
4. **Separation of Concerns**: Redis-Management ist nicht Aufgabe der ESI-Library

### Implementierung

#### API Design

```go
package client

import "github.com/redis/go-redis/v9"

type Config struct {
    // Redis client (REQUIRED) - provided by caller
    Redis *redis.Client
    
    // Key prefix for all Redis keys (default: "esi:")
    KeyPrefix string
    
    // Other config...
    UserAgent      string
    RateLimit      int
    ErrorThreshold int
}

type Client struct {
    redis      *redis.Client
    keyPrefix  string
    // ...
}

func New(cfg Config) (*Client, error) {
    if cfg.Redis == nil {
        return nil, errors.New("redis client is required")
    }
    
    keyPrefix := cfg.KeyPrefix
    if keyPrefix == "" {
        keyPrefix = "esi:" // Default prefix
    }
    
    return &Client{
        redis:     cfg.Redis,
        keyPrefix: keyPrefix,
        // ...
    }, nil
}
```

#### Key-Namespacing

Alle Redis-Keys erhalten automatisches Prefix:

```go
func (c *Client) cacheKey(path string) string {
    return c.keyPrefix + "cache:" + path
}

func (c *Client) rateLimitKey() string {
    return c.keyPrefix + "ratelimit:errors:remaining"
}
```

**Beispiel-Keys**:
```
esi:cache:/markets/10000002/orders/
esi:cache:/characters/123456/
esi:ratelimit:errors:remaining
esi:ratelimit:errors:reset
esi:metrics:requests:total
```

### Service Mode (cmd/esi-proxy)

HTTP-Service erstellt eigene Redis-Verbindung:

```go
func main() {
    redisClient := redis.NewClient(&redis.Options{
        Addr: os.Getenv("REDIS_URL"),
        DB:   0,
    })
    
    esiClient := client.New(client.Config{
        Redis:     redisClient,
        KeyPrefix: "esi:",
        UserAgent: "ESI-Proxy/0.2.0",
    })
    
    // Start HTTP server...
}
```

## Konsequenzen

### Positiv

✅ **Flexibilität**: Caller kann Redis-Setup vollständig kontrollieren  
✅ **Testbarkeit**: Mock-Redis einfach injizierbar (testcontainers-go, miniredis)  
✅ **Ressourceneffizienz**: Mehrere Anwendungen teilen eine Redis-Instanz  
✅ **Production-Ready**: Unterstützt Redis Cluster, Sentinel, Cloud-Redis  
✅ **Key-Isolation**: Automatisches Prefixing verhindert Konflikte  
✅ **Observability**: Keys sind leicht zu identifizieren (`esi:*`)  

### Negativ

⚠️ **Setup-Komplexität**: Caller muss Redis-Client konfigurieren  
⚠️ **Dokumentationsaufwand**: Klare Beispiele für verschiedene Setups nötig  

### Neutral

- Library ist nicht für Redis-Verbindungsmanagement verantwortlich
- Caller muss Redis-Lifecycle (Connect, Disconnect, Health Checks) verwalten

## Implementierungsdetails

### Erforderliche Änderungen

1. **pkg/client/client.go**:
   - `Redis *redis.Client` als required field in Config
   - `KeyPrefix string` als optional field (default: "esi:")
   - Validation in `New()`: Redis darf nicht nil sein

2. **pkg/cache/manager.go**:
   - Nimmt Redis-Client + Prefix im Constructor
   - Alle Cache-Keys verwenden `prefix + "cache:" + key`

3. **pkg/ratelimit/tracker.go**:
   - Nimmt Redis-Client + Prefix im Constructor
   - Keys: `prefix + "ratelimit:*"`

4. **README.md**:
   - Beispiele für Shared Redis Setup
   - Beispiele für verschiedene Redis-Topologien
   - Docker Compose Beispiel

5. **Tests**:
   - Integration Tests mit testcontainers-go
   - Unit Tests mit miniredis (In-Memory Mock)

### Beispiel-Integration (eve-o-provit)

```go
// Shared Redis für gesamte Anwendung
redisClient := redis.NewClient(&redis.Options{
    Addr:     "localhost:6379",
    Password: "",
    DB:       0,
})

// ESI Client mit Prefix "esi:"
esiClient := client.New(client.Config{
    Redis:     redisClient,
    KeyPrefix: "esi:",
    UserAgent: "EVE-O-Provit/1.0",
})

// Anwendung nutzt gleiche Redis mit anderem Prefix "app:"
sessionKey := "app:session:" + userID
redisClient.Set(ctx, sessionKey, sessionData, 24*time.Hour)
```

### Test-Setup

```go
// Integration Test mit testcontainers
func TestESIClientIntegration(t *testing.T) {
    ctx := context.Background()
    
    // Start Redis Container
    redisContainer, err := testcontainers.GenericContainer(ctx, 
        testcontainers.GenericContainerRequest{
            ContainerRequest: testcontainers.ContainerRequest{
                Image:        "redis:7-alpine",
                ExposedPorts: []string{"6379/tcp"},
            },
            Started: true,
        })
    require.NoError(t, err)
    defer redisContainer.Terminate(ctx)
    
    // Get Redis address
    host, _ := redisContainer.Host(ctx)
    port, _ := redisContainer.MappedPort(ctx, "6379")
    
    redisClient := redis.NewClient(&redis.Options{
        Addr: fmt.Sprintf("%s:%s", host, port.Port()),
    })
    
    // Create ESI Client
    client := client.New(client.Config{
        Redis:     redisClient,
        KeyPrefix: "test:esi:",
    })
    
    // Run tests...
}
```

## Alternativen (verworfen)

### Redis Connection String
Library erhält nur Connection-String und erstellt intern Client.

**Verworfen weil**:
- Keine Unterstützung für Redis Cluster/Sentinel
- Schwer testbar (keine Injection möglich)
- Caller kann Redis-Optionen nicht konfigurieren

### Optional Redis
Redis ist optional, Memory-Cache als Fallback.

**Verworfen weil**:
- Rate Limiting (ADR-006) ERFORDERT persistenten State
- ESI Error-Limit Tracking funktioniert nur mit shared State
- Distributed Deployments unmöglich

## Offene Fragen

- **Redis DB Selection**: Soll Library verschiedene DBs unterstützen oder nur DB 0?
  → **Entscheidung**: Caller kann `redis.Client` mit beliebiger DB konfigurieren
  
- **Redis Cluster**: Spezielle Unterstützung nötig?
  → **Entscheidung**: `*redis.Client` Interface unterstützt bereits Cluster via `redis.NewClusterClient()`

- **Key-TTL Strategy**: Wer bestimmt TTL für Cache-Keys?
  → **Entscheidung**: ADR-007 definiert TTL-Logik (ESI expires Header)

## Referenzen

- **ADR-005**: ESI Client Architecture (Hybrid Design)
- **ADR-006**: ESI Error & Rate Limit Handling (Persistent State Required)
- **ADR-007**: ESI Caching Strategy (Redis als Primary Cache)
- **ADR-009 (eve-o-provit)**: Shared Redis Infrastructure
- [go-redis Documentation](https://redis.uptrace.dev/)
- [testcontainers-go](https://golang.testcontainers.org/)
- [miniredis](https://github.com/alicebob/miniredis) - In-Memory Redis Mock

## Änderungshistorie

| Datum      | Änderung                          | Autor |
|------------|-----------------------------------|-------|
| 2025-10-27 | Initial proposal                  | AI    |
