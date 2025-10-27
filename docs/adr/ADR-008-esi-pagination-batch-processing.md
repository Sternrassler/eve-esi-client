# ADR-008: ESI Pagination & Batch Processing

**Status**: Proposed  
**Datum**: 2025-10-27  
**Autor**: AI (basierend auf ESI Dokumentation)  
**Kontext**: ESI Pagination, Parallel Fetching & Batch Processing  
**Supersedes**: -  
**Superseded By**: -

## Kontext

Viele ESI Endpoints liefern große Datasets, die über mehrere Seiten verteilt sind:

### ESI Pagination Mechanism

**HTTP Header:**
- `X-Pages: 42` - Gesamtanzahl der Seiten

**Query Parameter:**
- `?page=1` - Seitennummer (1-basiert)

**Beispiel Endpoint:**
```
GET /v1/markets/10000002/orders/?page=1
Response Headers:
  X-Pages: 15
  Content-Type: application/json
  
GET /v1/markets/10000002/orders/?page=2
...
GET /v1/markets/10000002/orders/?page=15
```

### ESI Best Practices für Pagination

**Aus ESI Dokumentation:**
1. **Spread Load**: Langsamer, konsistenter Traffic bevorzugt (nicht spiky)
2. **Parallel Requests**: Erlaubt, aber Rate Limit beachten
3. **Error Handling**: Eine fehlgeschlagene Seite sollte nicht alles blockieren
4. **Caching**: Jede Seite hat eigenen Cache TTL

### Problem

Ohne strukturiertes Pagination Handling:
1. **Ineffizienz**: Sequential Fetching → hohe Latenz
2. **Error Amplification**: Ein Fehler stoppt gesamten Batch
3. **Rate Limit Überschreitung**: Zu viele parallele Requests
4. **Unvollständige Daten**: Missing Pages bei Fehlern

## Entscheidung

Wir implementieren ein **intelligentes Pagination & Batch Processing System** mit kontrollierten Parallelität:

### 1. Pagination Metadata

```go
type PaginationInfo struct {
    TotalPages   int       `json:"total_pages"`   // From X-Pages header
    CurrentPage  int       `json:"current_page"`  // Requested page
    PageSize     int       `json:"page_size"`     // Items per page (if known)
    TotalItems   int       `json:"total_items"`   // Total count (if available)
    HasMore      bool      `json:"has_more"`      // More pages available
    FetchedAt    time.Time `json:"fetched_at"`    // When metadata was retrieved
}

func (p *PaginationInfo) RemainingPages() int {
    if p.CurrentPage >= p.TotalPages {
        return 0
    }
    return p.TotalPages - p.CurrentPage
}

func (p *PaginationInfo) PageNumbers() []int {
    pages := make([]int, 0, p.TotalPages)
    for i := 1; i <= p.TotalPages; i++ {
        pages = append(pages, i)
    }
    return pages
}
```

### 2. Page Fetcher (Single Page)

```go
type PageRequest struct {
    Endpoint    string
    PageNumber  int
    QueryParams url.Values
    CacheKey    CacheKey
}

type PageResult struct {
    PageNumber   int
    Data         []byte
    Error        error
    StatusCode   int
    Pagination   PaginationInfo
    CachedAt     time.Time
    FromCache    bool
}

func (c *ESIClient) FetchPage(ctx context.Context, req PageRequest) (*PageResult, error) {
    // Build request with page parameter
    params := req.QueryParams
    if params == nil {
        params = url.Values{}
    }
    params.Set("page", strconv.Itoa(req.PageNumber))
    
    httpReq, err := c.buildRequest("GET", req.Endpoint, params)
    if err != nil {
        return nil, fmt.Errorf("build request: %w", err)
    }
    
    // Fetch with cache
    resp, err := c.DoWithCache(httpReq, req.CacheKey)
    if err != nil {
        return &PageResult{
            PageNumber: req.PageNumber,
            Error:      err,
        }, nil // Don't fail, return error in result
    }
    defer resp.Body.Close()
    
    // Read body
    body, err := io.ReadAll(resp.Body)
    if err != nil {
        return &PageResult{
            PageNumber: req.PageNumber,
            Error:      fmt.Errorf("read body: %w", err),
        }, nil
    }
    
    // Parse X-Pages header
    totalPages := 1
    if xPages := resp.Header.Get("X-Pages"); xPages != "" {
        if pages, err := strconv.Atoi(xPages); err == nil {
            totalPages = pages
        }
    }
    
    return &PageResult{
        PageNumber: req.PageNumber,
        Data:       body,
        StatusCode: resp.StatusCode,
        Pagination: PaginationInfo{
            TotalPages:  totalPages,
            CurrentPage: req.PageNumber,
            HasMore:     req.PageNumber < totalPages,
            FetchedAt:   time.Now(),
        },
        FromCache: resp.Header.Get("X-Cache") == "HIT",
    }, nil
}
```

