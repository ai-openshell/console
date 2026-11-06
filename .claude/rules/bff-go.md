---
description: Go BFF conventions for the OpenShell Dashboard backend
globs: "backend/**/*.go"
alwaysApply: false
---

# Go BFF Conventions

## Directory structure

All of the BFF's packages live under `pkg/`. There is no `backend/internal/`
tree.

```
backend/
├── cmd/server/main.go        # Entry point, flag parsing, server setup
├── pkg/
│   ├── server/                # App wiring and routing
│   │   └── app.go             # App struct, NewApp(), Routes()
│   ├── handlers/              # HTTP handlers, one struct per resource
│   │   ├── *_handler.go       # SandboxHandler, WorkspacesHandler, ...
│   │   ├── *_handler_test.go  # Table-driven handler tests
│   │   └── mock_sdk_test.go   # SDK test doubles
│   ├── services/              # The interfaces the handlers depend on, thin over the SDK
│   │   ├── sandbox.go         # SandboxServiceInterface, NewSandboxService
│   │   └── ...                # One file per resource
│   ├── apiutils/              # Shared HTTP helpers
│   │   └── respond.go         # WriteJSON, WriteError, WriteSDKError, DecodeBody,
│   │                          # ValidDNS1123, ResponseCode constants
│   ├── auth/                  # Auth middleware
│   │   └── proxy.go           # Reads the bearer from the request headers
│   ├── clients/               # Bearer forwarding and the three direct gRPC calls
│   │   ├── auth.go            # Per-request bearer forwarding
│   │   ├── rawexec.go         # Non-TTY stdin exec for binary uploads
│   │   ├── rawprovider.go     # Keys of the credentials a provider holds
│   │   └── rawprofile.go      # Provider profiles, whole (endpoints included)
│   └── models/                # Response DTOs and request builders
│       ├── models.go          # DTOs shared with the frontend, FeatureFlags
│       ├── auth.go            # AuthConfigResponse
│       ├── builders.go        # Request structs and lightweight builders
│       ├── sdk_converters.go  # SDK <-> frontend JSON conversion
│       ├── policyproto.go     # SDK policy types <-> the SDK's proto, for protojson
│       ├── observability.go   # DTOs for logs, policy revisions, drafts, services and settings
│       ├── provider_profile.go        # Provider profile DTOs, endpoints as protojson
│       ├── gateway_release_line.go    # The release line compiled into the build
│       └── compatibility.go           # The verdict on a gateway's version
├── test/compat/               # Compat suite, run against a real gateway
├── go.mod
└── go.sum
```

## Router

Routes are declared in `pkg/server/app.go` with `go-chi/chi` and dispatch to a
handler struct. Method names carry no `Handler` suffix — the struct does:

```go
func (h *SandboxHandler) ListSandboxes(w http.ResponseWriter, r *http.Request)
```

URL params via `r.PathValue("workspace")`. chi sets these on every matched
route, so handlers do not import chi.

## Gateway client

The OpenShell Go SDK is the source of truth:

```go
import openshell "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"
```

Handlers never hold `openshell.ClientInterface` directly. `NewApp` resolves the
SDK sub-clients once (`Sandboxes()`, `Workspaces()`, `Providers()`, `Exec()`,
`Policy()`, `Services()`, ...), wraps each in a `pkg/services` type, and injects
that interface into the handler as `h.svc`. A handler therefore depends on a
narrow interface, not on the whole SDK client.

There are three direct gRPC calls in `pkg/clients`, each for a named gap in
the SDK. All use the proto client the SDK ships:

- `rawexec.go` streams a file into a sandbox for binary-safe uploads, because
  the public exec API lacks a non-TTY stdin path.
- `rawprovider.go` reads the keys of the credentials a provider holds, because
  the SDK's converter drops the redacted `credentials` map the gateway returns
  them in. `app.SetProviderCredentialKeys` wires it in; handlers reach it
  through `services.ProviderCredentialKeyReader`, beside the SDK call that
  returns the provider itself. `rawprovider_allworkspaces.go` is the same
  read for the all-workspaces provider list.
- `rawprofile.go` reads and writes provider profiles, because the SDK's
  profile endpoint type keeps a host, a port and a protocol and drops the
  access preset, enforcement and L7 rules the gateway stores with them. An
  update replaces the stored profile, so a profile that went through the SDK
  type would come back narrower than it was.

Do not add local wrappers, copied protos or generated stub trees. Add a direct
gRPC call only for an SDK gap you can name, state the gap at the top of its
file, and delete the call when the SDK closes the gap (ADR 0003, decision 4).

## Mirror the gateway

The BFF translates nothing (ADR 0003, decision 3). A request body carries what
the gateway's message carries and is forwarded as it is; a response carries
what the gateway returned, minus secrets. When a request fails because the
gateway wants it another way, change what the UI sends, not what the BFF
forwards. In particular the BFF does not rename provider credential keys, work
out a provider's profile scope, or guess the type of a setting, and it sends
an update only what the request names rather than a resource it read first.

## Handlers

Handlers use the exported helpers from `pkg/apiutils`:

```go
func (h *SandboxHandler) GetSandbox(w http.ResponseWriter, r *http.Request) {
    sandbox, err := h.svc.Get(r.Context(), r.PathValue("workspace"), r.PathValue("name"))
    if err != nil {
        apiutils.WriteSDKError(w, err)
        return
    }
    apiutils.WriteJSON(w, http.StatusOK, models.FromSDKSandbox(sandbox))
}
```

Key patterns:

