# OpenShell Dashboard

Web admin UI for [OpenShell](https://github.com/NVIDIA/OpenShell): a Go BFF and a React frontend in one container image, installed with a Helm chart. The BFF talks to one OpenShell gateway through the OpenShell Go SDK.

## Project structure

```
frontend/           React + TypeScript + PatternFly 6
  src/app/          App shell (App.tsx), routes (AuthenticatedRoutes.tsx, AppRoutes.tsx), layout, theme
  src/pages/        Page components: flat files, self-contained
  src/components/   Shared components (shared hooks are in src/hooks/, helpers in src/utils/)
  src/api/          REST client (client.ts), hooks, queryKeys.ts
  src/i18n/         useI18n and the English catalogs (locales/en/)
  src/types/        TypeScript types, all in index.ts
backend/            Go BFF; the compat suite is in test/compat/ (make compat, needs Docker)
  cmd/server/       Entry point: flags and environment variables (main.go)
  pkg/server/       App wiring and chi routes (app.go)
  pkg/handlers/     REST handlers, one struct per resource (*_handler.go)
  pkg/services/     The interfaces the handlers depend on, thin over the SDK
  pkg/apiutils/     Shared HTTP helpers (WriteJSON, WriteError, DecodeBody, ResponseCode)
  pkg/auth/         Reads the bearer from the request (proxy.go)
  pkg/clients/      Bearer forwarding (auth.go) and the direct gRPC calls (raw*.go)
  pkg/models/       Response DTOs (From*() converters) and request builders
scripts/            Release and CI tooling (gateway-range.mjs, release/, retag-image.sh) and dev-env.sh (the local stack)
deploy/             Dockerfile, Helm chart (helm/openshell-dashboard), Keycloak realm (keycloak/), local stack (dev/), compat stack and gateway pins (ci/)
docs/               Guides (install, authentication, configuration, compatibility, development, architecture, releasing) and ADRs (adrs/)
```

## Build and run

```bash
make setup          # install frontend + go deps
make gateway        # the pinned OpenShell gateway in Docker, for make dev
make dev            # frontend dev server (:3000, hot reload) + BFF (:8080, restart after Go changes)
make test           # frontend unit tests + go tests
make lint           # eslint + prettier check + golangci-lint
make typecheck      # tsc --noEmit
make build          # build the container image
```

`make dev` talks to the gateway `make gateway` starts. `make dev-full` adds Keycloak and the auth proxy and serves the UI behind sign-in on `http://localhost:4180`. For a gateway of your own, point `OPENSHELL_GATEWAY_URL` and the TLS variables at it ([docs/configuration.md](docs/configuration.md#connecting-to-a-gateway-outside-the-chart)); the rest is in [docs/development.md](docs/development.md).

## Architecture rules

- **One application, one image, one chart** ([ADR 0001](docs/adrs/0001-standalone-application.md)). The dashboard ships as a container image and a Helm chart, and nothing is published to npm. Nothing a frontend module or a Go package exports is a supported interface. The BFF never calls the Kubernetes API, never fetches, stores or injects provider credentials, and holds no state.
- **Relay-only auth** ([ADR 0002](docs/adrs/0002-auth-relay-only-bff.md)). The BFF never terminates authentication: the auth proxy (oauth2-proxy, which the Helm chart runs as a sidecar) owns sign-in, sessions, refresh and CSRF and sets `x-forwarded-access-token`. Bearer chain: proxy header → `Authorization: Bearer` → 401. The BFF never validates tokens and never authorizes; the gateway does both. The only auth switch is `AUTH_DISABLED`, for local development.
- **The gateway's API as it is, through the OpenShell Go SDK** ([ADR 0003](docs/adrs/0003-gateway-api-openshell-sdk.md)). Never invent RPCs, fields, lifecycle states or objects: Sandbox is the object the gateway has, labels categorize, and there is no Agent. The BFF translates nothing. Bodies mirror the gateway's messages, and when the gateway wants something a particular way (a credential under its env var name, a provider's profile scope, a setting in its own type) the UI sends it that way; the OpenShell CLI and TUI are the reference (hard facts 8, 21 and 22 in `.claude/rules/openshell-api.md`). Use the SDK. The exceptions are three direct gRPC calls in `backend/pkg/clients`, each for a named gap in the SDK: `rawexec.go` (binary-safe upload), `rawprovider.go` with `rawprovider_allworkspaces.go` (the names of a provider's credentials) and `rawprofile.go` (provider profiles whole). Secrets never reach the browser. State is polled, and the terminal is the one WebSocket.
- **Frontend** ([ADR 0004](docs/adrs/0004-frontend.md)). PatternFly 6 only: no other component library and no project CSS files. `src/app/` owns the frame; a page takes props, loads its data through the `src/api/` hooks and renders no app-shell chrome. Every request goes through `src/api/client.ts`. Feature flags hide features in the UI; they are not access control. New UI text goes through `useI18n` from `src/i18n`; most pages still have theirs inline.
- **One gateway release line** ([ADR 0005](docs/adrs/0005-gateway-compatibility-and-releases.md)). A dashboard `0.1.x` is for gateways `0.1.x`. A branch is built on one OpenShell release, named in `deploy/ci/gateway-pins.json`, and every change is tested against it. The Follow upstream workflow moves the pins and the SDK, on a `bump/openshell-X.Y.Z` branch with a pull request into `main`; never move them by hand (hard facts 17 and 20).

## Roles

- **Platform admin**, across workspaces: gateway overview and settings, creating and deleting workspaces, platform provider profiles, global policy, lists across all workspaces
- **Workspace admin**, in a workspace: members, providers, provider profiles, sandbox policies and settings
- **User**, in a workspace: sandboxes, logs, terminal, files
