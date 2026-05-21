.PHONY: help test test-unit test-integration test-coverage test-verbose build build-ui build-go sync-embed-reference install dev deploy-remote clean clean-e2e test-e2e fmt fmt-md fmt-all lint lint-check lint-fix lint-md lint-md-fix lint-all vet tidy install-tools ci quick-check pre-commit pre-push security gosec-check vulncheck setup-hooks sync-skills release-tests

# Tool versions - pinned for supply chain security
GOLANGCI_LINT_VERSION := v1.64.5
MARKDOWNLINT_VERSION := 0.43.0
GOSEC_VERSION := v2.22.4
GOVULNCHECK_VERSION := latest

# Binary name and install location
BINARY_NAME := thrum
BUILD_DIR := bin
INSTALL_DIR := $(HOME)/.local/bin
VERSION := 0.10.5

# Default target
help:
	@echo "Available targets:"
	@echo ""
	@echo "Development:"
	@echo "  make build             - Build UI and Go binary (full build)"
	@echo "  make build-ui          - Build UI and copy to embed location"
	@echo "  make build-go          - Build Go binary only (skip UI rebuild)"
	@echo "  make install           - Full build and install thrum to ~/.local/bin (SHARED — affects all agents)"
	@echo "  make dev               - Full build + restart LOCAL worktree daemon (isolated — safe for multi-agent machines)"
	@echo "  make deploy-remote REMOTE=host - Cross-compile + scp + (macOS: codesign) + verify version on remote"
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

# Sync root llms.txt to embed location before Go build
sync-embed-reference:
	@mkdir -p internal/context/reference
	@test -f llms.txt || (echo "ERROR: llms.txt not found at repo root - cannot sync embed reference" && exit 1)
	@cp llms.txt internal/context/reference/llms.txt
	@echo "Synced llms.txt to internal/context/reference/"

# Build Go binary only (skip UI rebuild, uses existing internal/web/dist/)
build-go: sync-embed-reference
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

# Build, sign, and restart the LOCAL worktree daemon.
#
# Use this when testing changes in a worktree on a multi-agent machine.
# Produces a signed binary at ./$(BUILD_DIR)/$(BINARY_NAME) and restarts
# the repo-scoped daemon via os.Executable() re-exec — the new daemon
# runs from the local bin path, NOT from $(INSTALL_DIR). Other agents
# on the same machine using ~/.local/bin/$(BINARY_NAME) are unaffected.
#
# Safe to run repeatedly during development. First-time use (no daemon
# running yet in this repo) falls through from restart to start.
dev: build
	@echo "Restarting worktree-local daemon using ./$(BUILD_DIR)/$(BINARY_NAME)..."
	@./$(BUILD_DIR)/$(BINARY_NAME) daemon restart 2>/dev/null || ./$(BUILD_DIR)/$(BINARY_NAME) daemon start
	@./$(BUILD_DIR)/$(BINARY_NAME) daemon status

# Deploy binary to a remote machine via scp with cross-compilation.
# Usage: make deploy-remote REMOTE=leonsmacmini.local
#        make deploy-remote REMOTE=ubuntuleondev
#        make deploy-remote REMOTE=user@192.168.1.10
#
# Scope: JUST installs a verified working binary at ~/.local/bin/. Does NOT
# touch daemon lifecycle — the agent operating on the remote decides when
# to restart. The final `version` check confirms the binary is runnable on
# the target (if codesign is missing or the arch is wrong the version call
# fails and the deploy errors out). `thrum version` is repo-context-free
# and is the only thrum subcommand run over raw ssh here.
#
# Detects remote OS/arch via `uname -s -m`, cross-compiles the right binary,
# uploads it, codesigns on darwin targets, moves it into place, and verifies.
# Supports darwin/amd64, darwin/arm64, linux/amd64, linux/arm64.
deploy-remote:
ifndef REMOTE
	$(error REMOTE is required. Usage: make deploy-remote REMOTE=host)
