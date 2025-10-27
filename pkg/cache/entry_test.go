package cache

import (
	"testing"
	"time"
)

func TestCacheEntry_IsExpired(t *testing.T) {
	tests := []struct {
		name    string
		expires time.Time
		want    bool
	}{
		{
			name:    "expired entry",
			expires: time.Now().Add(-1 * time.Hour),
			want:    true,
		},
		{
			name:    "valid entry",
			expires: time.Now().Add(1 * time.Hour),
			want:    false,
		},
		{
			name:    "just expired",
			expires: time.Now().Add(-1 * time.Second),
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := &CacheEntry{
				Expires: tt.expires,
			}
			if got := entry.IsExpired(); got != tt.want {
				t.Errorf("IsExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCacheEntry_TTL(t *testing.T) {
	tests := []struct {
		name    string
		expires time.Time
		wantMin time.Duration
		wantMax time.Duration
	}{
		{
			name:    "one hour remaining",
			expires: time.Now().Add(1 * time.Hour),
			wantMin: 59 * time.Minute,
			wantMax: 61 * time.Minute,
		},
		{
			name:    "already expired",
			expires: time.Now().Add(-1 * time.Hour),
			wantMin: 0,
			wantMax: 0,
		},
		{
			name:    "5 minutes remaining",
			expires: time.Now().Add(5 * time.Minute),
			wantMin: 4*time.Minute + 59*time.Second,
			wantMax: 5*time.Minute + 1*time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := &CacheEntry{
				Expires: tt.expires,
			}
			got := entry.TTL()
			if got < tt.wantMin || got > tt.wantMax {
				t.Errorf("TTL() = %v, want between %v and %v", got, tt.wantMin, tt.wantMax)
			}
		})
	}
}
