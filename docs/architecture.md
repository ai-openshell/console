# Architecture

OpenShell Dashboard is a web UI for one OpenShell gateway. One container image holds a Go backend-for-frontend (BFF) and the built React UI, and the Helm chart runs it with oauth2-proxy in front.

```
Browser ──HTTPS──► oauth2-proxy ──HTTP──► BFF ──gRPC──► OpenShell gateway
                   signs the user in,     REST API      validates the token,
                   keeps the session      and the UI    enforces roles
```

The proxy and the BFF are two containers in one pod: the BFF listens on `127.0.0.1`, and the Service exposes the proxy's port only.

**Frontend** (`frontend/`). React, TypeScript and PatternFly 6. `src/app/` is the shell: routes, layout and session. A page under `src/pages/` takes what it shows as props and loads its data through the hooks in `src/api/`, and every request goes through `src/api/client.ts`.

**BFF** (`backend/`). A Go HTTP server that serves the REST API under `/api/v1` and the built UI. Routes are declared in `pkg/server/app.go`, with one handler struct per resource in `pkg/handlers/`. Handlers call the interfaces in `pkg/services/`, and `pkg/models/` converts between the SDK's types and the JSON the UI reads. The BFF keeps no state.

**SDK.** The BFF reaches the gateway through the OpenShell Go SDK, `github.com/ai-openshell/sdk-go`, which `backend/go.mod` requires at the tag of the OpenShell release the branch is built on (sdk-go `vX.Y.Z` is the SDK for OpenShell `vX.Y.Z`). Until OpenShell 0.1.8 the SDK was the `sdk/go` directory of the core repository, `github.com/NVIDIA/OpenShell/sdk/go`; the Follow upstream bump moves a branch off that path.

The directory layout is in [development.md](development.md#layout).

## Principles

- **The gateway's API defines what exists.** The dashboard invents no RPC, field, lifecycle state or object. ([ADR 0003, decision 2](adrs/0003-gateway-api-openshell-sdk.md))
- **The BFF translates nothing.** Request and response bodies mirror the gateway's messages. When the gateway expects a value in a particular form, the UI sends it that way. ([ADR 0003, decision 3](adrs/0003-gateway-api-openshell-sdk.md))
- **Authentication is relayed.** The auth proxy (oauth2-proxy, which the Helm chart runs as a sidecar) signs the user in and forwards the access token. The BFF passes it to the gateway and neither validates it nor authorizes; the gateway does both. ([ADR 0002](adrs/0002-auth-relay-only-bff.md), [authentication.md](authentication.md))
- **Secrets never reach the browser.** The BFF returns the names of a provider's credentials and never a value. ([ADR 0003, decision 5](adrs/0003-gateway-api-openshell-sdk.md))
- **State is polled, and the terminal is the one WebSocket.** Lists, status and logs are fetched on an interval. The terminal relays the gateway's interactive exec stream. ([ADR 0003, decision 6](adrs/0003-gateway-api-openshell-sdk.md))
- **The SDK comes first.** The exception is three direct gRPC calls in `backend/pkg/clients`, each for a named gap in the SDK: `rawexec.go` (a command with stdin and no terminal, for binary-safe upload), `rawprovider.go` with `rawprovider_allworkspaces.go` (the names of the credentials a provider holds) and `rawprofile.go` (a provider profile's endpoints in full). ([ADR 0003, decision 4](adrs/0003-gateway-api-openshell-sdk.md))

## Roles

The gateway decides what a user may do. The UI reads the user's roles from the gateway and hides what a role cannot use: a convenience, not enforcement.

| Role | Who | The UI shows |
|---|---|---|
| Platform admin | Holds the gateway's admin role | Every workspace, and the gateway overview, lists across all workspaces, platform provider profiles, global policy and gateway settings |
| Workspace admin | A workspace member with the role `ADMIN` | The controls for members, providers, provider profiles, templates, sandbox policy and settings, and policy proposals |
| User | Any other workspace member | Sandboxes with their logs, terminal, files and services |

## Decisions

The decisions behind this design are in [`docs/adrs/`](adrs/README.md).
