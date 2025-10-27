package cache

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// CacheKey represents a unique identifier for a cached ESI response.
type CacheKey struct {
	// Endpoint is the ESI endpoint path (e.g., "/v4/markets/{region_id}/orders/")
	Endpoint string

	// PathParams are the path parameters (e.g., {"region_id": "10000002"})
	PathParams map[string]string

	// QueryParams are the query parameters (e.g., {"order_type": "all"})
	QueryParams url.Values

	// CharacterID is the character ID for authenticated endpoints (0 for public)
	CharacterID int64
}

// String generates a deterministic cache key string.
// Format: esi:endpoint:param1=val1:param2=val2:query1=val1:char=123456
//
// Example:
//   esi:/v4/markets/10000002/orders/:order_type=all:char=0
func (k CacheKey) String() string {
	parts := []string{"esi"}

	// Add endpoint (normalize path)
	endpoint := strings.Trim(k.Endpoint, "/")
	if endpoint != "" {
		parts = append(parts, endpoint)
	}

	// Add path params (sorted for determinism)
	if len(k.PathParams) > 0 {
		pathKeys := make([]string, 0, len(k.PathParams))
		for key := range k.PathParams {
			pathKeys = append(pathKeys, key)
		}
		sort.Strings(pathKeys)

		for _, key := range pathKeys {
			parts = append(parts, fmt.Sprintf("%s=%s", key, k.PathParams[key]))
		}
	}

	// Add query params (sorted for determinism)
	if len(k.QueryParams) > 0 {
		queryKeys := make([]string, 0, len(k.QueryParams))
		for key := range k.QueryParams {
			queryKeys = append(queryKeys, key)
		}
		sort.Strings(queryKeys)

		for _, key := range queryKeys {
			parts = append(parts, fmt.Sprintf("%s=%s", key, k.QueryParams.Get(key)))
		}
	}

	// Add character ID if authenticated
	if k.CharacterID > 0 {
		parts = append(parts, fmt.Sprintf("char=%d", k.CharacterID))
	}

	return strings.Join(parts, ":")
}
