package cache

import (
	"net/url"
	"testing"
)

func TestCacheKey_String(t *testing.T) {
	tests := []struct {
		name string
		key  CacheKey
		want string
	}{
		{
			name: "simple endpoint no params",
			key: CacheKey{
				Endpoint: "/v1/universe/types/",
			},
			want: "esi:v1/universe/types",
		},
		{
			name: "endpoint with path params",
			key: CacheKey{
				Endpoint:   "/v4/markets/{region_id}/orders/",
				PathParams: map[string]string{"region_id": "10000002"},
			},
			want: "esi:v4/markets/{region_id}/orders:region_id=10000002",
		},
		{
			name: "endpoint with query params",
			key: CacheKey{
				Endpoint: "/v1/markets/10000002/orders/",
				QueryParams: url.Values{
					"order_type": []string{"all"},
				},
			},
			want: "esi:v1/markets/10000002/orders:order_type=all",
		},
		{
			name: "endpoint with multiple query params (sorted)",
			key: CacheKey{
				Endpoint: "/v1/markets/10000002/orders/",
				QueryParams: url.Values{
					"order_type": []string{"all"},
					"page":       []string{"1"},
				},
			},
			want: "esi:v1/markets/10000002/orders:order_type=all:page=1",
		},
		{
			name: "authenticated endpoint",
			key: CacheKey{
				Endpoint:    "/v4/characters/{character_id}/assets/",
				CharacterID: 123456789,
			},
			want: "esi:v4/characters/{character_id}/assets:char=123456789",
		},
		{
			name: "complex key with all params",
			key: CacheKey{
				Endpoint:   "/v4/markets/{region_id}/orders/",
				PathParams: map[string]string{"region_id": "10000002"},
				QueryParams: url.Values{
					"order_type": []string{"all"},
					"page":       []string{"1"},
				},
				CharacterID: 123456789,
			},
			want: "esi:v4/markets/{region_id}/orders:region_id=10000002:order_type=all:page=1:char=123456789",
		},
		{
			name: "deterministic ordering with multiple path params",
			key: CacheKey{
				Endpoint: "/v1/some/endpoint/",
				PathParams: map[string]string{
					"param_z": "value_z",
					"param_a": "value_a",
					"param_m": "value_m",
				},
			},
			want: "esi:v1/some/endpoint:param_a=value_a:param_m=value_m:param_z=value_z",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.key.String()
			if got != tt.want {
				t.Errorf("CacheKey.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestCacheKey_Determinism ensures same input always produces same key
func TestCacheKey_Determinism(t *testing.T) {
	key := CacheKey{
		Endpoint: "/v1/markets/10000002/orders/",
		PathParams: map[string]string{
			"region_id": "10000002",
			"type_id":   "34",
		},
		QueryParams: url.Values{
			"order_type": []string{"all"},
			"page":       []string{"1"},
		},
		CharacterID: 123456789,
	}

	// Generate key multiple times
	results := make([]string, 10)
	for i := 0; i < 10; i++ {
		results[i] = key.String()
	}

	// All results should be identical
	first := results[0]
	for i, result := range results {
		if result != first {
			t.Errorf("result[%d] = %v, want %v (not deterministic)", i, result, first)
		}
	}
}
