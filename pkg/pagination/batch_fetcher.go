// Package pagination provides parallel batch fetching for paginated ESI endpoints
package pagination

import (
"context"
"fmt"
"sync"
"time"

"github.com/rs/zerolog/log"
)

// Config holds batch fetcher configuration
type Config struct {
// MaxConcurrency is the maximum number of parallel requests
// Recommendation: 10 workers for ESI (300 req/min = 5 req/s)
MaxConcurrency int
// Timeout per page fetch
Timeout time.Duration
// Buffer size for channels (default: estimated total pages)
BufferSize int
}

// DefaultConfig returns safe default configuration for ESI
func DefaultConfig() Config {
return Config{
MaxConcurrency: 10,
Timeout:        15 * time.Second,
BufferSize:     400,
}
}

// PageFetcher is the interface that ESI client must implement for single-page fetching
type PageFetcher interface {
// FetchPage fetches a single page and returns data + total page count
FetchPage(ctx context.Context, endpoint string, pageNum int) (data []byte, totalPages int, err error)
}

// PageResult represents the result of fetching a single page
type PageResult struct {
PageNumber int
Data       []byte
Error      error
}

// BatchFetcher handles parallel fetching of multiple pages
type BatchFetcher struct {
fetcher PageFetcher
config  Config
}

// NewBatchFetcher creates a new batch fetcher
func NewBatchFetcher(fetcher PageFetcher, config Config) *BatchFetcher {
if config.MaxConcurrency <= 0 {
config.MaxConcurrency = 10
}
if config.Timeout <= 0 {
config.Timeout = 15 * time.Second
}
if config.BufferSize <= 0 {
config.BufferSize = 400
}

return &BatchFetcher{
fetcher: fetcher,
config:  config,
}
}

// FetchAllPages fetches all pages of an endpoint in parallel using worker pool
// Returns map of pageNumber -> data for successful pages
func (bf *BatchFetcher) FetchAllPages(ctx context.Context, endpoint string) (map[int][]byte, error) {
start := time.Now()

// Fetch first page to get total page count
firstPageData, totalPages, err := bf.fetcher.FetchPage(ctx, endpoint, 1)
if err != nil {
return nil, fmt.Errorf("failed to fetch first page: %w", err)
}

log.Info().
Str("endpoint", endpoint).
Int("total_pages", totalPages).
Msg("Starting parallel page fetch")

// Single page optimization
if totalPages == 1 {
result := map[int][]byte{1: firstPageData}
log.Info().
Str("endpoint", endpoint).
Int("pages", 1).
Dur("duration", time.Since(start)).
Msg("Fetch complete (single page)")
return result, nil
}

// Create result map with first page
results := make(map[int][]byte)
results[1] = firstPageData
resultsMutex := sync.Mutex{}

// Create channels
pageQueue := make(chan int, bf.config.BufferSize)
pageResults := make(chan PageResult, bf.config.BufferSize)
errors := make(chan error, bf.config.MaxConcurrency)

// Fill page queue (skip page 1, already fetched)
go func() {
for page := 2; page <= totalPages; page++ {
pageQueue <- page
}
close(pageQueue)
}()

// Start worker pool
var wg sync.WaitGroup
for i := 0; i < bf.config.MaxConcurrency; i++ {
wg.Add(1)
go bf.worker(ctx, endpoint, pageQueue, pageResults, errors, &wg, i)
}

// Close results channel when all workers done
go func() {
wg.Wait()
close(pageResults)
close(errors)
}()

// Collect results
fetchedPages := 1 // First page already fetched
for result := range pageResults {
if result.Error != nil {
log.Warn().
Err(result.Error).
Int("page", result.PageNumber).
Msg("Page fetch failed")
continue
}

resultsMutex.Lock()
results[result.PageNumber] = result.Data
fetchedPages++
resultsMutex.Unlock()

// Progress logging every 50 pages
if fetchedPages%50 == 0 {
log.Info().
Int("fetched", fetchedPages).
Int("total", totalPages).
Float64("progress_pct", float64(fetchedPages)/float64(totalPages)*100).
Msg("Fetch progress")
}
}

// Check for errors
select {
case err := <-errors:
if err != nil {
log.Warn().
Err(err).
Int("fetched_pages", fetchedPages).
Int("total_pages", totalPages).
Msg("Worker error - returning partial results")
return results, fmt.Errorf("worker error (partial data: %d/%d pages): %w", fetchedPages, totalPages, err)
}
default:
}

log.Info().
Str("endpoint", endpoint).
Int("pages", fetchedPages).
Int("total", totalPages).
Dur("duration", time.Since(start)).
Msg("Fetch complete")

return results, nil
}

// worker processes pages from the queue
func (bf *BatchFetcher) worker(ctx context.Context, endpoint string, pageQueue <-chan int, results chan<- PageResult, errors chan<- error, wg *sync.WaitGroup, workerID int) {
defer wg.Done()
pagesProcessed := 0

for pageNum := range pageQueue {
// Check context cancellation
select {
case <-ctx.Done():
log.Debug().
Int("worker_id", workerID).
Int("pages_processed", pagesProcessed).
Msg("Worker stopping (context cancelled)")
return
default:
}

// Fetch page with timeout
pageCtx, cancel := context.WithTimeout(ctx, bf.config.Timeout)
data, _, err := bf.fetcher.FetchPage(pageCtx, endpoint, pageNum)
cancel()

if err != nil {
log.Warn().
Err(err).
Int("worker_id", workerID).
Int("page", pageNum).
Msg("Page fetch failed")

// Non-blocking error send
select {
case errors <- err:
default:
}
return
}

// Send result
select {
case results <- PageResult{
PageNumber: pageNum,
Data:       data,
Error:      nil,
}:
case <-ctx.Done():
log.Debug().
Int("worker_id", workerID).
Int("pages_processed", pagesProcessed).
Msg("Worker stopping (context cancelled after fetch)")
return
}

pagesProcessed++
}

if pagesProcessed > 0 {
log.Debug().
Int("worker_id", workerID).
Int("pages_processed", pagesProcessed).
Msg("Worker completed")
}
}
