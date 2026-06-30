# Podplane <https://podplane.dev>
# Copyright The Podplane Authors
# SPDX-License-Identifier: Apache-2.0

CURRENT_DIR := $(abspath $(dir $(firstword $(MAKEFILE_LIST))))
CHARTS := $(patsubst %/Chart.yaml,%,$(wildcard charts/*/Chart.yaml))
JSON_FILES := $(shell find manifests -name '*.json' -type f 2>/dev/null | sort)
PODPLANE_GIT_CACHE_DIR ?= $(HOME)/.podplane/cache/deps/git

.DEFAULT_GOAL := help

.PHONY: help fmt check deps lint render validate test check-crds update-crds update-manifests release-manifest precommit ci minimal recommended all _local-bootstrap git-sync git-watch clean

help: ## Show available targets
	@echo "Podplane Kubernetes PaaS Components"
	@echo ""
	@echo "Usage: make <target>"
	@awk 'BEGIN {FS = ":.*?## "} /^##@/ {printf "\n\033[1m%s\033[0m\n", substr($$0, 5)} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-17s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@echo ""
	@echo "Typical local test workflow:"
	@echo "  podplane local start --components=none --follow"
	@echo "  make git-sync"
	@echo "  make recommended"

##@ Checks

fmt: ## Format JSON files
	@command -v jq >/dev/null 2>&1 || { echo "jq is required but not installed"; exit 1; }
	@echo "Formatting JSON files..."
	@for file in $(JSON_FILES); do \
		tmp="$$(mktemp)"; \
		jq . "$$file" > "$$tmp"; \
		mv "$$tmp" "$$file"; \
	done

check: ## Check JSON formatting
	@command -v jq >/dev/null 2>&1 || { echo "jq is required but not installed"; exit 1; }
	@echo "Checking JSON formatting..."
	@for file in $(JSON_FILES); do \
		tmp="$$(mktemp)"; \
		jq . "$$file" > "$$tmp"; \
		if ! diff -u "$$file" "$$tmp"; then \
			rm -f "$$tmp"; \
			echo "$$file needs formatting (run 'make fmt')"; \
			exit 1; \
		fi; \
		rm -f "$$tmp"; \
	done

deps: ## Fetch Helm chart dependencies for charts that declare them
	@command -v helm >/dev/null 2>&1 || { echo "helm is required but not installed"; exit 1; }
	@for chart in $(CHARTS); do \
		grep -q '^dependencies:' $$chart/Chart.yaml || continue; \
		echo "==> $$chart"; \
		helm dependency update $$chart >/dev/null || exit 1; \
	done

lint: deps ## Run helm lint on every chart
	@for chart in $(CHARTS); do \
		echo "==> $$chart"; \
		extra_args=""; \
		if [ "$$chart" = "charts/podplane-operator" ]; then extra_args="--set podplane.operator.config.cluster.id=test-cluster"; fi; \
		helm lint $$chart $$extra_args || exit 1; \
	done

render: deps ## Run helm template on every chart
	@for chart in $(CHARTS); do \
		echo "==> $$chart"; \
		extra_args=""; \
		if [ "$$chart" = "charts/podplane-operator" ]; then extra_args="--set podplane.operator.config.cluster.id=test-cluster"; fi; \
		helm template $$chart $$extra_args >/dev/null || exit 1; \
	done

validate: render ## Alias for render

test: ## Run Go tests
	go test ./...

precommit: check ## Fast local pre-commit check: JSON fmt + helm lint (no network)
	@command -v helm >/dev/null 2>&1 || { echo "helm is required but not installed"; exit 1; }
	@for chart in $(CHARTS); do \
		extra_args=""; \
		if [ "$$chart" = "charts/podplane-operator" ]; then extra_args="--set podplane.operator.config.cluster.id=test-cluster"; fi; \
		helm lint --quiet $$chart $$extra_args >/dev/null || { helm lint $$chart $$extra_args; exit 1; }; \
	done
	@go vet ./...
	@echo "precommit ok"

ci: check lint render check-crds test ## Full CI suite (fetches deps, renders all charts, runs all tests)

##@ CRDs

check-crds: ## Validate vendored CRDs with the compatibility checker
	@for chart in charts/*-crds; do \
		echo "==> $$chart"; \
		go run ./scripts/crds --old $$chart/templates/external --new $$chart/templates/external || exit 1; \
	done

update-crds: ## Update vendored CRDs with compatibility checks (CHART=<name> or all)
	@go run ./scripts/crds --update $${CHART:-all}

##@ Manifests

update-manifests: ## Generate manifests/components.json from rendered non-CRD charts
	@echo "Updating manifests/components.json from rendered non-CRD charts..."
	@go run ./scripts/manifests --output manifests/components.json

release-manifest: ## Write dist/release/components_$${VERSION}.json from manifests/components.json
	@test -n "$${VERSION:-}" || { echo "VERSION is required"; exit 1; }
	@command -v jq >/dev/null 2>&1 || { echo "jq is required but not installed"; exit 1; }
	@mkdir -p dist/release
	@jq --arg version "$$VERSION" '.components.version = $$version | .components.source = {"url":"https://github.com/podplane/components.git","ref":{"tag":("v" + $$version)}}' manifests/components.json > "dist/release/components_$${VERSION}.json"
	@jq . "dist/release/components_$${VERSION}.json" >/dev/null
	@echo "wrote dist/release/components_$${VERSION}.json"

##@ Local Development

minimal: ## Bootstrap minimal/core components using the local Podplane Git cache
	@$(MAKE) --no-print-directory _local-bootstrap PLATFORM_INSTALL=minimal

recommended: ## Bootstrap recommended components using the local Podplane Git cache
	@$(MAKE) --no-print-directory _local-bootstrap PLATFORM_INSTALL=recommended

all: ## Bootstrap all components using the local Podplane Git cache
	@$(MAKE) --no-print-directory _local-bootstrap PLATFORM_INSTALL=all

_local-bootstrap:
	@test -f "$(PODPLANE_GIT_CACHE_DIR)/components.git/config" || { echo "Error: local components Git cache not found at $(PODPLANE_GIT_CACHE_DIR)/components.git. Run 'make git-sync' first." >&2; exit 1; }
	@command -v podplane >/dev/null 2>&1 || { echo "Error: podplane is required." >&2; exit 1; }
	@command -v jq >/dev/null 2>&1 || { echo "Error: jq is required." >&2; exit 1; }
	@status_json="$$(podplane local status --json)"; \
		local_server_running="$$(printf '%s' "$$status_json" | jq -r 'if .local_server.running then "true" else "" end')"; \
		if [ "$$local_server_running" != "true" ]; then \
			echo "Error: Podplane local server is not running. Run 'podplane local start --components=none' first." >&2; \
			exit 1; \
		fi; \
		PLATFORM_GIT_REPOSITORY_URL="$$(printf '%s' "$$status_json" | jq -r '.components.source.url // empty')"; \
		PLATFORM_GIT_REPOSITORY_BRANCH="$$(printf '%s' "$$status_json" | jq -r '.components.source.ref.branch // empty')"; \
		PLATFORM_GIT_SECRET_REF_NAME="$$(printf '%s' "$$status_json" | jq -r '.components.source.secretRef.name // empty')"; \
		PLATFORM_GIT_CA_CERT_FILE="$$(printf '%s' "$$status_json" | jq -r '.local_server.ca_cert_file // empty')"; \
		if [ -z "$$PLATFORM_GIT_REPOSITORY_URL" ] || [ -z "$$PLATFORM_GIT_REPOSITORY_BRANCH" ] || [ -z "$$PLATFORM_GIT_SECRET_REF_NAME" ] || [ -z "$$PLATFORM_GIT_CA_CERT_FILE" ]; then \
			echo "Error: podplane local status --json did not return local Git URL, branch, secretRef, and CA file." >&2; \
			exit 1; \
		fi; \
		export PLATFORM_GIT_REPOSITORY_URL PLATFORM_GIT_REPOSITORY_BRANCH PLATFORM_GIT_SECRET_REF_NAME PLATFORM_GIT_CA_CERT_FILE; \
		DOMAIN="$${DOMAIN:-default.localhost}" PLATFORM_INSTALL=$(PLATFORM_INSTALL) ./bootstrap/apply.sh

git-sync: ## Snapshot this checkout into the Podplane Git cache as components.git local-dev
	@mkdir -p "$(PODPLANE_GIT_CACHE_DIR)" temp/git-sync
	@if [ ! -d "$(PODPLANE_GIT_CACHE_DIR)/components.git" ]; then git init --bare "$(PODPLANE_GIT_CACHE_DIR)/components.git" >/dev/null; fi
	@if [ ! -d temp/git-sync/worktree/.git ]; then \
		mkdir -p temp/git-sync/worktree; \
		git init temp/git-sync/worktree >/dev/null; \
		git -C temp/git-sync/worktree checkout -b local-dev >/dev/null; \
	elif [ "$$(git -C temp/git-sync/worktree symbolic-ref --quiet --short HEAD || true)" != "local-dev" ]; then \
		if git -C temp/git-sync/worktree show-ref --verify --quiet refs/heads/local-dev; then \
			git -C temp/git-sync/worktree checkout local-dev >/dev/null; \
		else \
			git -C temp/git-sync/worktree checkout -b local-dev >/dev/null; \
		fi; \
	fi
	@git -C temp/git-sync/worktree config user.name "Podplane Git Sync"
	@git -C temp/git-sync/worktree config user.email "git-sync@podplane.local"
	@git -C temp/git-sync/worktree config commit.gpgsign false
	@rsync -a --delete \
		--exclude .git \
		--exclude temp \
		--exclude 'charts/*/charts' \
		--exclude vendor \
		--exclude dist \
		./ temp/git-sync/worktree/
	@git -C temp/git-sync/worktree add -A
	@if git -C temp/git-sync/worktree diff --cached --quiet; then \
		echo "No changes to sync."; \
	else \
		git -C temp/git-sync/worktree commit -m "git sync $$(date -u +%Y-%m-%dT%H:%M:%SZ)" >/dev/null; \
		git -C temp/git-sync/worktree push --force "$(PODPLANE_GIT_CACHE_DIR)/components.git" local-dev:local-dev >/dev/null; \
		echo "Synced local checkout to $(PODPLANE_GIT_CACHE_DIR)/components.git (local-dev)."; \
	fi

git-watch: ## Run git-sync automatically when local files change
	@command -v watchexec >/dev/null || \
		(echo "Error: watchexec is required for git-watch." >&2; exit 1)
	watchexec --watch . --ignore .git --ignore temp --debounce 1s -- $(MAKE) git-sync

clean: ## Remove temp directory including shadow Git worktrees
	rm -rf "$(CURRENT_DIR)/temp"
