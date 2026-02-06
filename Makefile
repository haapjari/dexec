DBG_MAKEFILE ?=
ifeq ($(DBG_MAKEFILE),1)
    $(warning ***** starting Makefile for goal(s) "$(MAKECMDGOALS)")
else
    MAKEFLAGS += -s
endif

SHELL := /usr/bin/env bash -o errexit -o pipefail -o nounset

MAKEFLAGS += --no-builtin-rules
MAKEFLAGS += --warn-undefined-variables

.SUFFIXES:

BINARY := dexec
GO := go
GOFLAGS ?=

GO_VERSION_REQ := $(shell grep '^go ' go.mod | cut -d' ' -f2)

OUT_DIR := _output
BIN_DIR := $(OUT_DIR)/bin
TOOLS_DIR := $(OUT_DIR)/tools
HACK_DIR := hack

GOLANGCI_LINT_VERSION := v2.7.2
GOTESTSUM_VERSION := v1.13.0
GOIMPORTS_VERSION := v0.28.0
GOFUMPT_VERSION := v0.7.0
GOLINES_VERSION := v0.13.0
GOVULNCHECK_VERSION := v1.1.4

VERSION_FILE := VERSION
CURRENT_VERSION := $(shell cat $(VERSION_FILE) 2>/dev/null || echo "0.0.0")
LDFLAGS := -ldflags "-s -w -X main.version=$(CURRENT_VERSION)"

DIST_DIR := dist
RELEASE_PLATFORMS := \
	linux/amd64 \
	linux/arm64 \
	darwin/amd64 \
	darwin/arm64 \
	windows/amd64

COVERAGE_DIR := $(OUT_DIR)/coverage
COVERAGE_FILE := $(COVERAGE_DIR)/coverage.out
COVERAGE_HTML := $(COVERAGE_DIR)/coverage.html

DOCKER_IMAGE ?= $(BINARY)
DOCKER_TAG ?= latest

# ============================================================================
# HELP TARGETS
# ============================================================================

.PHONY: all
all: verify build

.PHONY: help
help:
	@echo "Commands"
	@echo ""
	@echo "Primary:"
	@echo "  all            - run verify and build (default)"
	@echo "  build          - build the binary"
	@echo "  test           - run tests"
	@echo "  verify         - run all verification checks"
	@echo "  update         - run all update scripts (fix formatting, tidy, etc.)"
	@echo "  clean          - remove build artifacts"
	@echo ""
	@echo "Verification:"
	@echo "  verify-go-version  - check go version is in sync"
	@echo "  verify-gofmt       - check code formatting"
	@echo "  verify-gomod       - check go.mod is tidy"
	@echo "  verify-lint        - run golangci-lint"
	@echo "  verify-vet         - run go vet"
	@echo ""
	@echo "Update:"
	@echo "  update-gofmt       - format code with gofmt and goimports"
	@echo "  update-gomod       - run go mod tidy"
	@echo ""
	@echo "Development:"
	@echo "  tools          - install development tools"
	@echo "  setup-hooks    - install git pre-commit hooks"
	@echo "  test-race      - run tests with race detector"
	@echo "  test-cover     - run tests with coverage report"
	@echo "  docker-build   - build the docker image"
	@echo "  docker-run     - run the docker image"
	@echo ""
	@echo "Release:"
	@echo "  release        - tag, push, and create GitHub release with binaries"
	@echo "  release-build  - cross-compile release binaries to dist/"
	@echo ""
	@echo "Variables:"
	@echo "  DBG_MAKEFILE=1 - show make debugging output"
	@echo "  GOFLAGS=...    - extra flags for go commands"

# ============================================================================
# TOOLS TARGETS
# ============================================================================

.PHONY: tools
tools: $(TOOLS_DIR)/golangci-lint $(TOOLS_DIR)/gotestsum $(TOOLS_DIR)/goimports $(TOOLS_DIR)/gofumpt $(TOOLS_DIR)/golines $(TOOLS_DIR)/govulncheck

$(TOOLS_DIR):
	mkdir -p $(TOOLS_DIR)

$(TOOLS_DIR)/govulncheck: | $(TOOLS_DIR)
	@echo "installing govulncheck $(GOVULNCHECK_VERSION)..."
	GOBIN=$(abspath $(TOOLS_DIR)) $(GO) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