### 3. Parallel Page Fetcher (Worker Pool)

```go
type BatchFetchConfig struct {
    MaxConcurrency int           // Max parallel requests
    MaxRetries     int           // Retry failed pages
    Timeout        time.Duration // Per-page timeout
    RateLimit      int           // Requests per second
}

type BatchFetcher struct {
    client *ESIClient
    config BatchFetchConfig
    
    // Rate limiting
    limiter *rate.Limiter
    
    // Metrics
    mutex          sync.Mutex
    pagesSuccess   int
    pagesFailed    int
    pagesFromCache int
}

func NewBatchFetcher(client *ESIClient, config BatchFetchConfig) *BatchFetcher {
    return &BatchFetcher{
        client:  client,
        config:  config,
        limiter: rate.NewLimiter(rate.Limit(config.RateLimit), config.RateLimit),
    }
}

func (bf *BatchFetcher) FetchAllPages(ctx context.Context, endpoint string, params url.Values) ([]*PageResult, error) {
    // Step 1: Fetch first page to get total pages
    firstPage, err := bf.client.FetchPage(ctx, PageRequest{
        Endpoint:    endpoint,
        PageNumber:  1,
        QueryParams: params,
    })
    if err != nil {
        return nil, fmt.Errorf("fetch first page: %w", err)
    }
    
    totalPages := firstPage.Pagination.TotalPages
    log.Info().
        Int("total_pages", totalPages).
        Str("endpoint", endpoint).
        Msg("Starting paginated fetch")
    
    // Step 2: Create page requests for remaining pages
    pageRequests := make([]PageRequest, totalPages)
    pageRequests[0] = PageRequest{
        Endpoint:    endpoint,
        PageNumber:  1,
        QueryParams: params,
    }
    
    for i := 2; i <= totalPages; i++ {
        pageRequests[i-1] = PageRequest{
            Endpoint:    endpoint,
            PageNumber:  i,
            QueryParams: params,
        }
    }
    
    // Step 3: Fetch pages in parallel with worker pool
    results := bf.fetchPagesParallel(ctx, pageRequests)
    
    // Step 4: Inject first page result (already fetched)
    results[0] = firstPage
    
    return results, nil
}

func (bf *BatchFetcher) fetchPagesParallel(ctx context.Context, requests []PageRequest) []*PageResult {
    results := make([]*PageResult, len(requests))
    resultsMutex := sync.Mutex{}
    
    // Worker pool with semaphore
    sem := make(chan struct{}, bf.config.MaxConcurrency)
    var wg sync.WaitGroup
    
    for i, req := range requests {
        // Skip first page (already fetched)
        if req.PageNumber == 1 {
            continue
        }
        
        wg.Add(1)
        go func(index int, pageReq PageRequest) {
            defer wg.Done()
            
            // Acquire semaphore
            sem <- struct{}{}
            defer func() { <-sem }()
            
            // Rate limiting
            if err := bf.limiter.Wait(ctx); err != nil {
                log.Error().Err(err).Msg("Rate limiter error")
                return
            }
            
            // Fetch with timeout
            pageCtx, cancel := context.WithTimeout(ctx, bf.config.Timeout)
            defer cancel()
            
            result, err := bf.client.FetchPage(pageCtx, pageReq)
            if err != nil {
                log.Error().
                    Err(err).
                    Int("page", pageReq.PageNumber).
                    Msg("Failed to fetch page")
                result = &PageResult{
                    PageNumber: pageReq.PageNumber,
                    Error:      err,
                }
            }
            
            // Store result
            resultsMutex.Lock()
            results[index] = result
            bf.updateMetrics(result)
            resultsMutex.Unlock()
            
            log.Debug().
                Int("page", pageReq.PageNumber).
                Bool("from_cache", result.FromCache).
                Msg("Page fetched")
            
        }(i, req)
    }
    
    wg.Wait()
    
    log.Info().
        Int("total", len(requests)).
        Int("success", bf.pagesSuccess).
        Int("failed", bf.pagesFailed).
        Int("cached", bf.pagesFromCache).
        Msg("Batch fetch completed")
    
    return results
}

func (bf *BatchFetcher) updateMetrics(result *PageResult) {
    bf.mutex.Lock()
    defer bf.mutex.Unlock()
    
    if result.Error != nil {
        bf.pagesFailed++
    } else {
        bf.pagesSuccess++
        if result.FromCache {
            bf.pagesFromCache++
        }
    }
}
```

