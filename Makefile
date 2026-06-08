.PHONY: help test test-coverage lint fmt vet clean deps tidy release-check release

GO := go
GOFLAGS := -v

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

test: ## Run tests
	@echo "Running tests..."
	$(GO) test -v -race -coverprofile=coverage.out ./...

test-coverage: test ## Run tests with coverage report
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

lint: ## Run linter
	@echo "Running linter..."
	golangci-lint run ./...

fmt: ## Format code
	@echo "Formatting code..."
	$(GO) fmt ./...

vet: ## Run go vet
	@echo "Running go vet..."
	$(GO) vet ./...

clean: ## Clean build artifacts
	@echo "Cleaning..."
	rm -f coverage.out coverage.html

deps: ## Download dependencies
	@echo "Downloading dependencies..."
	$(GO) mod download

tidy: ## Tidy go.mod
	@echo "Tidying go.mod..."
	$(GO) mod tidy

release-check: ## Prüft, ob der [Unreleased]-Block Einträge für ein Release hat
	@awk '/^## \[Unreleased\]/{f=1;next} /^## \[/{if(f)exit} f&&/[^[:space:]]/{found=1} END{exit !found}' CHANGELOG.md \
		|| { echo "[make release-check] ERROR: [Unreleased] ist leer — nichts zu releasen" >&2; exit 1; }
	@echo "[make release-check] ✅ [Unreleased] enthält Einträge"

release: release-check ## Version bump: [Unreleased] -> [VERSION] (Beispiel: make release VERSION=0.7.1)
	@if [ -z "$(VERSION)" ]; then \
		echo "[make release] ERROR: VERSION Parameter fehlt (Beispiel: make release VERSION=0.7.1)" >&2; \
		exit 1; \
	fi
	@echo "$(VERSION)" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$$' \
		|| { echo "[make release] ERROR: VERSION '$(VERSION)' ist kein SemVer (X.Y.Z)" >&2; exit 1; }
	@grep -qE '^## \[$(VERSION)\]' CHANGELOG.md \
		&& { echo "[make release] ERROR: Version $(VERSION) steht bereits im CHANGELOG" >&2; exit 1; } || true
	@echo "[make release] Bump auf $(VERSION)..."
	@sed -i "s/^## \[Unreleased\]/## [Unreleased]\n\n## [$(VERSION)] - $$(date +%Y-%m-%d)/" CHANGELOG.md
	@echo "[make release] ✅ CHANGELOG aktualisiert — nächste Schritte:"
	@echo "    git add CHANGELOG.md && git commit -m 'chore(release): v$(VERSION)'"
	@echo "    git tag v$(VERSION) && git push origin main v$(VERSION)"

.DEFAULT_GOAL := help
