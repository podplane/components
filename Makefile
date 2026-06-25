# Podplane <https://podplane.dev>
# Copyright The Podplane Authors
# SPDX-License-Identifier: Apache-2.0

CURRENT_DIR := $(abspath $(dir $(firstword $(MAKEFILE_LIST))))
CHARTS := $(patsubst %/Chart.yaml,%,$(wildcard charts/*/Chart.yaml))
JSON_FILES := $(shell find manifests -name '*.json' -type f 2>/dev/null | sort)
PODPLANE_GIT_CACHE_DIR ?= $(HOME)/.podplane/cache/deps/git

.DEFAULT_GOAL := help

.PHONY: help fmt check deps lint render validate test check-crds update-crds update-manifests release-manifest precommit ci kind-create bootstrap recommended dev-bootstrap git-sync dev-sync dev-watch kind-delete clean

help: ## Show available targets
	@echo "Podplane Kubernetes PaaS Components"
	@echo ""
	@echo "Usage: make <target>"
	@awk 'BEGIN {FS = ":.*?## "} /^##@/ {printf "\n\033[1m%s\033[0m\n", substr($$0, 5)} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-17s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@echo ""
	@echo "Typical local test workflow:"
	@echo "  make dev-sync"
	@echo "  make kind-create"
	@echo "  make dev-bootstrap"
	@echo "  make kind-delete"

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
		helm lint $$chart || exit 1; \
	done

render: deps ## Run helm template on every chart
	@for chart in $(CHARTS); do \
		echo "==> $$chart"; \
		helm template $$chart >/dev/null || exit 1; \
	done

validate: render ## Alias for render

test: ## Run Go tests
	go test ./...

precommit: check ## Fast local pre-commit check: JSON fmt + helm lint (no network)
	@command -v helm >/dev/null 2>&1 || { echo "helm is required but not installed"; exit 1; }
	@for chart in $(CHARTS); do \
		helm lint --quiet $$chart >/dev/null || { helm lint $$chart; exit 1; }; \
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
	@jq --arg version "$$VERSION" '.components.version = $$version | .components.source = {"url":"https://github.com/podplane/components.git","ref":{"semver":("^" + $$version)}}' manifests/components.json > "dist/release/components_$${VERSION}.json"
	@jq . "dist/release/components_$${VERSION}.json" >/dev/null
	@echo "wrote dist/release/components_$${VERSION}.json"

##@ Local Development

kind-create: ## Create the local Kind cluster and mount temp/kind-git
	@mkdir -p temp/kind-git
	kind create cluster --config kind.yaml
	kubectl config use-context kind-podplane
	@# CoreDNS chart mounts /run/systemd/resolve/resolv.conf as hostPath type: File
	@# (correct for production nodes running systemd-resolved). Kind nodes don't run
	@# systemd-resolved, so symlink that path to the node's own /etc/resolv.conf,
	@# which Docker populates with valid upstream nameservers.
	@for node in $$(kind get nodes --name podplane); do \
		docker exec $$node sh -c 'mkdir -p /run/systemd/resolve && ln -sf /etc/resolv.conf /run/systemd/resolve/resolv.conf'; \
	done

bootstrap: ## Run bootstrap.sh against the current kubectl context (minimal/core components only)
	@./bootstrap.sh

recommended: ## Run bootstrap.sh with recommended components using the local Podplane Git cache
	@test -f "$(PODPLANE_GIT_CACHE_DIR)/components.git/config" || { echo "Error: local components Git cache not found at $(PODPLANE_GIT_CACHE_DIR)/components.git. Run 'make git-sync' first." >&2; exit 1; }
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
		DOMAIN="$${DOMAIN:-default.localhost}" PLATFORM_INSTALL=recommended ./bootstrap.sh

dev-bootstrap: ## Run bootstrap.sh with Flux pointed at the local development Git source and all components enabled
	@KIND_NODE_IP=$$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}' 2>/dev/null | awk '{print $$1}'); \
	if [ -z "$$KIND_NODE_IP" ]; then \
		echo "Error: could not determine node InternalIP. Is the cluster running? (make kind-create)" >&2; \
		exit 1; \
	fi; \
	KIND_LOCAL_GIT=1 \
		PLATFORM_INSTALL=all \
		PLATFORM_GIT_REPOSITORY_URL=http://components-git.platform-fluxcd.svc.cluster.local/components.git \
		PLATFORM_GIT_REPOSITORY_BRANCH=local-dev \
		CILIUM_K8S_SERVICE_HOST=$$KIND_NODE_IP \
		./bootstrap.sh

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
	@git -C temp/git-sync/worktree config user.name "Podplane Dev Sync"
	@git -C temp/git-sync/worktree config user.email "dev-sync@podplane.local"
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

dev-sync: ## Snapshot this checkout into temp/kind-git for local development testing
	@mkdir -p temp/kind-git
	@if [ ! -d temp/kind-git/components.git ]; then git init --bare temp/kind-git/components.git >/dev/null; fi
	@mkdir -p temp/kind-git/worktree
	@if [ ! -d temp/kind-git/worktree/.git ]; then \
		git init temp/kind-git/worktree >/dev/null; \
		git -C temp/kind-git/worktree checkout -b local-dev >/dev/null; \
	elif [ "$$(git -C temp/kind-git/worktree symbolic-ref --quiet --short HEAD || true)" != "local-dev" ]; then \
		if git -C temp/kind-git/worktree show-ref --verify --quiet refs/heads/local-dev; then \
			git -C temp/kind-git/worktree checkout local-dev >/dev/null; \
		else \
			git -C temp/kind-git/worktree checkout -b local-dev >/dev/null; \
		fi; \
	fi
	@git -C temp/kind-git/worktree config user.name "Podplane Dev Sync"
	@git -C temp/kind-git/worktree config user.email "dev-sync@podplane.local"
	@git -C temp/kind-git/worktree config commit.gpgsign false
	@rsync -a --delete \
		--exclude .git \
		--exclude temp \
		--exclude 'charts/*/charts' \
		--exclude vendor \
		--exclude dist \
		./ temp/kind-git/worktree/
	@git -C temp/kind-git/worktree add -A
	@if git -C temp/kind-git/worktree diff --cached --quiet; then \
		echo "No changes to sync."; \
	else \
		git -C temp/kind-git/worktree commit -m "dev sync $$(date -u +%Y-%m-%dT%H:%M:%SZ)" >/dev/null; \
		git -C temp/kind-git/worktree push --force ../components.git local-dev:local-dev >/dev/null; \
		echo "Synced local checkout to temp/kind-git/components.git (local-dev)."; \
	fi

dev-watch: ## Run dev-sync automatically when local files change
	@command -v watchexec >/dev/null || \
		(echo "Error: watchexec is required for dev-watch." >&2; exit 1)
	watchexec --watch . --ignore .git --ignore temp --debounce 1s -- $(MAKE) dev-sync

kind-delete: ## Delete the local Kind cluster
	kind delete cluster --name podplane

clean: ## Remove temp directory including shadow Git repo temp/kind-git
	rm -rf "$(CURRENT_DIR)/temp"
