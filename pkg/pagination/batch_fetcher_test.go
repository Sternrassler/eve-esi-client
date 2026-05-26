package pagination

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeFetcher is a thread-safe PageFetcher for testing.
type fakeFetcher struct {
	mu         sync.Mutex
	totalPages int
	// pageData maps pageNum -> data bytes; if absent, a default is generated.
	pageData map[int][]byte
	// pageErrors maps pageNum -> error to return for that page.
	pageErrors map[int]error
	callCount  int
}

func newFakeFetcher(totalPages int) *fakeFetcher {
	return &fakeFetcher{
		totalPages: totalPages,
		pageData:   make(map[int][]byte),
		pageErrors: make(map[int]error),
	}
}

func (f *fakeFetcher) setPageData(pageNum int, data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pageData[pageNum] = data
}

func (f *fakeFetcher) setPageError(pageNum int, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pageErrors[pageNum] = err
}

func (f *fakeFetcher) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.callCount
}

// FetchPage implements PageFetcher.
func (f *fakeFetcher) FetchPage(_ context.Context, _ string, pageNum int) ([]byte, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.callCount++

	if err, ok := f.pageErrors[pageNum]; ok {
		return nil, 0, err
	}

	if data, ok := f.pageData[pageNum]; ok {
		return data, f.totalPages, nil
	}

	// Default: synthesise deterministic page data.
	data := []byte(fmt.Sprintf(`{"page":%d}`, pageNum))
	return data, f.totalPages, nil
}

// ---------------------------------------------------------------------------
// 1. DefaultConfig / NewBatchFetcher clamping
// ---------------------------------------------------------------------------

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.MaxConcurrency != 10 {
		t.Errorf("MaxConcurrency: got %d, want 10", cfg.MaxConcurrency)
	}
	if cfg.Timeout != 15*time.Second {
		t.Errorf("Timeout: got %v, want 15s", cfg.Timeout)
	}
	if cfg.BufferSize != 400 {
		t.Errorf("BufferSize: got %d, want 400", cfg.BufferSize)
	}
}