$(TOOLS_DIR)/golangci-lint: | $(TOOLS_DIR)
	@echo "installing golangci-lint $(GOLANGCI_LINT_VERSION)..."
	GOBIN=$(abspath $(TOOLS_DIR)) $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

$(TOOLS_DIR)/gotestsum: | $(TOOLS_DIR)
	@echo "installing gotestsum $(GOTESTSUM_VERSION)..."
	GOBIN=$(abspath $(TOOLS_DIR)) $(GO) install gotest.tools/gotestsum@$(GOTESTSUM_VERSION)

$(TOOLS_DIR)/goimports: | $(TOOLS_DIR)
	@echo "installing goimports $(GOIMPORTS_VERSION)..."
	GOBIN=$(abspath $(TOOLS_DIR)) $(GO) install golang.org/x/tools/cmd/goimports@$(GOIMPORTS_VERSION)

$(TOOLS_DIR)/gofumpt: | $(TOOLS_DIR)
	@echo "installing gofumpt $(GOFUMPT_VERSION)..."
	GOBIN=$(abspath $(TOOLS_DIR)) $(GO) install mvdan.cc/gofumpt@$(GOFUMPT_VERSION)

$(TOOLS_DIR)/golines: | $(TOOLS_DIR)
	@echo "installing golines $(GOLINES_VERSION)..."
	GOBIN=$(abspath $(TOOLS_DIR)) $(GO) install github.com/segmentio/golines@$(GOLINES_VERSION)

# ============================================================================
#  BUILD TARGETS
# ============================================================================

.PHONY: build
build: $(BIN_DIR)/$(BINARY)

$(BIN_DIR):
	mkdir -p $(BIN_DIR)

$(BIN_DIR)/$(BINARY): $(shell find . -name '*.go' -not -path './_output/*') | $(BIN_DIR)
	@echo "building $(BINARY)..."
	$(GO) build $(GOFLAGS) $(LDFLAGS) -o $@ ./cmd

# ============================================================================
# VERIFY TARGETS
# ============================================================================

.PHONY: verify
verify: verify-go-version verify-gofmt verify-gomod verify-vet verify-lint verify-govulncheck
	@echo "all verification checks passed!"

.PHONY: verify-go-version
verify-go-version:
	@$(HACK_DIR)/verify-go-version.sh

.PHONY: verify-gofmt
verify-gofmt:
	@$(HACK_DIR)/verify-gofmt.sh

.PHONY: verify-gomod
verify-gomod:
	@$(HACK_DIR)/verify-gomod.sh

.PHONY: verify-vet
verify-vet:
	$(GO) vet ./...

.PHONY: verify-govulncheck
verify-govulncheck: $(TOOLS_DIR)/govulncheck
	$(TOOLS_DIR)/govulncheck ./...

.PHONY: verify-lint
verify-lint: $(TOOLS_DIR)/golangci-lint
	$(TOOLS_DIR)/golangci-lint run --timeout 5m

# ============================================================================
# UPDATE TARGETS
# ============================================================================

.PHONY: update
update: update-gofmt update-gomod
	@echo "all updates complete!"

.PHONY: update-gofmt
update-gofmt: $(TOOLS_DIR)/goimports $(TOOLS_DIR)/gofumpt $(TOOLS_DIR)/golines
	@$(HACK_DIR)/update-gofmt.sh

.PHONY: update-gomod
update-gomod:
	@echo "running go mod tidy..."
	$(GO) mod tidy

# ============================================================================
# TEST TARGETS
# ============================================================================

.PHONY: test
test: $(TOOLS_DIR)/gotestsum
	@echo "running tests..."
	$(TOOLS_DIR)/gotestsum --format pkgname -- -count=1 ./...

.PHONY: test-race
test-race: $(TOOLS_DIR)/gotestsum
	@echo "running tests with race detector..."
	$(TOOLS_DIR)/gotestsum --format pkgname -- -race -count=1 ./...

.PHONY: test-cover
test-cover: $(TOOLS_DIR)/gotestsum | $(COVERAGE_DIR)
	@echo "running tests with coverage..."
	$(TOOLS_DIR)/gotestsum --format pkgname -- -race -count=1 -coverprofile=$(COVERAGE_FILE) -covermode=atomic ./...
	$(GO) tool cover -func=$(COVERAGE_FILE)
	$(GO) tool cover -html=$(COVERAGE_FILE) -o $(COVERAGE_HTML)
	@echo ""
	@echo "html report: $(COVERAGE_HTML)"

