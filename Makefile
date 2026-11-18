IMAGE_NAME ?= console
TAG ?= latest
PLATFORMS ?= linux/amd64,linux/arm64

.PHONY: help setup gateway dev dev-full dev-mock dev-mock-empty dev-backend dev-frontend dev-proxy dev-logs dev-down dev-reset build build-frontend build-backend test lint lint-go typecheck format format-check clean buildx

help: ## List the targets
	@awk -F ':.*## ' '/^[a-zA-Z0-9_-]+:.*## / {printf "  %-14s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

setup: ## Install frontend deps and Go deps
	cd frontend && npm install
	cd backend && go mod download

gateway: ## Start the OpenShell gateway this branch pins, in Docker, for make dev
	scripts/dev-env.sh up

dev: ## Frontend dev server (:3000) and Go BFF (:8080), without sign-in
	@scripts/dev-env.sh ports
	@eval "$$(scripts/dev-env.sh env)" && $(MAKE) -j2 dev-backend dev-frontend

dev-full: ## The same behind sign-in: starts the gateway, Keycloak and the auth proxy (:4180)
	@scripts/dev-env.sh ports --sign-in
	@scripts/dev-env.sh proxy --build
	scripts/dev-env.sh up --sign-in
	@eval "$$(scripts/dev-env.sh env --sign-in)" && $(MAKE) -j3 dev-backend dev-frontend dev-proxy

# UI work against fixtures instead of a gateway. A Vite middleware plugin
# answers /api/v1 itself, so neither the BFF nor a gateway has to be running.
# See docs/mock-api.md for what the fixtures cover.
dev-mock: ## Start the frontend only (:3000), served from mock API data
	cd frontend && MOCK_API=true npm start

# The same fixtures, emptied: the one `default` workspace a gateway starts
# with and nothing in it. This is how the Gateway page's get-started
# walkthrough, which stands in for the counts, is reached.
dev-mock-empty: ## Start the frontend only (:3000), as a gateway nobody has used yet
	cd frontend && MOCK_API=true MOCK_EMPTY=true npm start

# Sign-in is off unless the caller says otherwise: AUTH_DISABLED=false make dev.
# make dev-full does, and adds what the BFF needs behind the auth proxy.
#
# The BFF listens on this machine's loopback address only: with sign-in off,
# whoever reaches it is a platform admin of the gateway behind it.
# LISTEN_ADDRESS=0.0.0.0 make dev opens it to the network.
#
# Nothing is passed for the compatibility notice: the gateway release line the
# BFF is for is compiled in. To see the notice against a gateway of that line,
# start the BFF for another one: GATEWAY_RELEASE_LINE=9.9 make dev
dev-backend:
	cd backend && AUTH_DISABLED=$${AUTH_DISABLED:-true} LISTEN_ADDRESS=$${LISTEN_ADDRESS:-127.0.0.1} go run ./cmd/server

dev-frontend:
	cd frontend && npm start

dev-proxy:
	scripts/dev-env.sh proxy

dev-logs: ## Follow the logs of the gateway and Keycloak
	scripts/dev-env.sh logs

dev-down: ## Stop what make gateway or make dev-full started; the gateway's data stays
	scripts/dev-env.sh down

dev-reset: ## Stop it and delete its data and its sandboxes' containers
	scripts/dev-env.sh reset

build: ## Build the container image (BFF + static frontend)
	docker build -t console:latest -f deploy/Dockerfile .

buildx:
	docker buildx build \
		--platform ${PLATFORMS} \
		-t ${IMAGE_NAME}:${TAG} \
		-f deploy/Dockerfile \
		--load .

build-frontend:
	cd frontend && npm run build

build-backend:
	cd backend && go build -o bin/server ./cmd/server

.PHONY: test test-backend test-frontend
test: test-backend test-frontend ## Frontend unit tests + go tests

test-backend:
	cd backend && go test ./...

test-frontend:
	cd frontend && npm test -- --passWithNoTests

# Left unset, the compat targets run the gateway this branch pins
# (deploy/ci/gateway-pins.json), by digest: the one CI runs. Set it to try
# another release by its tag.
export OPENSHELL_VERSION

# For the compat targets only: where the compat gateway keeps its data, and
# where a sandbox's supervisor calls it back (its published port, on the
# Docker host). The environment overrides both. The data in the default
# directory is removed when the gateway is. CI calls deploy/ci/e2e-stack.sh
# itself, with its own values.
compat compat-up compat-down: export OPENSHELL_STATE_DIR ?= $(CURDIR)/scripts/.state/compat
compat compat-up compat-down: export OPENSHELL_GRPC_ENDPOINT ?= http://127.0.0.1:8080

.PHONY: compat compat-up compat-down
compat: ## Gateway compat suite vs the pinned gateway (another: OPENSHELL_VERSION=0.1.2 make compat)
	@scripts/dev-env.sh ports --compat
	@mkdir -p "$$OPENSHELL_STATE_DIR" 2>/dev/null || true
	deploy/ci/e2e-stack.sh run; status=$$?; scripts/dev-env.sh compat-clean; exit $$status

# compat-up leaves the compat suite's gateway running on port 8080, to run
# single tests against. They go through a BFF on port 9080, in backend/:
#   AUTH_DISABLED=true OPENSHELL_GATEWAY_URL=localhost:8080 PORT=9080 go run ./cmd/server
#   BFF_URL=http://localhost:9080 go test -tags compat -count=1 -run <Test> ./test/compat/...
compat-up:
	@scripts/dev-env.sh ports --compat
	@mkdir -p "$$OPENSHELL_STATE_DIR" 2>/dev/null || true
	deploy/ci/e2e-stack.sh up

compat-down: ## Remove the compat suite's gateway, if make compat-up or a stopped run left it
	deploy/ci/e2e-stack.sh down
	@scripts/dev-env.sh compat-clean

.PHONY: helm-lint helm-test helm-docs
helm-lint: ## Lint and render Helm charts and their example values
	@set -eu; \
	for chart_file in deploy/helm/*/Chart.yaml; do \
		[ -f "$$chart_file" ] || continue; \
		chart=$${chart_file%/Chart.yaml}; \
		set --; \
		if [ "$$chart" = deploy/helm/console ]; then \
			set -- --set-string oidc.issuer=https://idp.example.com/realms/openshell; \
		fi; \
		helm lint "$$chart" "$$@"; \
		helm template lint "$$chart" "$$@" >/dev/null; \
		for values in "$$chart"/ci/values-*.yaml; do \
			[ -f "$$values" ] || continue; \
			helm lint "$$chart" -f "$$values" "$$@"; \
			helm template lint "$$chart" -f "$$values" "$$@" >/dev/null; \
		done; \
	done

helm-test: ## Run unit tests for every Helm chart
	@set -eu; \
	for chart_file in deploy/helm/*/Chart.yaml; do \
		[ -f "$$chart_file" ] || continue; \
		helm unittest "$${chart_file%/Chart.yaml}"; \
	done

helm-docs: ## Generate documentation for every Helm chart via mise
	mise run helm:docs --chart-search-root deploy/helm

lint: ## eslint + golangci-lint + prettier check
	cd frontend && npm run lint
	cd frontend && npm run format:check
	$(MAKE) lint-go

lint-go: ## golangci-lint (requires golangci-lint installed)
	cd backend && golangci-lint run ./...

typecheck: ## tsc --noEmit
	cd frontend && npm run typecheck

format: ## Auto-format frontend code with Prettier
	cd frontend && npm run format

format-check: ## Check frontend formatting without writing
	cd frontend && npm run format:check

clean:
	rm -rf frontend/dist backend/bin