### 4. Retry Logic for Failed Pages

```go
func (bf *BatchFetcher) retryFailedPages(ctx context.Context, results []*PageResult) error {
    failedPages := make([]PageRequest, 0)
    
    for _, result := range results {
        if result.Error != nil {
            failedPages = append(failedPages, PageRequest{
                PageNumber: result.PageNumber,
            })
        }
    }
    
    if len(failedPages) == 0 {
        return nil // No failures
    }
    
    log.Warn().
        Int("failed_count", len(failedPages)).
        Msg("Retrying failed pages")
    
    for attempt := 1; attempt <= bf.config.MaxRetries; attempt++ {
        retryResults := bf.fetchPagesParallel(ctx, failedPages)
        
        // Update original results
        for i, retryResult := range retryResults {
            if retryResult.Error == nil {
                // Success - update original result
                pageNum := retryResult.PageNumber
                for j, orig := range results {
                    if orig.PageNumber == pageNum {
                        results[j] = retryResult
                        break
                    }
                }
                // Remove from failed list
                failedPages = append(failedPages[:i], failedPages[i+1:]...)
            }
        }
        
        if len(failedPages) == 0 {
            log.Info().Int("attempt", attempt).Msg("All failed pages recovered")
            return nil
        }
        
        // Exponential backoff
        backoff := time.Duration(attempt) * time.Second
        log.Debug().
            Dur("backoff", backoff).
            Int("remaining_failures", len(failedPages)).
            Msg("Backoff before next retry")
        time.Sleep(backoff)
    }
    
    return fmt.Errorf("%d pages failed after %d retries", len(failedPages), bf.config.MaxRetries)
}
```

### 5. Data Aggregation

```go
type AggregatedResult struct {
    Data         []json.RawMessage `json:"data"`
    TotalPages   int               `json:"total_pages"`
    SuccessPages int               `json:"success_pages"`
    FailedPages  []int             `json:"failed_pages"`
    Incomplete   bool              `json:"incomplete"`
    FetchedAt    time.Time         `json:"fetched_at"`
}

func (bf *BatchFetcher) AggregateResults(results []*PageResult) (*AggregatedResult, error) {
    aggregated := &AggregatedResult{
        Data:       make([]json.RawMessage, 0),
        TotalPages: len(results),
        FetchedAt:  time.Now(),
    }
    
    for _, result := range results {
        if result.Error != nil {
            aggregated.FailedPages = append(aggregated.FailedPages, result.PageNumber)
            aggregated.Incomplete = true
            continue
        }
        
        // Parse JSON array from page
        var pageData []json.RawMessage
        if err := json.Unmarshal(result.Data, &pageData); err != nil {
            log.Error().
                Err(err).
                Int("page", result.PageNumber).
                Msg("Failed to unmarshal page data")
            aggregated.FailedPages = append(aggregated.FailedPages, result.PageNumber)
            aggregated.Incomplete = true
            continue
        }
        
        // Append to aggregated data
        aggregated.Data = append(aggregated.Data, pageData...)
        aggregated.SuccessPages++
    }
    
    log.Info().
        Int("total_items", len(aggregated.Data)).
        Int("success_pages", aggregated.SuccessPages).
        Int("failed_pages", len(aggregated.FailedPages)).
        Bool("incomplete", aggregated.Incomplete).
        Msg("Results aggregated")
    
    return aggregated, nil
}
```

