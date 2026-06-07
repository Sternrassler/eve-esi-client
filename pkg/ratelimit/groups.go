package ratelimit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
)

// ESI group rate limiting (X-Ratelimit-* headers, rollout ab Oktober 2025).
//
// Jede Route-Gruppe × Consumer (ApplicationID:CharacterID bzw. SourceIP) hat
// einen Token-Bucket mit Floating Window (Header-Format "3600/15m"). Kosten:
// 2xx = 2 Tokens, 3xx = 1, 4xx = 5, 5xx = 0. Überschreitung ⇒ 429 mit
// Retry-After (Sekunden). Die Gruppen-Zugehörigkeit eines Endpoints steht nur
// in der Response (X-Ratelimit-Group) — der Tracker lernt das Mapping
// Endpoint→Gruppe aus beobachteten Antworten.
//
// Quelle: https://developers.eveonline.com/docs/services/esi/rate-limiting/
// Koexistenz: Routen ohne neues Rate Limiting tragen weiterhin die Legacy-
// X-ESI-Error-Limit-Header (siehe Tracker).

// Redis keys for group rate limit state.
const (
	// RedisKeyGroupPrefix + <group> → JSON GroupState, TTL = Window + Puffer.
	RedisKeyGroupPrefix = "esi:ratelimit:group:"
	// RedisKeyEndpointPrefix + <endpoint> → Gruppenname (gelerntes Mapping).
	RedisKeyEndpointPrefix = "esi:ratelimit:endpoint:"
)

const (
	// endpointMappingTTL begrenzt das gelernte Endpoint→Gruppe-Mapping.
	endpointMappingTTL = 24 * time.Hour

	// groupStateTTLBuffer wird auf die Window-Dauer aufgeschlagen, damit der
	// State das Floating Window sicher überdauert, aber nicht ewig lebt.
	groupStateTTLBuffer = 60 * time.Second

	// maxBlockWait ist die längste Wartezeit, die Gate für eine 429-Sperre
	// synchron aussitzt; längere Sperren werden als Fehler gemeldet.
	maxBlockWait = 60 * time.Second

	// groupThrottleSleep bremst Requests, wenn das Token-Budget einer Gruppe
	// zur Neige geht (Doku: "slow down as Remaining approaches zero").
	groupThrottleSleep = 1 * time.Second

	// fallbackRetryAfter greift, wenn eine 429-Antwort keinen (parsebaren)
	// Retry-After-Header trägt.
	fallbackRetryAfter = 60 * time.Second
)

// GroupRateLimitedError signalisiert eine 429-Sperre, die länger als
// maxBlockWait andauert — der Aufrufer soll nicht synchron warten.
type GroupRateLimitedError struct {
	Group      string
	RetryAfter time.Duration
}

func (e *GroupRateLimitedError) Error() string {
	return fmt.Sprintf("esi rate limit group %q blocked for %s (429 Retry-After)", e.Group, e.RetryAfter.Round(time.Second))
}

// GroupState ist der in Redis geteilte Zustand einer Rate-Limit-Gruppe.
type GroupState struct {
	Group         string    `json:"group"`
	Limit         int       `json:"limit"`
	WindowSeconds int       `json:"window_seconds"`
	Remaining     int       `json:"remaining"`
	UpdatedAt     time.Time `json:"updated_at"`
	// BlockedUntil ist gesetzt, wenn die letzte Antwort dieser Gruppe ein 429
	// war (jetzt + Retry-After). Eine spätere Nicht-429-Antwort löscht die
	// Sperre durch Überschreiben des States.
	BlockedUntil time.Time `json:"blocked_until,omitzero"`
}

// throttleThreshold liefert die Remaining-Schwelle, unter der Requests dieser
// Gruppe gebremst werden: 5 % des Limits, mindestens 10 Tokens.
func (s *GroupState) throttleThreshold() int {
	return max(s.Limit/20, 10)
}

// GroupTracker verfolgt die ESI-Gruppen-Rate-Limits (X-Ratelimit-*) und gated
// Requests pro Gruppe. Zustand liegt in Redis und ist damit über alle
// Client-Instanzen geteilt — wie beim Legacy-Tracker.
type GroupTracker struct {
	redis  *redis.Client
	logger zerolog.Logger
}

// NewGroupTracker creates a new group rate limit tracker.
func NewGroupTracker(redisClient *redis.Client, logger zerolog.Logger) *GroupTracker {
	return &GroupTracker{
		redis:  redisClient,
		logger: logger,
	}
}

// parseRateLimitHeader zerlegt das X-Ratelimit-Limit-Format "3600/15m" in
// Token-Limit und Window-Dauer.
func parseRateLimitHeader(value string) (int, time.Duration, error) {
	parts := strings.SplitN(value, "/", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("malformed X-Ratelimit-Limit %q (want <tokens>/<window>)", value)
	}
	limit, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, fmt.Errorf("parse X-Ratelimit-Limit tokens %q: %w", parts[0], err)
	}
	window, err := time.ParseDuration(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, fmt.Errorf("parse X-Ratelimit-Limit window %q: %w", parts[1], err)
	}
	if limit <= 0 || window <= 0 {
		return 0, 0, fmt.Errorf("non-positive X-Ratelimit-Limit %q", value)
	}
	return limit, window, nil
}