$(COVERAGE_DIR):
	mkdir -p $(COVERAGE_DIR)

# ============================================================================
# CLEANING TARGETS
# ============================================================================

.PHONY: setup-hooks
setup-hooks:
	@$(HACK_DIR)/setup-hooks.sh

.PHONY: clean
clean:
	@echo "cleaning build artifacts..."
	rm -rf $(OUT_DIR)
	rm -rf $(DIST_DIR)
	$(GO) clean

# ============================================================================
# RELEASE TARGETS
# ============================================================================

.PHONY: release-build
release-build:
	@echo "building release binaries v$(CURRENT_VERSION)..."
	@rm -rf $(DIST_DIR)
	@mkdir -p $(DIST_DIR)
	@for platform in $(RELEASE_PLATFORMS); do \
		GOOS=$${platform%/*}; \
		GOARCH=$${platform#*/}; \
		output="$(DIST_DIR)/$(BINARY)-$${GOOS}-$${GOARCH}"; \
		if [ "$$GOOS" = "windows" ]; then output="$${output}.exe"; fi; \
		echo "  $$GOOS/$$GOARCH -> $$output"; \
		GOOS=$$GOOS GOARCH=$$GOARCH $(GO) build $(LDFLAGS) -o "$$output" ./cmd || exit 1; \
	done
	@echo "binaries written to $(DIST_DIR)/"

.PHONY: release
release:
	@echo "preparing release..."
	@# check for staged changes (nothing should be staged)
	@if ! git diff --cached --quiet; then \
		echo "error: staged changes detected"; \
		echo "unstage or commit changes before releasing"; \
		exit 1; \
	fi
	@# check for unstaged changes (excluding VERSION which we may modify)
	@if ! git diff --quiet -- . ':!VERSION'; then \
		echo "error: unstaged changes detected"; \
		echo "commit or stash changes before releasing"; \
		exit 1; \
	fi
	@# check gh is available
	@command -v gh >/dev/null 2>&1 || { echo "error: gh (github cli) not found"; exit 1; }
	@# check we're on main branch
	@if [ "$$(git branch --show-current)" != "main" ]; then \
		echo "error: must be on main branch to release"; \
		exit 1; \
	fi
	@# pull latest changes and verify we're in sync with remote
	@echo "pulling latest changes..."
	@git fetch origin main
	@if [ "$$(git rev-parse HEAD)" != "$$(git rev-parse origin/main)" ]; then \
		echo "error: local main is not in sync with origin/main"; \
		echo "run 'git pull' or 'git push' to sync before releasing"; \
		exit 1; \
	fi
	@# determine version: if tag exists, bump patch; otherwise use VERSION as-is
	@VERSION=$(CURRENT_VERSION); \
	if git rev-parse "v$$VERSION" >/dev/null 2>&1; then \
		echo "tag v$$VERSION exists, bumping patch version..."; \
		MAJOR=$$(echo $$VERSION | cut -d. -f1); \
		MINOR=$$(echo $$VERSION | cut -d. -f2); \
		PATCH=$$(echo $$VERSION | cut -d. -f3); \
		PATCH=$$((PATCH + 1)); \
		VERSION="$$MAJOR.$$MINOR.$$PATCH"; \
		echo "$$VERSION" > $(VERSION_FILE); \
		echo "updated VERSION to $$VERSION"; \
	else \
		echo "using version $$VERSION from VERSION file"; \
	fi; \
	\
	echo "committing version bump..."; \
	git add $(VERSION_FILE); \
	git commit -m "chore: release v$$VERSION" || true; \
	\
	echo "creating tag v$$VERSION..."; \
	git tag -a "v$$VERSION" -m "Release v$$VERSION"; \
	\
	echo "pushing to origin..."; \
	git push origin main --tags; \
	\
	echo "building release binaries..."; \
	$(MAKE) release-build CURRENT_VERSION=$$VERSION; \
	\
	echo "creating github release..."; \
	gh release create "v$$VERSION" $(DIST_DIR)/* --generate-notes --title "v$$VERSION"; \
	\
	echo ""; \
	echo "release v$$VERSION complete!"
