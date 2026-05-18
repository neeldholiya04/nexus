# Nexus Makefile
# Usage: make <target>

BINARY      := nexus
CMD_DIR     := ./cmd/nexus
BUILD_DIR   := ./bin
GO          := go
GOFLAGS     := -trimpath
VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
VERSION_PKG := github.com/neeldholiya04/nexus/internal/cli/commands
LDFLAGS     := -ldflags="-s -w -X $(VERSION_PKG).version=$(VERSION)"

# Cross-compilation targets
GOOS_LINUX  := GOOS=linux GOARCH=amd64
GOOS_WIN    := GOOS=windows GOARCH=amd64
GOOS_MAC    := GOOS=darwin GOARCH=arm64

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

# =============================================================================
# Build
# =============================================================================

.PHONY: build
build: ## Build nexus binary for current OS
	@mkdir -p $(BUILD_DIR)
	$(GO) build $(GOFLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY) $(CMD_DIR)
	@echo "Built: $(BUILD_DIR)/$(BINARY)"

.PHONY: build-linux
build-linux: ## Build nexus binary for Linux amd64
	@mkdir -p $(BUILD_DIR)
	$(GOOS_LINUX) $(GO) build $(GOFLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-linux-amd64 $(CMD_DIR)
	@echo "Built: $(BUILD_DIR)/$(BINARY)-linux-amd64"

.PHONY: build-windows
build-windows: ## Build nexus binary for Windows amd64
	@mkdir -p $(BUILD_DIR)
	$(GOOS_WIN) $(GO) build $(GOFLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-windows-amd64.exe $(CMD_DIR)
	@echo "Built: $(BUILD_DIR)/$(BINARY)-windows-amd64.exe"

.PHONY: build-all
build-all: build-linux build-windows ## Build for all target platforms
	@echo "All platform builds complete."

.PHONY: install
install: ## Install nexus binary to $GOPATH/bin
	$(GO) install $(GOFLAGS) $(LDFLAGS) $(CMD_DIR)
	@echo "Installed: $(shell which nexus)"

# =============================================================================
# Development
# =============================================================================

.PHONY: run
run: ## Run nexus directly (pass ARGS="..." for subcommands)
	$(GO) run $(CMD_DIR) $(ARGS)

.PHONY: serve-stdio
serve-stdio: build ## Start MCP server in stdio mode (Claude Code)
	$(BUILD_DIR)/$(BINARY) serve --transport stdio

.PHONY: serve-sse
serve-sse: build ## Start MCP server in SSE mode (Claude Desktop)
	$(BUILD_DIR)/$(BINARY) serve --transport sse

.PHONY: embed
embed: build ## Run embedding backfill pipeline
	$(BUILD_DIR)/$(BINARY) embed

# =============================================================================
# Testing
# =============================================================================

.PHONY: test
test: ## Run all tests
	$(GO) test -v -race -timeout 60s ./...

.PHONY: test-unit
test-unit: ## Run unit tests only (skip integration)
	$(GO) test -v -race -timeout 30s -short ./...

.PHONY: test-cover
test-cover: ## Run tests with coverage report
	$(GO) test -v -race -timeout 60s -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

.PHONY: test-storage
test-storage: ## Run storage layer tests
	$(GO) test -v -race ./internal/storage/...

.PHONY: test-retrieval
test-retrieval: ## Run retrieval pipeline tests
	$(GO) test -v -race ./internal/retrieval/...

# =============================================================================
# Code quality
# =============================================================================

.PHONY: lint
lint: ## Run golangci-lint
	@which golangci-lint > /dev/null || (echo "Install: https://golangci-lint.run/usage/install/" && exit 1)
	golangci-lint run ./...

.PHONY: vet
vet: ## Run go vet
	$(GO) vet ./...

.PHONY: fmt
fmt: ## Format all Go source files
	$(GO) fmt ./...
	@which goimports > /dev/null && goimports -w . || true

.PHONY: tidy
tidy: ## Tidy and verify go.mod
	$(GO) mod tidy
	$(GO) mod verify

.PHONY: check
check: fmt vet lint test ## Run all quality checks (fmt + vet + lint + test)

# =============================================================================
# Database
# =============================================================================

.PHONY: db-shell
db-shell: ## Open sqlite3 shell on the Nexus database
	@sqlite3 $${NEXUS_STORAGE_DB_PATH:-$$HOME/.nexus/nexus.db}

.PHONY: db-reset
db-reset: ## DELETE the Nexus database (destructive!)
	@echo "WARNING: This will delete your Nexus database!"
	@read -p "Type 'yes' to confirm: " confirm && [ "$$confirm" = "yes" ] || exit 1
	rm -f $${NEXUS_STORAGE_DB_PATH:-$$HOME/.nexus/nexus.db}
	@echo "Database deleted."

# =============================================================================
# Ollama
# =============================================================================

.PHONY: ollama-pull
ollama-pull: ## Pull the nomic-embed-text embedding model
	ollama pull nomic-embed-text

.PHONY: ollama-ping
ollama-ping: ## Verify Ollama is running and model is available
	@curl -sf http://localhost:11434/api/tags | python3 -m json.tool || \
		echo "Ollama not running. Start with: ollama serve"

# =============================================================================
# Setup
# =============================================================================

.PHONY: setup
setup: ## First-time project setup
	@echo "==> Setting up Nexus development environment..."
	@cp -n .env.example .env 2>/dev/null && echo "Created .env from .env.example" || echo ".env already exists"
	@mkdir -p $$HOME/.nexus
	$(GO) mod download
	@echo ""
	@echo "Next steps:"
	@echo "  1. Edit .env and set NEXUS_ANTHROPIC_API_KEY"
	@echo "  2. Start Ollama: ollama serve"
	@echo "  3. Pull model:   make ollama-pull"
	@echo "  4. Build:        make build"
	@echo "  5. Dry-run add:  nexus add 'Test memory' --category FACT"

.PHONY: clean
clean: ## Remove build artifacts and coverage files
	rm -rf $(BUILD_DIR) coverage.out coverage.html

# =============================================================================
# Documentation
# =============================================================================

.PHONY: docs-check
docs-check: ## Verify all required doc files exist
	@echo "Checking docs structure..."
	@for f in \
		docs/architecture/HLD.md \
		docs/architecture/system-overview.md \
		docs/adr/ADR-001-sqlite-pure-go.md \
		docs/setup/local-setup.md; do \
		test -f "$$f" && echo "  OK  $$f" || echo "  MISSING  $$f"; \
	done
