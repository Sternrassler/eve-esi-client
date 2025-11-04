// Package pagination provides parallel batch fetching for ESI paginated endpoints.
//
// ESI uses X-Pages header to indicate total pages, and supports parallel requests
// within rate limits (300 req/min). This package implements a worker pool pattern
// to efficiently fetch all pages while respecting ESI constraints.
//
// Example usage:
//
//	config := pagination.DefaultConfig()
//	fetcher := pagination.NewBatchFetcher(esiClient, config)
//	results, err := fetcher.FetchAllPages(ctx, "/v1/markets/10000002/orders/")
//
// The batch fetcher:
//   - Fetches first page to determine total pages
//   - Spawns worker pool (default 10 workers)
//   - Distributes remaining pages across workers
//   - Collects results with progress logging
//   - Handles errors gracefully (returns partial data)
//
// See ADR-008 for architecture decisions.
package pagination
