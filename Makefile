.PHONY: help test test-unit test-integration test-coverage test-verbose build build-ui build-go install clean fmt fmt-md fmt-all lint lint-check lint-fix lint-md lint-md-fix lint-all vet tidy install-tools ci quick-check pre-commit pre-push security gosec-check vulncheck setup-hooks

# Tool versions - pinned for supply chain security
GOLANGCI_LINT_VERSION := v1.64.5
MARKDOWNLINT_VERSION := 0.43.0
GOSEC_VERSION := v2.22.4
GOVULNCHECK_VERSION := latest

# Binary name and install location
BINARY_NAME := thrum
BUILD_DIR := bin
INSTALL_DIR := $(HOME)/.local/bin
VERSION := 0.1.0

# Default target
help:
	@echo "Available targets:"
	@echo ""
	@echo "Development:"
	@echo "  make build             - Build UI and Go binary (full build)"
	@echo "  make build-ui          - Build UI and copy to embed location"
	@echo "  make build-go          - Build Go binary only (skip UI rebuild)"
	@echo "  make install           - Full build and install thrum to ~/.local/bin"
	@echo "  make fmt               - Format Go code"
	@echo "  make fmt-md            - Format Markdown files with prettier"
	@echo "  make fmt-all           - Format all files (Go + Markdown)"
	@echo "  make lint              - Run golangci-lint with auto-fix"
	@echo "  make lint-check        - Run golangci-lint (check only, no fix)"
	@echo "  make lint-md           - Run markdownlint on Markdown files"
	@echo "  make lint-md-fix       - Run markdownlint with auto-fix"
	@echo "  make lint-all          - Run all linters (Go + Markdown)"
	@echo "  make clean             - Remove build artifacts"
	@echo "  make tidy              - Tidy dependencies"
	@echo "  make vet               - Run go vet"
	@echo ""
	@echo "Security:"
	@echo "  make security          - Run all security checks (gosec + govulncheck)"
	@echo "  make gosec-check       - Run gosec security scanner"
	@echo "  make vulncheck         - Run govulncheck for known vulnerabilities"
	@echo ""
	@echo "Quick Checks (before commit/push):"
	@echo "  make quick-check       - Fast pre-commit checks (format, vet, test, build)"
	@echo "  make ci                - Full CI checks locally (includes security)"
	@echo "  make pre-commit        - Alias for quick-check"
	@echo "  make pre-push          - Alias for ci"
	@echo ""
	@echo "Testing:"
	@echo "  make test              - Run all tests"
	@echo "  make test-unit         - Run only unit tests (fast)"
	@echo "  make test-integration  - Run integration tests"
	@echo "  make test-coverage     - Run tests with coverage report"
	@echo "  make test-verbose      - Run tests with verbose output"
	@echo ""
	@echo "Setup:"
	@echo "  make install-tools     - Install dev tools (golangci-lint, gosec, govulncheck)"
	@echo "  make setup-hooks       - Install git hook chaining for pre-commit/pre-push"
	@echo ""

# Run all tests
test:
	@echo "Running all tests..."
	go test ./... -v

# Run only unit tests (skip integration tests)
test-unit:
	@echo "Running unit tests only..."
	go test -short ./... -v

# Run integration tests
test-integration:
	@echo "Running integration tests..."
	go test ./... -v -run Integration

# Run tests with coverage
test-coverage:
	@echo "Running tests with coverage..."
	@mkdir -p output
	go test -cover ./... -coverprofile=output/coverage.out
	go tool cover -html=output/coverage.out -o output/coverage.html
	@echo "Coverage report generated: output/coverage.html"

# Run tests with verbose output
test-verbose:
	@echo "Running tests with verbose output..."
	go test -v ./...

# Build UI and copy to embed location
build-ui:
	@echo "Building UI..."
	cd ui && pnpm install --frozen-lockfile && pnpm build
	@rm -rf internal/web/dist
	@mkdir -p internal/web/dist
	cp -r ui/packages/web-app/dist/* internal/web/dist/
	@touch internal/web/dist/.gitkeep
	@echo "UI built and copied to internal/web/dist/"

# Build Go binary only (skip UI rebuild, uses existing internal/web/dist/)
build-go:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	go build -ldflags="-X main.Version=$(VERSION) -X main.Build=$$(git rev-parse --short HEAD)" -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/$(BINARY_NAME)
ifeq ($(shell uname),Darwin)
	@codesign -s - -f $(BUILD_DIR)/$(BINARY_NAME) 2>/dev/null || true
	@echo "Signed $(BINARY_NAME) for macOS"
endif
	@echo "Build complete: $(BUILD_DIR)/$(BINARY_NAME)"

# Full build: UI then Go binary
build: build-ui build-go

# Install binary to ~/.local/bin (builds, signs on macOS, and copies)
install: build
	@mkdir -p $(INSTALL_DIR)
	@rm -f $(INSTALL_DIR)/$(BINARY_NAME)
	@cp $(BUILD_DIR)/$(BINARY_NAME) $(INSTALL_DIR)/$(BINARY_NAME)
	@# Remove stale go install binary so PATH resolves to INSTALL_DIR
	@rm -f $(shell go env GOPATH)/bin/$(BINARY_NAME)
	@echo "Installed $(BINARY_NAME) to $(INSTALL_DIR)/$(BINARY_NAME)"

# Clean build artifacts
clean:
	@echo "Cleaning..."
	rm -rf output/
	rm -rf bin/
	rm -rf dist/
	go clean

# Format Go code
fmt:
	@echo "Formatting Go code..."
	gofmt -s -w -e .

# Format Markdown files
fmt-md:
	@echo "Formatting Markdown files..."
	@if ! command -v prettier >/dev/null 2>&1; then \
		echo "prettier not found. Install with: npm install -g prettier"; \
		echo "Skipping Markdown formatting"; \
		exit 0; \
	fi
	@prettier --write "**/*.md" --prose-wrap always --ignore-path .prettierignore 2>/dev/null || true
	@echo "Markdown files formatted"