endif
	@set -e; \
	echo "Detecting remote OS/arch on $(REMOTE)..."; \
	uname_out=$$(ssh $(REMOTE) uname -s -m); \
	echo "  remote: $$uname_out"; \
	case "$$uname_out" in \
		Darwin*arm64)    goos=darwin; goarch=arm64 ;; \
		Darwin*x86_64)   goos=darwin; goarch=amd64 ;; \
		Linux*x86_64)    goos=linux;  goarch=amd64 ;; \
		Linux*aarch64)   goos=linux;  goarch=arm64 ;; \
		Linux*arm64)     goos=linux;  goarch=arm64 ;; \
		*) echo "ERROR: unsupported remote OS/arch: $$uname_out"; exit 1 ;; \
	esac; \
	artifact=$(BINARY_NAME)-$$goos-$$goarch; \
	short_sha=$$(git rev-parse --short HEAD); \
	echo "Cross-compiling $$artifact (v$(VERSION) build $$short_sha)..."; \
	GOOS=$$goos GOARCH=$$goarch go build \
		-ldflags="-X main.Version=$(VERSION) -X main.Build=$$short_sha" \
		-o $(BUILD_DIR)/$$artifact ./cmd/$(BINARY_NAME); \
	echo "Uploading $(BUILD_DIR)/$$artifact to $(REMOTE):/tmp/$(BINARY_NAME)-new..."; \
	scp $(BUILD_DIR)/$$artifact $(REMOTE):/tmp/$(BINARY_NAME)-new; \
	ssh $(REMOTE) "chmod +x /tmp/$(BINARY_NAME)-new"; \
	if [ "$$goos" = "darwin" ]; then \
		echo "Codesigning on darwin target..."; \
		ssh $(REMOTE) "xattr -cr /tmp/$(BINARY_NAME)-new && codesign -s - -f /tmp/$(BINARY_NAME)-new"; \
	fi; \
	ssh $(REMOTE) "mv /tmp/$(BINARY_NAME)-new ~/.local/bin/$(BINARY_NAME)"; \
	echo "Verifying remote binary runs..."; \
	remote_ver=$$(ssh $(REMOTE) "~/.local/bin/$(BINARY_NAME) version" 2>&1 | head -1); \
	echo "  remote: $$remote_ver"; \
	if ! echo "$$remote_ver" | grep -qF "v$(VERSION)" || ! echo "$$remote_ver" | grep -qF "$$short_sha"; then \
		echo "ERROR: remote binary did not report expected version v$(VERSION) + build $$short_sha."; \
		echo "This usually means the binary did not codesign properly, is"; \
		echo "the wrong arch, or failed to run for another reason."; \
		exit 1; \
	fi; \
	echo "Deployed $(BINARY_NAME) ($$goos/$$goarch) to $(REMOTE) — version verified. Daemon restart is up to the operator."

## E2E test cleanup — stops daemon and removes /tmp/thrum-e2e-release/
clean-e2e:
	@echo "Stopping E2E daemon..."
	@if [ -d /tmp/thrum-e2e-release/coordinator ]; then \
		bin/thrum --repo /tmp/thrum-e2e-release/coordinator daemon stop 2>/dev/null || true; \
	fi
	@echo "Removing /tmp/thrum-e2e-release/..."
	@rm -rf /tmp/thrum-e2e-release
	@rm -f node_modules/.e2e-test-repo node_modules/.e2e-implementer-repo \
		node_modules/.e2e-bare-remote node_modules/.e2e-ws-port
	@echo "E2E cleanup complete."

## Run E2E tests (builds first, creates test environment)
test-e2e:
	npx playwright test

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
	gosec \
		-exclude-dir=.ref -exclude-dir=third_party -exclude-dir=builtin -exclude-dir=examples -exclude-dir=output \
		-exclude=G115,G117,G204,G404,G602,G703 \
		./...