- `apiutils.DecodeBody(w, r, &dst)` — handles MaxBytesReader, DisallowUnknownFields, writes error response on failure, returns false
- `apiutils.WriteJSON(w, statusCode, payload)` — marshals and writes
- `apiutils.WriteError(w, statusCode, code, message)` — writes ErrorResponse envelope; `code` is an `apiutils.ResponseCode` constant, never a bare string. Add a new constant rather than inlining a literal.
- `apiutils.WriteSDKError(w, err)` — maps SDK and fallback gRPC status errors to HTTP status codes
- `apiutils.ValidDNS1123(name)` — validates resource names
- Convert SDK responses through `models.FromSDK*()` helpers or explicit DTO assembly before serializing to JSON
- For policy JSON, preserve the existing protojson contract through `models.ParseSDKPolicy` / `marshalSDKPolicy`; do not hand-roll `map[string]any` policy parsing

## Auth

Relay-only (ADR 0002): the BFF never terminates authentication. The auth proxy (oauth2-proxy, which the Helm chart runs as a sidecar) owns sign-in, sessions, refresh and CSRF, and sets the bearer.

Bearer resolution is one precedence chain in `pkg/auth/proxy.go`:

1. `x-forwarded-access-token` header (set by the auth proxy)
2. `Authorization: Bearer` header (API clients)
3. No bearer → 401

The token lands in request context; `clients.ContextAuthProvider` forwards it
on every SDK/gRPC call as `authorization: Bearer` metadata. Gateway enforces
RBAC (admin/user roles) and workspace membership — the BFF never does.

The BFF does NOT validate tokens, call JWKS endpoints, parse JWTs, or make authorization decisions. Zero dependency on `go-oidc`. There are no OIDC endpoints, no session codec, no CSRF middleware — if you find yourself adding any of these, stop and read ADR 0002.

Auth-adjacent routes (under `/api/v1/`): `auth/config` (bootstrap: authDisabled + feature flags), `auth/whoami` (gateway `GetCurrentUser`). That's all.

Before adding anything auth-adjacent, check ADR 0002 (no auth termination, no JWT validation, no RBAC) and ADR 0001, decision 4 (no Kubernetes API calls, no credential brokering, no server-side state).

## Configuration

Every setting is a CLI flag with an environment variable, read in `cmd/server/main.go`. The exceptions are the `FEATURE_*` variables, which have no flag, and `--healthcheck`, which has no variable. The full table is in [docs/configuration.md](../../docs/configuration.md#bff-flags-and-environment-variables). The three you need most often:

| Env var | Flag | Default | Description |
| --------- | ------ | --------- | ------------- |
| `OPENSHELL_GATEWAY_URL` | `-gateway-url` | `localhost:50051` | Gateway gRPC endpoint. `grpcs://` or `https://` for TLS; `grpc://`, `http://` or no scheme for plaintext |
| `AUTH_DISABLED` | `-auth-disabled` | `false` | Local development only: every request is a synthetic `dev-user`, and no token is forwarded to the gateway |
| `GATEWAY_RELEASE_LINE` | `-gateway-release-line` | compiled in | Gateway release line this build is for, written `major.minor` (`0.1`). A gateway whose version starts with those two numbers is `supported` (any patch, pre-release, dev build or rebuild), any other line is `unsupported`, and a gateway with no usable version is `unknown` (ADR 0005, decision 9). The default is `models.BuiltInGatewayReleaseLine`, the line of the release `deploy/ci/gateway-pins.json` names; `node scripts/gateway-range.mjs --check` fails in CI when the two disagree. Override it only for tests and local development: no image or chart sets it, and a value that is not a line turns the status into `unknown`. The verdict only informs; the BFF never blocks on it |

`GET /gateway/compatibility` serves the verdict to every signed-in user, with the version read from the gateway's unauthenticated health check. It also rides on `GET /gateway`, which the gateway answers for platform admins only.

## Error handling

Standard error envelope:

```go
type ErrorResponse struct {
    Code    ResponseCode `json:"code"`
    Message string       `json:"message"`
}
```

`ResponseCode` is a string enum in `pkg/apiutils/respond.go`. Every code a
handler returns is declared there. One code is not: the auth middleware
answers a request without a bearer with `unauthorized` (`pkg/auth/proxy.go`).

## Testing

- Table-driven tests with `*_test.go` adjacent to implementation
- `httptest.NewRecorder()` + `http.NewRequest()` for handler tests
- `mock_sdk_test.go` provides `openshell.ClientInterface` test doubles for handler coverage
- `rawexec_test.go`, `rawprovider_test.go` (with `rawprovider_allworkspaces_test.go`) and `rawprofile_test.go` cover the three direct gRPC calls against a fake gateway that enforces the real one's 1 MiB message limit
- `slog` for structured logging

## SDK updates

The SDK is pinned to the commit of an OpenShell **release tag** and moves
together with the gateway the branch pins, in one commit (ADR 0005, decision 7;
hard facts 17 and 20 in `openshell-api.md`). The Follow upstream workflow makes
that commit, on a `bump/openshell-X.Y.Z` branch with a pull request into
`main`: never make it by hand, and never `go get ...@latest` or a branch. The
bot creates the branch and never touches it again. A person does the work the
move makes necessary, pushed to that bump branch: the call sites and the test
doubles in `mock_sdk_test.go` when `check-backend` is red on the bump pull
request, and the dashboard itself when `compat` is red there or the issue
labelled `compat-migrate` is open. How the workflow works is in
[deploy/ci/upstream/README.md](../../deploy/ci/upstream/README.md).

To inspect an RPC or a type, read the SDK package (`openshell/v1`, `types/*`)
in the module cache or use `go doc`. `pkg/models` imports the SDK's proto
packages where the frontend's contract is protojson: policies
(`policyproto.go`) and provider profiles (`provider_profile.go`). Do not add
`backend/proto/`, `backend/gen/` or a `backend/internal/` wrapper layer.
