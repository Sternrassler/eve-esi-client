# Phase 1 Implementation Roadmap

**Target Version**: v0.2.0  
**Status**: Planning  
**Created**: 2025-10-27

## Overview

Phase 1 implementiert die Core ESI Client Infrastructure mit folgenden Komponenten:
- ✅ Rate Limit Tracker (Error Limiting) - **COMPLETED**
- ✅ Cache Manager (ETag + expires Header) - **COMPLETED**
- 🚀 ESI Client Core (Integration) - **READY TO START**
- Error Handling & Retry Logic
- Metrics & Observability
- Integration Tests & Release

**Phase A Status**: ✅ **COMPLETED** (2025-10-27)  
**Phase B Status**: 🚀 **READY TO START**

## Issues & Dependencies

### Issue #1: Rate Limit Tracker Implementation ✅
**Status**: ✅ **COMPLETED** (2025-10-27)  
**URL**: https://github.com/Sternrassler/eve-esi-client/issues/1  
**Labels**: `enhancement`, `phase-1`, `core`  
**Actual Time**: ~6 hours (PR #7)  

**Dependencies**:
- ✅ Keine (kann sofort gestartet werden)
- ✅ Redis (bereits in go.mod)

**Deliverables**:
- ✅ `pkg/ratelimit/state.go` - Rate Limit State Model
- ✅ `pkg/ratelimit/tracker.go` - Core Tracker Implementation
- ✅ Redis Keys: `esi:rate_limit:*`
- ✅ Header Parsing: `X-ESI-Error-Limit-Remain`, `X-ESI-Error-Limit-Reset`
- ✅ Request Gating Logic (Thresholds: 5, 20, 50)
- ✅ Tests (89.5% coverage) + Prometheus Metrics
- ✅ Integration Tests mit testcontainers-go
- ✅ Zerolog Structured Logging

**Blocks**:
- ~~#3 (ESI Client Core Integration)~~ → **UNBLOCKED**

---

### Issue #2: Cache Manager Implementation ✅
**Status**: ✅ **COMPLETED** (2025-10-27)  
**URL**: https://github.com/Sternrassler/eve-esi-client/issues/2  
**Labels**: `enhancement`, `phase-1`, `core`  
**Actual Time**: ~7 hours (PR #8)  

**Dependencies**:
- ✅ Keine (parallel zu #1 entwickelt)
- ✅ Redis (bereits in go.mod)

**Deliverables**:
- ✅ `pkg/cache/entry.go` - Immutable Cache Entry Model
- ✅ `pkg/cache/key.go` - Deterministic Cache Key Generation
- ✅ `pkg/cache/manager.go` - Redis Cache Manager
- ✅ `pkg/cache/http.go` - HTTP Integration (Expires, ETag, Last-Modified)
- ✅ `pkg/cache/metrics.go` - Prometheus Metrics (5 metrics)
- ✅ `pkg/cache/doc.go` - Package Documentation
- ✅ ETag Support (If-None-Match)
- ✅ Expires Header Parsing (5-min fallback)
- ✅ 304 Not Modified Handling
- ✅ Tests (85.6% coverage) + Integration Tests
- ✅ Example Code (`examples/cache-usage/main.go`)

**Blocks**:
- ~~#3 (ESI Client Core Integration)~~ → **UNBLOCKED**

---

### Issue #3: ESI Client Core Integration
**Status**: 🚀 **READY TO START** (UNBLOCKED 2025-10-27)  
**URL**: https://github.com/Sternrassler/eve-esi-client/issues/3  
**Labels**: `enhancement`, `phase-1`, `core`  
**Estimated Time**: 6-8 hours  

**Dependencies**:
- ✅ **UNBLOCKED**: #1 (Rate Limit Tracker) - COMPLETED
- ✅ **UNBLOCKED**: #2 (Cache Manager) - COMPLETED
- 🎯 Beide Komponenten fertig und getestet

**Deliverables**:
- `pkg/client/client.go` - Client Core Updates
- Component Integration (Rate Limiter + Cache)
- Request Flow Implementation (7 steps)
- Conditional Request Logic
- Error Classification
- User-Agent Enforcement
- Tests + Metrics

**Blocks**:
- #6 (Integration Tests & Release)

---

### Issue #4: Error Handling & Retry Logic
**Status**: Open  
**URL**: https://github.com/Sternrassler/eve-esi-client/issues/4  
**Labels**: `enhancement`, `phase-1`  
**Estimated Time**: 4-5 hours  

**Dependencies**:
- ⚠️ Kann parallel zu #3 entwickelt werden (separate Komponente)
- 🔗 Integration in #3 nach Fertigstellung

**Deliverables**:
- `pkg/client/retry.go` - Retry Config & Logic
- `pkg/client/errors.go` - Error Classification
- Exponential Backoff Implementation
- Retry Strategies per Error Class (4xx, 5xx, 520, Network)
- Jitter + Context Cancellation
- Tests + Metrics

**Blocks**:
- #6 (Integration Tests & Release)

---

### Issue #5: Metrics & Observability
**Status**: Open  
**URL**: https://github.com/Sternrassler/eve-esi-client/issues/5  
**Labels**: `enhancement`, `phase-1`, `observability`  
**Estimated Time**: 3-4 hours  

**Dependencies**:
- ⚠️ Kann parallel zu #3 entwickelt werden
- 🔗 Integration in alle Komponenten (#1, #2, #3, #4)

**Deliverables**:
- `pkg/metrics/metrics.go` - Prometheus Metrics Registry
- `pkg/logging/logger.go` - Zerolog Logger Setup
- Metrics: Rate Limit, Cache, Requests, Retries
- Structured Logging (Debug, Info, Warn, Error)
- Health Check Endpoints (/health, /ready)
- Tests

**Blocks**:
- #6 (Integration Tests & Release)

---

### Issue #6: Integration Tests & Release v0.2.0
**Status**: Open  
**URL**: https://github.com/Sternrassler/eve-esi-client/issues/6  
**Labels**: `enhancement`, `phase-1`, `release`  
**Estimated Time**: 6-8 hours  

**Dependencies**:
- ⚠️ **BLOCKED BY**: #1 (Rate Limit Tracker)
- ⚠️ **BLOCKED BY**: #2 (Cache Manager)
- ⚠️ **BLOCKED BY**: #3 (ESI Client Core)
- ⚠️ **BLOCKED BY**: #4 (Error Handling & Retry)
- ⚠️ **BLOCKED BY**: #5 (Metrics & Observability)
- 🔗 **ALLE vorherigen Issues müssen DONE sein**

**Deliverables**:
- Mock ESI Server (`internal/testutil/mock_esi.go`)
- Integration Tests (`tests/integration/client_test.go`)
- Example Code (`examples/library-usage/main.go`)
- README Vervollständigung
- Documentation (`docs/getting-started.md`, `docs/configuration.md`, etc.)
- CHANGELOG.md Update
- VERSION bump zu 0.2.0
- Git Tag v0.2.0
- GitHub Release

**Blocks**:
- Nothing (Final Phase 1 Issue)

---

## Dependency Graph

```
┌────────────────────────────────────────────────────────┐
│                     Parallel Development               │
│                                                        │
│  ┌─────────────────┐              ┌─────────────────┐  │
│  │   Issue #1      │              │   Issue #2      │  │
│  │ Rate Limiter    │              │ Cache Manager   │  │
│  │  (4-6h)         │              │   (5-7h)        │  │
│  └────────┬────────┘              └────────┬────────┘  │
│           │                                │           │
│           └────────────┬───────────────────┘           │
│                        │                               │
└────────────────────────┼───────────────────────────────┘
                         │
         ┌───────────────────────────────┐
         │       Issue #3                │
         │   ESI Client Core             │◄────────┐
         │   Integration                 │         │
         │   (6-8h)                      │         │
         │   BLOCKS ON: #1 + #2          │         │
         └───────────┬───────────────────┘         │
                     │                             │
         ┌───────────┼──────────────┐              │
         ▼           ▼              ▼              │
┌────────────┐  ┌─────────┐  ┌──────────────┐      │
│ Issue #4   │  │Issue #5 │  │  Issue #6    │      │
│Error Handle│  │Metrics  │  │ Integration  │      │
│& Retry     │  │& Logs   │  │ Tests &      │      │
│(4-5h)      │  │(3-4h)   │  │ Release      │      │
│            │  │         │  │ v0.2.0       │      │
│Parallel    │  │Parallel │  │ (6-8h)       │      │
│to #3       │  │to #3    │  │              │      │
└────────────┘  └─────────┘  │BLOCKS ON:    │      │
                             │#1,#2,#3,#4,#5│      │
                             └──────────────┘      │
                                    │              │
                                    └──────────────┘
```

## Implementation Strategy

### Phase A: Foundation (Parallel) ✅
**Duration**: ~6-7 hours (actual)  
**Status**: ✅ **COMPLETED** (2025-10-27)  
**Tasks**: 
- ✅ #1 (Rate Limit Tracker) - PR #7 merged, 89.5% coverage
- ✅ #2 (Cache Manager) - PR #8 merged, 85.6% coverage
- ✅ Both developed by GitHub Copilot in parallel

**Achievements**:
- ✅ Combined test coverage: 87.5% (foundation)
- ✅ Zero build errors
- ✅ All integration tests passing (testcontainers-go)
- ✅ Prometheus metrics implemented (8 total)
- ✅ Full ADR compliance verified

### Phase B: Integration 🚀
**Duration**: ~6-8 hours (estimated)  
**Status**: 🚀 **READY TO START** (UNBLOCKED 2025-10-27)  
**Tasks**:
- 🚀 Start #3 (ESI Client Core) - **NOW AVAILABLE**
- Simultaneously start #4 (Error Handling) - can be developed in parallel
- Simultaneously start #5 (Metrics) - can be developed in parallel

### Phase C: Finalization
**Duration**: ~6-8 hours  
**Tasks**:
- Wait for ALL previous issues (#1-#5) to be DONE
- Start #6 (Integration Tests & Release)
- Create v0.2.0 release

### Total Estimated Time
- Sequential: ~28-38 hours
- With 2 developers (parallel): ~18-24 hours
- With 3 developers (parallel): ~16-21 hours
- **Actual Phase A**: ~6-7 hours ✅

## Success Criteria

After Phase 1 (v0.2.0), eve-esi-client will be:
- ✅ **ESI-Compliant**: Rate Limiting, Caching, User-Agent enforcement
- ✅ **Production-Ready**: Tests (>75% coverage), Metrics, Logging
- ✅ **Documented**: README, Examples, GoDoc, ADRs
- ✅ **Usable**: As Go Library import
- ✅ **Observable**: Prometheus metrics, Health checks
- ✅ **Resilient**: Retry logic, Circuit breaker foundation

## Next Steps (Phase 2 - Future)

After v0.2.0 release:
- In-Memory Cache Layer (Performance optimization)
- Circuit Breaker Pattern (Advanced resilience)
- Pagination Support (Batch processing)
- Stale-While-Revalidate (Background refresh)
- HTTP Service Mode (Optional standalone proxy)

## References

- [ADR-005: ESI Client Architecture](docs/adr/ADR-005-esi-client-architecture.md)
- [ADR-006: ESI Error & Rate Limit Handling](docs/adr/ADR-006-esi-error-rate-limit-handling.md)
- [ADR-007: ESI Caching Strategy](docs/adr/ADR-007-esi-caching-strategy.md)
- [ADR-008: ESI Pagination & Batch Processing](docs/adr/ADR-008-esi-pagination-batch-processing.md)
- [GitHub Project](https://github.com/Sternrassler/eve-esi-client)
- [ESI Documentation](https://docs.esi.evetech.net/)