// Update wertet die X-Ratelimit-Header einer Antwort aus und aktualisiert den
// Gruppen-State plus das gelernte Endpoint→Gruppe-Mapping. Liefert (false, nil),
// wenn die Antwort keine Gruppen-Header trägt (Legacy-Route), (true, nil) bei
// erfolgreichem Update.
func (t *GroupTracker) Update(ctx context.Context, endpoint string, statusCode int, headers http.Header) (bool, error) {
	group := headers.Get("X-Ratelimit-Group")
	if group == "" {
		return false, nil
	}

	limit, window, err := parseRateLimitHeader(headers.Get("X-Ratelimit-Limit"))
	if err != nil {
		return false, err
	}

	remaining, err := strconv.Atoi(strings.TrimSpace(headers.Get("X-Ratelimit-Remaining")))
	if err != nil {
		return false, fmt.Errorf("parse X-Ratelimit-Remaining: %w", err)
	}

	now := time.Now()
	state := &GroupState{
		Group:         group,
		Limit:         limit,
		WindowSeconds: int(window.Seconds()),
		Remaining:     remaining,
		UpdatedAt:     now,
	}

	if statusCode == http.StatusTooManyRequests {
		retryAfter := fallbackRetryAfter
		if raw := headers.Get("Retry-After"); raw != "" {
			if seconds, perr := strconv.Atoi(strings.TrimSpace(raw)); perr == nil && seconds >= 0 {
				retryAfter = time.Duration(seconds) * time.Second
			}
		}
		state.BlockedUntil = now.Add(retryAfter)
	}

	payload, err := json.Marshal(state)
	if err != nil {
		return false, fmt.Errorf("marshal group state: %w", err)
	}

	pipe := t.redis.Pipeline()
	pipe.Set(ctx, RedisKeyGroupPrefix+group, payload, window+groupStateTTLBuffer)
	pipe.Set(ctx, RedisKeyEndpointPrefix+endpoint, group, endpointMappingTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return false, fmt.Errorf("store group rate limit state: %w", err)
	}

	switch {
	case !state.BlockedUntil.IsZero():
		t.logger.Warn().
			Str("group", group).
			Time("blocked_until", state.BlockedUntil).
			Msg("ESI group rate limited (429) - blocking group")
	case remaining < state.throttleThreshold():
		t.logger.Warn().
			Str("group", group).
			Int("remaining", remaining).
			Int("limit", limit).
			Msg("ESI group token budget low - requests will be throttled")
	default:
		t.logger.Debug().
			Str("group", group).
			Int("remaining", remaining).
			Int("limit", limit).
			Msg("ESI group rate limit state updated")
	}

	return true, nil
}

// Gate bremst bzw. blockiert einen Request anhand des Gruppen-States seines
// Endpoints. Unbekannte Endpoints/Gruppen (kein gelerntes Mapping, abgelaufener
// State) passieren ungebremst. Bei einer aktiven 429-Sperre wartet Gate bis zu
// maxBlockWait; längere Sperren liefern GroupRateLimitedError.
func (t *GroupTracker) Gate(ctx context.Context, endpoint string) error {
	group, err := t.redis.Get(ctx, RedisKeyEndpointPrefix+endpoint).Result()
	if errors.Is(err, redis.Nil) {
		return nil // Endpoint(-Gruppe) noch nicht beobachtet
	}
	if err != nil {
		return fmt.Errorf("get endpoint group mapping: %w", err)
	}

	payload, err := t.redis.Get(ctx, RedisKeyGroupPrefix+group).Result()
	if errors.Is(err, redis.Nil) {
		return nil // State abgelaufen ⇒ Window vorbei ⇒ Budget wieder voll
	}
	if err != nil {
		return fmt.Errorf("get group rate limit state: %w", err)
	}

	var state GroupState
	if err := json.Unmarshal([]byte(payload), &state); err != nil {
		return fmt.Errorf("parse group rate limit state: %w", err)
	}

	if wait := time.Until(state.BlockedUntil); wait > 0 {
		if wait > maxBlockWait {
			return &GroupRateLimitedError{Group: group, RetryAfter: wait}
		}
		t.logger.Warn().
			Str("group", group).
			Dur("wait", wait).
			Msg("ESI group blocked (429) - waiting for Retry-After")
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		return nil
	}

	if state.Remaining < state.throttleThreshold() {
		t.logger.Warn().
			Str("group", group).
			Int("remaining", state.Remaining).
			Msg("ESI group token budget low - throttling request")
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(groupThrottleSleep):
		}
	}

	return nil
}
