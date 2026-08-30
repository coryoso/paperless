.DEFAULT_GOAL := help

BINARY ?= bin/paperless
CONFIG ?= $(HOME)/.paperless/config.toml
MODEL ?= qwen3.5:9b-q4_K_M
FILE ?=
INBOX ?=
BASE ?=
ARCHIVE ?=
STATE_DIR ?=
FORCE ?=

CONFIGURE_FLAGS = $(if $(FORCE),--force) $(if $(BASE),--base "$(BASE)") $(if $(INBOX),--inbox "$(INBOX)") $(if $(ARCHIVE),--archive "$(ARCHIVE)") $(if $(STATE_DIR),--state-dir "$(STATE_DIR)")

.PHONY: help build web-install web-build web-dev web-test setup configure init init-folders doctor run serve open process dry-run model test test-unit test-race acceptance fmt vet check sqlc service-install service-start service-stop service-status

help: ## Show the available commands and optional variables.
	@awk 'BEGIN {FS = ":.*## "; printf "Paperless local commands\n\n"} /^[a-zA-Z0-9_-]+:.*## / {printf "  %-17s %s\n", $$1, $$2} END {printf "\nVariables: CONFIG, INBOX, BASE, ARCHIVE, STATE_DIR, FILE, MODEL, FORCE\n"}' $(MAKEFILE_LIST)

build: web-build ## Build the React client and compile the single Paperless binary.
	@mkdir -p "$(dir $(BINARY))"
	go build -o "$(BINARY)" ./cmd/paperless

web-install: ## Install the Bun-managed React client dependencies.
	cd web && bun install --frozen-lockfile

web-build: ## Build the client-side React app for embedding in Go.
	cd web && bun run build

web-dev: ## Run the React development server with API proxying to Paperless.
	cd web && bun run dev

web-test: ## Run the React client tests.
	cd web && bun run test

setup: build ## First-time setup: create config, install dependencies, create folders, and check everything.
	@if [ ! -f "$(CONFIG)" ]; then \
		"$(BINARY)" --config "$(CONFIG)" configure $(CONFIGURE_FLAGS); \
	else \
		echo "Using existing config: $(CONFIG)"; \
	fi
	"$(BINARY)" --config "$(CONFIG)" init
	"$(BINARY)" --config "$(CONFIG)" doctor

configure: build ## Create a config; pass INBOX, ARCHIVE, BASE, or STATE_DIR. Use FORCE=1 to replace it.
	"$(BINARY)" --config "$(CONFIG)" configure $(CONFIGURE_FLAGS)

init: build ## Install runtime tools/model and create all configured folders, including the inbox.
	"$(BINARY)" --config "$(CONFIG)" init

init-folders: build ## Create folders and the database without installing Homebrew tools or a model.
	"$(BINARY)" --config "$(CONFIG)" init --skip-install

doctor: build ## Check OCR tools, language data, Ollama, model, and configured paths.
	"$(BINARY)" --config "$(CONFIG)" doctor

run: build ## Run the inbox watcher and dashboard together. Pass INBOX for a temporary override.
	"$(BINARY)" --config "$(CONFIG)" run $(if $(INBOX),--inbox "$(INBOX)")

serve: build ## Run only the dashboard, without watching the inbox.
	"$(BINARY)" --config "$(CONFIG)" serve

open: ## Open the local dashboard in the default browser.
	open http://127.0.0.1:8844

process: build ## Process one pass of the configured inbox and exit.
	"$(BINARY)" --config "$(CONFIG)" process-once

dry-run: build ## Analyze one document without filing it: make dry-run FILE=/path/to/document.pdf
	@test -n "$(FILE)" || (echo "FILE is required. Example: make dry-run FILE=/path/to/document.pdf"; exit 2)
	"$(BINARY)" --config "$(CONFIG)" dry-run "$(FILE)"

model: ## Download the local Ollama model. Override with MODEL=another-tag.
	ollama pull "$(MODEL)"

test: ## Run the complete Go test suite.
	go test -count=1 ./...

test-unit: ## Run fast isolated tests without local OCR acceptance checks.
	go test -short -count=1 ./...

test-race: ## Run the test suite with Go's race detector.
	go test -race -count=1 ./...

acceptance: ## Test the real OCR pipeline: make acceptance FILE=/path/to/document.pdf
	@test -n "$(FILE)" || (echo "FILE is required. Example: make acceptance FILE=/path/to/document.pdf"; exit 2)
	PAPERLESS_ACCEPTANCE_PDF="$(FILE)" go test -count=1 ./internal/ocr ./internal/app

fmt: ## Format all Go source files.
	gofmt -w $$(rg --files cmd internal -g '*.go')

vet: ## Run Go's static checks.
	go vet ./...

check: web-test web-build fmt vet test ## Build and test the React client and Go application.

sqlc: ## Regenerate type-safe database access after changing SQL.
	@command -v sqlc >/dev/null || go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
	sqlc generate

service-install: build ## Install the macOS LaunchAgent.
	"$(BINARY)" --config "$(CONFIG)" service install

service-start: build ## Start the installed macOS LaunchAgent.
	"$(BINARY)" --config "$(CONFIG)" service start

service-stop: build ## Stop the installed macOS LaunchAgent.
	"$(BINARY)" --config "$(CONFIG)" service stop

service-status: build ## Show the macOS LaunchAgent status.
	"$(BINARY)" --config "$(CONFIG)" service status