# Format all files (Go + Markdown)
fmt-all: fmt fmt-md
	@echo "All files formatted"

# Run Go linter with auto-fix (default)
lint:
	@echo "Running golangci-lint with auto-fix ($(GOLANGCI_LINT_VERSION))..."
	@if ! command -v golangci-lint >/dev/null 2>&1 && ! [ -f ~/go/bin/golangci-lint ]; then \
		echo "golangci-lint not found. Installing $(GOLANGCI_LINT_VERSION)..."; \
		go install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION); \
	fi
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run --fix --timeout=10m; \
	else \
		~/go/bin/golangci-lint run --fix --timeout=10m; \
	fi
	@echo "Auto-fixable issues resolved"

# Run Go linter check-only (no auto-fix, for CI)
lint-check:
	@echo "Running golangci-lint ($(GOLANGCI_LINT_VERSION))..."
	@if ! command -v golangci-lint >/dev/null 2>&1 && ! [ -f ~/go/bin/golangci-lint ]; then \
		echo "golangci-lint not found. Installing $(GOLANGCI_LINT_VERSION)..."; \
		go install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION); \
	fi
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run --timeout=10m; \
	else \
		~/go/bin/golangci-lint run --timeout=10m; \
	fi

# Backwards compat alias
lint-fix: lint

# Run Markdown linter
lint-md:
	@echo "Running markdownlint ($(MARKDOWNLINT_VERSION))..."
	@if ! command -v markdownlint >/dev/null 2>&1; then \
		echo "markdownlint not found. Installing $(MARKDOWNLINT_VERSION)..."; \
		npm install -g markdownlint-cli@$(MARKDOWNLINT_VERSION) || (echo "Failed to install markdownlint. Install manually: npm install -g markdownlint-cli@$(MARKDOWNLINT_VERSION)" && exit 1); \
	fi
	@markdownlint . --config .markdownlint.json --ignore-path .markdownlintignore || (echo "Markdown linting failed. Run 'make fmt-md' to auto-fix some issues." && exit 1)

# Run Markdown linter with auto-fix
lint-md-fix:
	@echo "Running markdownlint with auto-fix ($(MARKDOWNLINT_VERSION))..."
	@if ! command -v markdownlint >/dev/null 2>&1; then \
		echo "markdownlint not found. Installing $(MARKDOWNLINT_VERSION)..."; \
		npm install -g markdownlint-cli@$(MARKDOWNLINT_VERSION) || (echo "Failed to install markdownlint" && exit 1); \
	fi
	@markdownlint . --config .markdownlint.json --ignore-path .markdownlintignore --fix
	@echo "Markdown files fixed"

# Run all linters
lint-all: lint lint-md

# Run go vet
vet:
	@echo "Running go vet..."
	go vet ./...

# Tidy dependencies
tidy:
	@echo "Tidying dependencies..."
	go mod tidy

# Install development tools
install-tools:
	@echo "Installing development tools (pinned versions)..."
	@echo "Installing golangci-lint $(GOLANGCI_LINT_VERSION)..."
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	@echo "Installing gosec $(GOSEC_VERSION)..."
	go install github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION)
	@echo "Installing govulncheck..."
	go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	@echo "Installing markdownlint-cli $(MARKDOWNLINT_VERSION)..."
	@if command -v npm >/dev/null 2>&1; then \
		npm install -g markdownlint-cli@$(MARKDOWNLINT_VERSION); \
	else \
		echo "npm not found, skipping markdownlint-cli installation"; \
	fi
	@echo "Tools installed successfully"
	@echo ""
	@echo "Optional: Run 'make setup-hooks' to install git hook chaining"
	@echo "Optional: Run 'scripts/setup-git-secrets.sh' to set up git-secrets"

# Quick pre-commit checks (fast)
quick-check: fmt vet test build
	@echo "Quick checks passed"

# Security scanning
security: gosec-check vulncheck
	@echo "Security checks passed"

gosec-check:
	@echo "Running gosec security scanner..."
	@if ! command -v gosec >/dev/null 2>&1; then \
		echo "gosec not found. Installing $(GOSEC_VERSION)..."; \
		go install github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION); \
	fi
	gosec -exclude-dir=.ref -exclude-dir=third_party -exclude-dir=builtin -exclude-dir=examples -exclude-dir=output ./...

vulncheck:
	@echo "Running govulncheck..."
	@if ! command -v govulncheck >/dev/null 2>&1; then \
		echo "govulncheck not found. Installing..."; \
		go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION); \
	fi
	govulncheck ./...

# Full CI checks locally
ci: fmt-all lint-all vet test security build
	@echo "CI checks passed"

# Aliases for convenience
pre-commit: quick-check

pre-push: ci

# Setup git hook chaining
setup-hooks:
	@scripts/install-hooks.sh