### 6. Streaming Interface (Large Datasets)

```go
// For very large datasets - stream pages instead of loading all into memory
type PageStream struct {
    fetcher *BatchFetcher
    pages   chan *PageResult
    errors  chan error
    ctx     context.Context
}

func (bf *BatchFetcher) StreamPages(ctx context.Context, endpoint string, params url.Values) (*PageStream, error) {
    stream := &PageStream{
        fetcher: bf,
        pages:   make(chan *PageResult, 10), // Buffer 10 pages
        errors:  make(chan error, 1),
        ctx:     ctx,
    }
    
    go stream.start(endpoint, params)
    
    return stream, nil
}

func (ps *PageStream) start(endpoint string, params url.Values) {
    defer close(ps.pages)
    defer close(ps.errors)
    
    // Fetch first page
    firstPage, err := ps.fetcher.client.FetchPage(ps.ctx, PageRequest{
        Endpoint:    endpoint,
        PageNumber:  1,
        QueryParams: params,
    })
    if err != nil {
        ps.errors <- fmt.Errorf("fetch first page: %w", err)
        return
    }
    
    ps.pages <- firstPage
    totalPages := firstPage.Pagination.TotalPages
    
    // Stream remaining pages
    sem := make(chan struct{}, ps.fetcher.config.MaxConcurrency)
    var wg sync.WaitGroup
    
    for page := 2; page <= totalPages; page++ {
        wg.Add(1)
        
        go func(pageNum int) {
            defer wg.Done()
            
            sem <- struct{}{}
            defer func() { <-sem }()
            
            result, err := ps.fetcher.client.FetchPage(ps.ctx, PageRequest{
                Endpoint:    endpoint,
                PageNumber:  pageNum,
                QueryParams: params,
            })
            if err != nil {
                log.Error().Err(err).Int("page", pageNum).Msg("Stream page fetch failed")
                return
            }
            
            ps.pages <- result
        }(page)
    }
    
    wg.Wait()
}

func (ps *PageStream) Next() (*PageResult, bool) {
    select {
    case result, ok := <-ps.pages:
        return result, ok
    case err := <-ps.errors:
        log.Error().Err(err).Msg("Stream error")
        return nil, false
    case <-ps.ctx.Done():
        return nil, false
    }
}
```

### 7. Progress Tracking

```go
type FetchProgress struct {
    TotalPages     int       `json:"total_pages"`
    FetchedPages   int       `json:"fetched_pages"`
    FailedPages    int       `json:"failed_pages"`
    CachedPages    int       `json:"cached_pages"`
    StartedAt      time.Time `json:"started_at"`
    EstimatedEnd   time.Time `json:"estimated_end"`
    PercentComplete float64   `json:"percent_complete"`
}

func (bf *BatchFetcher) GetProgress() FetchProgress {
    bf.mutex.Lock()
    defer bf.mutex.Unlock()
    
    total := bf.pagesSuccess + bf.pagesFailed
    percent := 0.0
    if total > 0 {
        percent = float64(bf.pagesSuccess) / float64(total) * 100
    }
    
    return FetchProgress{
        TotalPages:      total,
        FetchedPages:    bf.pagesSuccess,
        FailedPages:     bf.pagesFailed,
        CachedPages:     bf.pagesFromCache,
        PercentComplete: percent,
    }
}
```

### 8. Configuration Examples

```yaml
# config/esi-pagination.yaml
pagination:
  # Parallel Fetching
  max_concurrent_requests: 5     # Conservative (ESI Best Practice: spread load)
  requests_per_second: 10        # Rate limit
  
  # Timeouts
  page_timeout: 30s              # Per-page timeout
  total_timeout: 5m              # Total batch timeout
  
  # Retry
  max_retries: 3                 # Retry failed pages
  retry_backoff: 1s              # Initial backoff
  
  # Streaming
  stream_buffer_size: 10         # Pages to buffer
  
  # Memory Management
  max_batch_size: 100            # Max pages per batch
  use_streaming: true            # Use streaming for large datasets
```

### 9. Monitoring & Metrics