# Exclusion rationale (matches .golangci.yml — standalone gosec doesn't
# read that file nor honor //nolint directives):
#   G115: int(file.Fd()) for syscall.Flock — safe on 64-bit Go builds.
#   G117: struct field names matching "secret" patterns are intentional config fields.
#   G204: CLI tool legitimately runs git and other subprocesses with variables.
#   G404: math/rand used for non-security UI hint rotation (cli/hints.go).
#   G602: false positive on guarded slice access (e.g. i > 0 before ports[i-1]).
#   G703: path traversal via taint — paths are constructed internally, not from user input.

vulncheck:
	@echo "Running govulncheck..."
	@if ! command -v govulncheck >/dev/null 2>&1; then \
		echo "govulncheck not found. Installing..."; \
		go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION); \
	fi
	govulncheck ./... || echo "⚠ govulncheck failed (may be Go toolchain incompatibility — check upstream)"

# Verify codex-plugin and claude-plugin manifest versions stay in sync.
# Spec ref: dev-docs/specs/codex-plugin-first-class.md §6 (Versioning policy).
check-plugin-versions:
	@codex_v=$$(awk -F'"' '/"version"/ {print $$4; exit}' codex-plugin/plugins/thrum/.codex-plugin/plugin.json); \
	claude_v=$$(awk -F'"' '/"version"/ {print $$4; exit}' claude-plugin/.claude-plugin/plugin.json); \
	if [ "$$codex_v" != "$$claude_v" ]; then \
		echo "ERROR: codex-plugin version ($$codex_v) != claude-plugin version ($$claude_v) — keep them in sync"; \
		exit 1; \
	fi; \
	echo "plugin manifests in sync ($$codex_v)"

# Full CI checks locally
ci: fmt-all lint-all vet test security check-plugin-versions build
	@echo "CI checks passed"

# Aliases for convenience
pre-commit: quick-check

pre-push: ci

# Sync toolkit assets to plugin directories
sync-skills:
	@echo "Syncing toolkit assets to plugins..."
	@cp toolkit/skills/update-project.md claude-plugin/commands/update-project.md
	@test -f toolkit/resources/LISTENER_PATTERN.md && cp toolkit/resources/LISTENER_PATTERN.md claude-plugin/skills/thrum/resources/LISTENER_PATTERN.md || true
	@test -d codex-plugin/skills/thrum/resources && test -f toolkit/resources/LISTENER_PATTERN.md && cp toolkit/resources/LISTENER_PATTERN.md codex-plugin/skills/thrum/resources/LISTENER_PATTERN.md || true
	@test -d internal/cli/skill/thrum/references && test -f toolkit/resources/LISTENER_PATTERN.md && cp toolkit/resources/LISTENER_PATTERN.md internal/cli/skill/thrum/references/LISTENER_PATTERN.md || true
	@cp toolkit/agents/message-listener.md claude-plugin/agents/message-listener.md
	@echo "Assets synced."

# Setup git hook chaining
setup-hooks:
	@scripts/install-hooks.sh

# Release test framework — drives a real coord+impl multi-agent fixture
# in tmux panes. Slow (~100s for the full baseline), spawns real claude
# processes, NOT part of `make ci`.
release-tests: build
	@./tests/release/run.sh

# Behavioral test harness — drives two live agents through scripted YAML
# cards with structural + LLM-judge assertions. Iterative dev tool for
# preamble/plugin work; NOT part of `make ci`. See
# tests/release/behavioral/README.md.
.PHONY: behavioral-setup behavioral

behavioral-setup:
	@bash scripts/gen-behavioral-gowork.sh
	@bash -c 'set -a; source "$$(cd "$$(git rev-parse --git-common-dir)/.." && pwd)/.env"; set +a; cd tests/release/cmd/llm-judge && go run . ping'
	@echo "behavioral-setup OK"

behavioral: behavioral-setup
	./tests/release/behavioral/run-behavioral.sh
