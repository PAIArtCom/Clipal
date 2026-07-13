.PHONY: build test test-unit test-smoke test-data-transfer test-live-oauth test-live-codex-oauth test-live-claude-oauth test-live-gemini-oauth test-oauth-authorize lint lint-fix vuln fmt install-hooks ci

GO ?= go
GOBIN := $(shell $(GO) env GOPATH)/bin
GOLANGCI_LINT ?= $(shell command -v golangci-lint 2>/dev/null || echo "$(GOBIN)/golangci-lint")
GOVULNCHECK ?= $(shell command -v govulncheck 2>/dev/null || echo "$(GOBIN)/govulncheck")

build:
	./scripts/build.sh

test: test-unit

test-unit:
	$(GO) test ./...

test-smoke:
	./scripts/smoke_test.sh

test-data-transfer:
	$(GO) test -count=1 ./cmd/clipal ./internal/transfer ./internal/integration
	$(GO) test -race -count=1 ./internal/transfer ./internal/proxy ./internal/telemetry ./internal/web
	node --test internal/web/static/app.test.js
	./scripts/data_transfer_smoke.sh

test-live-oauth:
ifeq ($(PROVIDER),codex)
	$(MAKE) test-live-codex-oauth \
		CONFIG_DIR="$(or $(CODEX_CONFIG_DIR),$(CONFIG_DIR))" \
		OAUTH_EMAIL="$(or $(CODEX_OAUTH_EMAIL),$(OAUTH_EMAIL))" \
		OAUTH_REF="$(or $(CODEX_OAUTH_REF),$(OAUTH_REF))" \
		OAUTH_FILE="$(or $(CODEX_OAUTH_FILE),$(OAUTH_FILE))" \
		MODEL="$(or $(CODEX_MODEL),$(MODEL))" \
		SKIP_STREAM="$(SKIP_STREAM)" \
		SKIP_REFRESH_RETRY="$(SKIP_REFRESH_RETRY)" \
		KEEP_TEMP="$(KEEP_TEMP)"
else ifeq ($(PROVIDER),claude)
	$(MAKE) test-live-claude-oauth \
		CONFIG_DIR="$(or $(CLAUDE_CONFIG_DIR),$(CONFIG_DIR))" \
		OAUTH_EMAIL="$(or $(CLAUDE_OAUTH_EMAIL),$(OAUTH_EMAIL))" \
		OAUTH_REF="$(or $(CLAUDE_OAUTH_REF),$(OAUTH_REF))" \
		OAUTH_FILE="$(or $(CLAUDE_OAUTH_FILE),$(OAUTH_FILE))" \
		MODEL="$(or $(CLAUDE_MODEL),$(MODEL))" \
		SKIP_STREAM="$(SKIP_STREAM)" \
		SKIP_COUNT_TOKENS="$(or $(CLAUDE_SKIP_COUNT_TOKENS),$(SKIP_COUNT_TOKENS))" \
		SKIP_REFRESH_RETRY="$(SKIP_REFRESH_RETRY)" \
		KEEP_TEMP="$(KEEP_TEMP)"
else ifeq ($(PROVIDER),gemini)
	$(MAKE) test-live-gemini-oauth \
		CONFIG_DIR="$(or $(GEMINI_CONFIG_DIR),$(CONFIG_DIR))" \
		OAUTH_EMAIL="$(or $(GEMINI_OAUTH_EMAIL),$(OAUTH_EMAIL))" \
		OAUTH_REF="$(or $(GEMINI_OAUTH_REF),$(OAUTH_REF))" \
		OAUTH_FILE="$(or $(GEMINI_OAUTH_FILE),$(OAUTH_FILE))" \
		MODEL="$(or $(GEMINI_MODEL),$(MODEL))" \
		SKIP_STREAM="$(SKIP_STREAM)" \
		SKIP_REFRESH_RETRY="$(SKIP_REFRESH_RETRY)" \
		KEEP_TEMP="$(KEEP_TEMP)"
else
	@echo "usage: make test-live-oauth PROVIDER=codex|claude|gemini [CONFIG_DIR=... or <PROVIDER>_CONFIG_DIR=...] [OAUTH_EMAIL=...|OAUTH_REF=...|OAUTH_FILE=... or <PROVIDER>_OAUTH_*=...] [MODEL=... or <PROVIDER>_MODEL=...]"
	@exit 1
endif

test-live-codex-oauth:
	CLIPAL_LIVE_CONFIG_DIR="$(CONFIG_DIR)" \
	CLIPAL_LIVE_OAUTH_EMAIL="$(OAUTH_EMAIL)" \
	CLIPAL_LIVE_OAUTH_REF="$(OAUTH_REF)" \
	CLIPAL_LIVE_OAUTH_FILE="$(OAUTH_FILE)" \
	CLIPAL_LIVE_MODEL="$(MODEL)" \
	CLIPAL_LIVE_SKIP_STREAM="$(SKIP_STREAM)" \
	CLIPAL_LIVE_SKIP_REFRESH_RETRY="$(SKIP_REFRESH_RETRY)" \
	CLIPAL_LIVE_KEEP_TEMP="$(KEEP_TEMP)" \
	./scripts/live_codex_oauth_smoke.sh

test-live-claude-oauth:
	CLIPAL_LIVE_CONFIG_DIR="$(CONFIG_DIR)" \
	CLIPAL_LIVE_OAUTH_EMAIL="$(OAUTH_EMAIL)" \
	CLIPAL_LIVE_OAUTH_REF="$(OAUTH_REF)" \
	CLIPAL_LIVE_OAUTH_FILE="$(OAUTH_FILE)" \
	CLIPAL_LIVE_MODEL="$(MODEL)" \
	CLIPAL_LIVE_SKIP_STREAM="$(SKIP_STREAM)" \
	CLIPAL_LIVE_SKIP_COUNT_TOKENS="$(SKIP_COUNT_TOKENS)" \
	CLIPAL_LIVE_SKIP_REFRESH_RETRY="$(SKIP_REFRESH_RETRY)" \
	CLIPAL_LIVE_KEEP_TEMP="$(KEEP_TEMP)" \
	./scripts/live_claude_oauth_smoke.sh

test-live-gemini-oauth:
	CLIPAL_LIVE_CONFIG_DIR="$(CONFIG_DIR)" \
	CLIPAL_LIVE_OAUTH_EMAIL="$(OAUTH_EMAIL)" \
	CLIPAL_LIVE_OAUTH_REF="$(OAUTH_REF)" \
	CLIPAL_LIVE_OAUTH_FILE="$(OAUTH_FILE)" \
	CLIPAL_LIVE_MODEL="$(MODEL)" \
	CLIPAL_LIVE_SKIP_STREAM="$(SKIP_STREAM)" \
	CLIPAL_LIVE_SKIP_REFRESH_RETRY="$(SKIP_REFRESH_RETRY)" \
	CLIPAL_LIVE_KEEP_TEMP="$(KEEP_TEMP)" \
	./scripts/live_gemini_oauth_smoke.sh

test-oauth-authorize:
	./scripts/oauth_authorize_smoke.sh --mock

lint:
	$(GOLANGCI_LINT) run ./...

lint-fix:
	$(GOLANGCI_LINT) run --fix ./...

vuln:
	$(GOVULNCHECK) ./...

fmt:
	gofmt -w $$(find cmd internal -type f -name '*.go' | sort)

install-hooks:
	./scripts/install_git_hooks.sh

ci: test-unit lint vuln test-smoke
