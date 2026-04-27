SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c

.DEFAULT_GOAL := help

.PHONY: help setup setup-macos setup-ubuntu dev run-docling run-ollama run-zora llm-pull doctor test package-check package-snapshot version scan

help: ## Show available local development commands.
	@awk 'BEGIN {FS = ":.*##"; printf "Zora development commands:\n\n"} /^[a-zA-Z0-9_-]+:.*##/ {printf "  %-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

setup: ## Install host dependencies for this OS, including Ollama.
	@case "$$(uname -s)" in \
		Darwin) scripts/bootstrap-macos.sh ;; \
		Linux) scripts/bootstrap-ubuntu.sh ;; \
		*) echo "unsupported OS: $$(uname -s)" >&2; exit 1 ;; \
	esac

setup-macos: ## Install macOS dependencies with Homebrew, including Ollama.
	@scripts/bootstrap-macos.sh

setup-ubuntu: ## Install Ubuntu dependencies with apt, including Ollama.
	@scripts/bootstrap-ubuntu.sh

dev: ## Run Docling, Ollama, and Zora together for local development.
	@scripts/dev-up.sh

run-docling: ## Run only the local Docling service.
	@scripts/run-docling-dev.sh

run-ollama: ## Run only the local Ollama service.
	@scripts/run-ollama-dev.sh

run-zora: ## Run only the local Zora server.
	@scripts/run-zora-dev.sh

llm-pull: ## Pull the default local LLM for action item generation.
	@scripts/pull-llm-dev.sh

doctor: ## Check local tools, paths, and service health.
	@scripts/doctor.sh

test: ## Run the CGO-free Go test suite.
	@CGO_ENABLED=0 go test ./...

package-check: ## Validate the GoReleaser configuration.
	@goreleaser check

package-snapshot: ## Build snapshot tarballs and local .deb packages.
	@goreleaser release --snapshot --clean

version: ## Print zora build metadata through go run.
	@go run ./cmd/zora version

scan: ## Ask the running local server to scan .local/inbox now.
	@curl -fsS -X POST http://127.0.0.1:8787/ingest/scan >/dev/null
	@echo "scan requested"
