# Release v0.2.0 - Manual Steps

This document contains the manual steps needed to complete the v0.2.0 release after the PR is merged.

## Prerequisites

- PR #6 (Integration Tests & Release v0.2.0) merged to `main`
- Local repository updated to latest `main`
- Push access to repository

## Step 1: Create Git Tag

```bash
# Update local main branch
git checkout main
git pull origin main

# Create annotated tag
git tag -a v0.2.0 -m "Release v0.2.0 - Phase 1: Core Infrastructure"

# Push tag to GitHub
git push origin v0.2.0
```

## Step 2: Create GitHub Release

1. Navigate to: https://github.com/Sternrassler/eve-esi-client/releases/new

2. Fill in release details:
   - **Tag**: `v0.2.0` (select from dropdown)
   - **Release title**: `v0.2.0 - Phase 1: Core Infrastructure`
   - **Description**: Copy the v0.2.0 section from CHANGELOG.md (see below)

3. **Release Description** (copy from CHANGELOG.md):

```markdown
## Release v0.2.0 - Phase 1: Core Infrastructure

Production-ready ESI client library with complete rate limiting, caching, and error handling.

### Added

**ESI Client Core** (`pkg/client/`)
- Complete ESI client with integrated rate limiting, caching, and error handling
- Automatic rate limit checking before requests
- Redis-backed response caching with ETag support
- Intelligent retry logic with exponential backoff (5xx, 520, network errors)
- No retry for 4xx client errors (protects error budget)
- Error classification: client, server, rate_limit, network
- Context support with timeout handling
- Prometheus metrics integration
- 8 comprehensive integration tests

**Mock ESI Server** (`internal/testutil/mock_esi.go`)
- Controllable mock server for testing
- Supports all ESI response types (200, 304, 429, 500, 520)
- Request tracking and conditional request support

**Integration Tests** (`tests/integration/`)
- Full request flow (Rate Limit → Cache → ESI → Cache Update)
- Cache hit and 304 Not Modified tests
- Rate limit blocking verification
- Retry logic validation
- All tests passing with Redis test containers

**Library Usage Example** (`examples/library-usage/`)
- Realistic market orders fetching example
- Error handling demonstration
- Complete README with best practices

**Comprehensive Documentation**
- `docs/getting-started.md` - Quick start guide
- `docs/configuration.md` - Configuration reference
- `docs/monitoring.md` - Prometheus metrics & alerting
- `docs/troubleshooting.md` - Common issues & debugging

### ESI Compliance

✅ Error rate limiting with automatic blocking  
✅ Cache respects Expires header (required)  
✅ Conditional requests with If-None-Match (ETag)  
✅ User-Agent header (required format enforced)  
✅ Spread load with rate limiting  

### Performance

- Efficient caching reduces ESI load by >60% (typical)
- P95 request latency < 1s with cache
- Automatic rate limit protection prevents IP bans
- Concurrent request support with configurable limits

### What's Next

Phase 2 will add:
- Pagination support with worker pools
- HTTP service mode (proxy)
- Advanced metrics and alerting

Full changelog: https://github.com/Sternrassler/eve-esi-client/blob/main/CHANGELOG.md
```

4. Options:
   - ✅ Check "Set as the latest release"
   - ✅ Check "Create a discussion for this release" (optional)

5. Click **"Publish release"**

## Step 3: Close Related Issues

Close the following issues with this comment:

```markdown
Completed in v0.2.0. 

All acceptance criteria met:
- ✅ Mock ESI server implemented
- ✅ Integration tests passing (8 tests)
- ✅ Example code and documentation complete
- ✅ All quality gates passing

See [CHANGELOG.md](https://github.com/Sternrassler/eve-esi-client/blob/main/CHANGELOG.md#020---2025-10-27) for full details.
```

**Issues to close:**
- #1 - Rate Limit Tracker
- #2 - Cache Manager  
- #3 - ESI Client Core
- #4 - Error Handling & Retry
- #5 - Metrics & Observability
- #6 - Integration Tests & Release v0.2.0

## Step 4: Milestone Management

1. Go to: https://github.com/Sternrassler/eve-esi-client/milestones

2. Close "Phase 1" milestone:
   - Verify all issues are closed
   - Click "Close milestone"

3. Create "Phase 2" milestone (optional):
   - Title: "Phase 2: Pagination & Service Mode"
   - Description: "Add pagination support and HTTP service mode"
   - Due date: (set as needed)

## Step 5: Verify Release

1. **Check GitHub Release Page:**
   - Visit: https://github.com/Sternrassler/eve-esi-client/releases
   - Verify v0.2.0 is listed as "Latest"
   - Verify description is formatted correctly

2. **Check Tag:**
   ```bash
   git fetch --tags
   git tag -l "v0.2.0"
   # Should show: v0.2.0
   ```

3. **Test Installation:**
   ```bash
   # In a test directory
   go get github.com/Sternrassler/eve-esi-client/pkg/client@v0.2.0
   ```

4. **Verify Documentation Links:**
   - Check that README links to new docs work
   - Verify example code is accessible

## Step 6: Announce Release (Optional)

Consider announcing the release:

1. **GitHub Discussions** (if enabled)
   - Create a discussion post highlighting key features
   - Link to release notes

2. **Social Media / Community** (if applicable)
   - EVE Online third-party developer Discord
   - Reddit r/Eve
   - Twitter/X

## Post-Release Checklist

- [ ] Git tag v0.2.0 created and pushed
- [ ] GitHub release published
- [ ] Issues #1-#6 closed
- [ ] Milestone "Phase 1" closed
- [ ] Milestone "Phase 2" created (optional)
- [ ] Release verified (tag exists, package installable)
- [ ] Documentation links verified
- [ ] Release announced (optional)

## Rollback Procedure (If Needed)

If critical issues are found immediately after release:

```bash
# Delete the tag locally
git tag -d v0.2.0

# Delete the tag remotely
git push origin :refs/tags/v0.2.0

# Delete the GitHub release (via web UI)
```

Then:
1. Fix the issue
2. Create a v0.2.1 patch release
3. Update CHANGELOG.md with fixes

## Next Steps

After completing the release:

1. Start working on Phase 2 issues:
   - Pagination support (#7 - to be created)
   - Service mode (#8 - to be created)

2. Monitor for issues:
   - Watch for bug reports
   - Review user feedback
   - Address critical issues promptly

3. Plan v0.3.0:
   - Review Phase 2 requirements
   - Update ADRs if needed
   - Create new issues and milestones

## Support

If you encounter any issues during the release process:
- Check GitHub Actions logs for CI failures
- Review CHANGELOG.md for completeness
- Verify all tests pass: `make test`
- Ensure linting passes: `make vet`

## License

MIT License - See [LICENSE](../LICENSE)