```go
// Prometheus Metrics
var (
    esiPaginationFetches = promauto.NewCounterVec(prometheus.CounterOpts{
        Name: "esi_pagination_fetches_total",
        Help: "Total paginated fetches",
    }, []string{"endpoint", "status"}) // success, partial, failed
    
    esiPaginationPages = promauto.NewHistogram(prometheus.HistogramOpts{
        Name:    "esi_pagination_pages",
        Help:    "Number of pages per fetch",
        Buckets: []float64{1, 5, 10, 25, 50, 100, 250, 500},
    })
    
    esiPaginationDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
        Name:    "esi_pagination_duration_seconds",
        Help:    "Duration of paginated fetches",
        Buckets: prometheus.DefBuckets,
    }, []string{"endpoint"})
    
    esiPaginationCacheHitRate = promauto.NewGaugeVec(prometheus.GaugeOpts{
        Name: "esi_pagination_cache_hit_rate",
        Help: "Cache hit rate for paginated endpoints",
    }, []string{"endpoint"})
)
```

### 10. High-Level API Example

```go
// Simple API for common use cases
func (c *ESIClient) GetMarketOrders(ctx context.Context, regionID int) ([]MarketOrder, error) {
    endpoint := fmt.Sprintf("/v1/markets/%d/orders/", regionID)
    
    // Create batch fetcher
    fetcher := NewBatchFetcher(c, BatchFetchConfig{
        MaxConcurrency: 5,
        MaxRetries:     3,
        Timeout:        30 * time.Second,
        RateLimit:      10,
    })
    
    // Fetch all pages
    results, err := fetcher.FetchAllPages(ctx, endpoint, url.Values{
        "order_type": []string{"all"},
    })
    if err != nil {
        return nil, fmt.Errorf("fetch pages: %w", err)
    }
    
    // Aggregate
    aggregated, err := fetcher.AggregateResults(results)
    if err != nil {
        return nil, fmt.Errorf("aggregate results: %w", err)
    }
    
    if aggregated.Incomplete {
        log.Warn().
            Ints("failed_pages", aggregated.FailedPages).
            Msg("Market orders fetch incomplete")
    }
    
    // Unmarshal to typed data
    var orders []MarketOrder
    if err := json.Unmarshal(aggregated.Data, &orders); err != nil {
        return nil, fmt.Errorf("unmarshal orders: %w", err)
    }
    
    return orders, nil
}
```

## Konsequenzen

### Positiv

✅ **Performance**: Parallele Page Fetches (5-10x schneller)  
✅ **Resilience**: Retry Logic für failed pages  
✅ **Efficiency**: Caching pro Seite (nicht ganzer Batch)  
✅ **Scalability**: Worker Pool mit kontrollierbarer Concurrency  
✅ **Memory Safe**: Streaming Interface für große Datasets  
✅ **Rate Limit Safe**: Request Rate Limiter (spread load)  

### Negativ

⚠️ **Complexity**: Worker Pool + Retry + Aggregation  
⚠️ **Partial Failures**: Incomplete Data möglich  
⚠️ **Memory Spikes**: Große Batches (→ Streaming nutzen)  

### Risiken

🔴 **Thundering Herd**: Viele Clients fetching same pages  
→ Mitigation: Shared Cache (Redis), Jitter in parallel requests

🟡 **Page Count Changes**: X-Pages ändert sich während Fetch  
→ Mitigation: Accept incomplete data, re-fetch on next interval

## Implementierung

### Phase 1 (v0.3.0): Basic Pagination
- Single page fetcher
- X-Pages header parsing
- Sequential fetching

### Phase 2 (v0.4.0): Parallel Fetching
- Worker pool implementation
- Rate limiting
- Retry logic

### Phase 3 (v0.4.1): Advanced Features
- Streaming interface
- Progress tracking
- Aggregation helpers

### Phase 4 (v0.5.0): Production Hardening
- Monitoring & Metrics
- Memory optimization
- Batch size tuning

## Referenzen

- [ESI Pagination](https://docs.esi.evetech.net/docs/esi_introduction.html)
- [ESI Best Practices - Spread Load](https://docs.esi.evetech.net/docs/best_practices.html)
- ADR-005: ESI Client Architecture
- ADR-006: ESI Error & Rate Limit Handling
- ADR-007: ESI Caching Strategy

## Tags

`#esi` `#pagination` `#parallel-processing` `#worker-pool` `#streaming` `#batch-processing`