func TestNewBatchFetcher_ClampsNonPositiveFields(t *testing.T) {
	tests := []struct {
		name string
		in   Config
		want Config
	}{
		{
			name: "zero values clamped to defaults",
			in:   Config{MaxConcurrency: 0, Timeout: 0, BufferSize: 0},
			want: Config{MaxConcurrency: 10, Timeout: 15 * time.Second, BufferSize: 400},
		},
		{
			name: "negative values clamped to defaults",
			in:   Config{MaxConcurrency: -5, Timeout: -1, BufferSize: -100},
			want: Config{MaxConcurrency: 10, Timeout: 15 * time.Second, BufferSize: 400},
		},
		{
			name: "positive values preserved",
			in:   Config{MaxConcurrency: 3, Timeout: 5 * time.Second, BufferSize: 50},
			want: Config{MaxConcurrency: 3, Timeout: 5 * time.Second, BufferSize: 50},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeFetcher(1)
			bf := NewBatchFetcher(f, tc.in)

			if bf.config.MaxConcurrency != tc.want.MaxConcurrency {
				t.Errorf("MaxConcurrency: got %d, want %d", bf.config.MaxConcurrency, tc.want.MaxConcurrency)
			}
			if bf.config.Timeout != tc.want.Timeout {
				t.Errorf("Timeout: got %v, want %v", bf.config.Timeout, tc.want.Timeout)
			}
			if bf.config.BufferSize != tc.want.BufferSize {
				t.Errorf("BufferSize: got %d, want %d", bf.config.BufferSize, tc.want.BufferSize)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 2. Single-page fast path
// ---------------------------------------------------------------------------

func TestFetchAllPages_SinglePage(t *testing.T) {
	want := []byte(`{"items":[1,2,3]}`)
	f := newFakeFetcher(1)
	f.setPageData(1, want)

	bf := NewBatchFetcher(f, DefaultConfig())
	results, err := bf.FetchAllPages(context.Background(), "/v1/test/")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 page in results, got %d", len(results))
	}
	if string(results[1]) != string(want) {
		t.Errorf("page 1 data: got %q, want %q", results[1], want)
	}
	if f.calls() != 1 {
		t.Errorf("fetcher called %d times, want 1", f.calls())
	}
}

// ---------------------------------------------------------------------------
// 3. Multiple pages — all pages present and correct
// ---------------------------------------------------------------------------

func TestFetchAllPages_MultiplePages(t *testing.T) {
	const total = 5
	f := newFakeFetcher(total)
	for i := 1; i <= total; i++ {
		f.setPageData(i, []byte(fmt.Sprintf(`{"page":%d,"data":"ok"}`, i)))
	}

	bf := NewBatchFetcher(f, DefaultConfig())
	results, err := bf.FetchAllPages(context.Background(), "/v1/markets/")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != total {
		t.Fatalf("expected %d pages, got %d", total, len(results))
	}
	for i := 1; i <= total; i++ {
		want := []byte(fmt.Sprintf(`{"page":%d,"data":"ok"}`, i))
		if string(results[i]) != string(want) {
			t.Errorf("page %d: got %q, want %q", i, results[i], want)
		}
	}
}

// ---------------------------------------------------------------------------
// 4. First-page error
// ---------------------------------------------------------------------------

func TestFetchAllPages_FirstPageError(t *testing.T) {
	f := newFakeFetcher(5)
	f.setPageError(1, errors.New("network timeout"))

	bf := NewBatchFetcher(f, DefaultConfig())
	results, err := bf.FetchAllPages(context.Background(), "/v1/test/")

	if results != nil {
		t.Errorf("expected nil map on first-page error, got %v", results)
	}
	if err == nil {
		t.Fatal("expected non-nil error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to fetch first page") {
		t.Errorf("error message %q does not contain 'failed to fetch first page'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// 5. Partial failure (one worker page fails)
// ---------------------------------------------------------------------------

func TestFetchAllPages_PartialFailure(t *testing.T) {
	const total = 5
	failedPage := 3

	f := newFakeFetcher(total)
	f.setPageError(failedPage, errors.New("upstream unavailable"))

	bf := NewBatchFetcher(f, DefaultConfig())
	results, err := bf.FetchAllPages(context.Background(), "/v1/markets/")

	// Error must be present and mention partial data.
	if err == nil {
		t.Fatal("expected non-nil error for partial failure, got nil")
	}
	if !strings.Contains(err.Error(), "partial data") {
		t.Errorf("error %q does not contain 'partial data'", err.Error())
	}

	// Failed page must be absent from results.
	if _, present := results[failedPage]; present {
		t.Errorf("page %d should be absent from results (it failed)", failedPage)
	}

	// Fewer than total pages must be in the map.
	if len(results) >= total {
		t.Errorf("expected < %d pages in partial results, got %d", total, len(results))
	}

	// Page 1 must always be present (it succeeded).
	if _, ok := results[1]; !ok {
		t.Error("page 1 should be present in results")
	}
}

// ---------------------------------------------------------------------------
// 6. Context cancellation — must not hang
// ---------------------------------------------------------------------------

func TestFetchAllPages_ContextCancellation(t *testing.T) {
	// Use a large totalPages so there are plenty of remaining pages in the queue.
	const total = 100
	f := newFakeFetcher(total)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	bf := NewBatchFetcher(f, DefaultConfig())

	done := make(chan struct{})
	go func() {
		_, _ = bf.FetchAllPages(ctx, "/v1/markets/")
		close(done)
	}()

	select {
	case <-done:
		// completed without hanging — pass
	case <-time.After(3 * time.Second):
		t.Fatal("FetchAllPages did not return within 3s after context cancellation")
	}
}
