package cache

import (
	"bytes"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestResponseToEntry(t *testing.T) {
	tests := []struct {
		name    string
		resp    *http.Response
		wantErr bool
	}{
		{
			name: "valid response with all headers",
			resp: &http.Response{
				StatusCode: 200,
				Header: http.Header{
					"Expires":       []string{time.Now().Add(1 * time.Hour).Format(http.TimeFormat)},
					"Last-Modified": []string{time.Now().Add(-1 * time.Hour).Format(http.TimeFormat)},
					"ETag":          []string{`"abc123"`},
					"Content-Type":  []string{"application/json"},
				},
				Body: io.NopCloser(bytes.NewReader([]byte(`{"test": "data"}`))),
			},
			wantErr: false,
		},
		{
			name: "response without expires header",
			resp: &http.Response{
				StatusCode: 200,
				Header: http.Header{
					"Content-Type": []string{"application/json"},
				},
				Body: io.NopCloser(bytes.NewReader([]byte(`{"test": "data"}`))),
			},
			wantErr: false,
		},
		{
			name:    "nil response",
			resp:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry, err := ResponseToEntry(tt.resp)
			if (err != nil) != tt.wantErr {
				t.Errorf("ResponseToEntry() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if entry == nil {
					t.Fatal("ResponseToEntry() returned nil entry")
				}

				// Verify body was read and restored
				if tt.resp != nil && tt.resp.Body != nil {
					body, _ := io.ReadAll(tt.resp.Body)
					if len(body) == 0 {
						t.Error("Response body was not restored")
					}
				}

				// Verify status code
				if entry.StatusCode != tt.resp.StatusCode {
					t.Errorf("StatusCode = %v, want %v", entry.StatusCode, tt.resp.StatusCode)
				}

				// Verify ETag
				expectedETag := tt.resp.Header.Get("ETag")
				if entry.ETag != expectedETag {
					t.Errorf("ETag = %v, want %v", entry.ETag, expectedETag)
				}

				// Verify expires was set (either from header or default)
				if entry.Expires.IsZero() {
					t.Error("Expires time was not set")
				}
			}
		})
	}
}

func TestParseExpires(t *testing.T) {
	now := time.Now()
	futureTime := now.Add(1 * time.Hour)
	pastTime := now.Add(-1 * time.Hour)

	tests := []struct {
		name         string
		headers      http.Header
		wantWithin   time.Duration // Allow some tolerance for timing
		expectFuture bool
	}{
		{
			name: "valid expires header",
			headers: http.Header{
				"Expires": []string{futureTime.Format(http.TimeFormat)},
			},
			wantWithin:   2 * time.Second,
			expectFuture: true,
		},
		{
			name:         "no expires header",
			headers:      http.Header{},
			wantWithin:   2 * time.Second,
			expectFuture: true,
		},
		{
			name: "invalid expires header",
			headers: http.Header{
				"Expires": []string{"not a valid date"},
			},
			wantWithin:   2 * time.Second,
			expectFuture: true,
		},
		{
			name: "expires in the past",
			headers: http.Header{
				"Expires": []string{pastTime.Format(http.TimeFormat)},
			},
			wantWithin:   2 * time.Second,
			expectFuture: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseExpires(tt.headers)

			if tt.expectFuture && got.Before(now) {
				t.Errorf("parseExpires() = %v, expected time in the future", got)
			}

			// For valid future times, check it's within expected range
			if tt.name == "valid expires header" {
				diff := got.Sub(futureTime)
				if diff < -tt.wantWithin || diff > tt.wantWithin {
					t.Errorf("parseExpires() = %v, want approximately %v (diff: %v)",
						got, futureTime, diff)
				}
			}

			// For default TTL cases
			if tt.name == "no expires header" || tt.name == "invalid expires header" {
				expected := now.Add(DefaultTTL)
				diff := got.Sub(expected)
				if diff < -tt.wantWithin || diff > tt.wantWithin {
					t.Errorf("parseExpires() = %v, want approximately %v (diff: %v)",
						got, expected, diff)
				}
			}
		})
	}
}

func TestShouldMakeConditionalRequest(t *testing.T) {
	tests := []struct {
		name  string
		entry *CacheEntry
		want  bool
	}{
		{
			name:  "nil entry",
			entry: nil,
			want:  false,
		},
		{
			name: "entry with ETag",
			entry: &CacheEntry{
				ETag: `"abc123"`,
			},
			want: true,
		},
		{
			name: "entry with Last-Modified",
			entry: &CacheEntry{
				LastModified: time.Now(),
			},
			want: true,
		},
		{
			name: "entry with both ETag and Last-Modified",
			entry: &CacheEntry{
				ETag:         `"abc123"`,
				LastModified: time.Now(),
			},
			want: true,
		},
		{
			name: "entry without ETag or Last-Modified",
			entry: &CacheEntry{
				Data: []byte("data"),
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShouldMakeConditionalRequest(tt.entry); got != tt.want {
				t.Errorf("ShouldMakeConditionalRequest() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAddConditionalHeaders(t *testing.T) {
	tests := []struct {
		name       string
		entry      *CacheEntry
		wantHeader string
		wantValue  string
	}{
		{
			name: "add If-None-Match with ETag",
			entry: &CacheEntry{
				ETag: `"abc123"`,
			},
			wantHeader: "If-None-Match",
			wantValue:  `"abc123"`,
		},
		{
			name: "add If-Modified-Since with Last-Modified",
			entry: &CacheEntry{
				LastModified: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
			},
			wantHeader: "If-Modified-Since",
			wantValue:  "Sun, 01 Jan 2023 12:00:00 GMT",
		},
		{
			name: "prefer ETag over Last-Modified",
			entry: &CacheEntry{
				ETag:         `"abc123"`,
				LastModified: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
			},
			wantHeader: "If-None-Match",
			wantValue:  `"abc123"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "https://example.com", nil)
			AddConditionalHeaders(req, tt.entry)

			if tt.wantHeader != "" {
				got := req.Header.Get(tt.wantHeader)
				if got != tt.wantValue {
					t.Errorf("Header %s = %v, want %v", tt.wantHeader, got, tt.wantValue)
				}
			}
		})
	}
}

func TestAddConditionalHeaders_NilInputs(t *testing.T) {
	// Should not panic with nil inputs
	AddConditionalHeaders(nil, &CacheEntry{ETag: "test"})
	AddConditionalHeaders(&http.Request{}, nil)
}
