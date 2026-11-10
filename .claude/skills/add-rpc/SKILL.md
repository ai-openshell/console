---
name: add-rpc
description: Add a new OpenShell API capability to the dashboard using the OpenShell Go SDK. Updates the handler, models, frontend API hook, and types. Use when adding a new endpoint to the dashboard.
---

# Add API Capability

Wire a new OpenShell gateway capability through the full stack: SDK surface check
→ models/request shape → REST handler → route → frontend API function → hook →
types.

## Arguments

`$ARGUMENTS` — SDK method or gateway capability name. Example:
`CreateWorkspace`, `ListProviders`, `GetSandboxTemplate`

## Steps

### 1. Find the capability in the SDK

Read the SDK surface described in `.claude/rules/openshell-api.md`.
Start with `openshell/v1/` and `types/` in the pinned module version. Note:
- Which SDK sub-client owns the capability (`Sandboxes()`, `SandboxTemplates()`, `Workspaces()`, `Providers()`, `Exec()`, `Config()`, `Policy()`, `Services()`, `Health()`)
- Workspace scoping
- Whether the API addresses a sandbox by name or UUID
- Secret-bearing fields or write-only fields that must not be sent back to the frontend
- Whether the SDK is actually missing what you need; if so, name the gap before adding a direct gRPC call (ADR 0003, decision 4)

If the capability exists upstream but not in the **pinned** SDK, do not run
`go get ...@latest` to reach it. The SDK moves with the gateway, to the commit
of an upstream release tag, and the Follow upstream workflow makes that move
on a `bump/openshell-X.Y.Z` branch (ADR 0005, decision 7 and the amendment of
2026-11-06; see "SDK updates" in `.claude/rules/bff-go.md`). If the capability
is in the upcoming release, the endpoint is work for that release's bump
branch, or, before upstream ships it, for a prep branch made by running Follow
upstream with the pre-release as `version`. Do not start that run yourself:
say what is needed and stop.
Also check that the gateway this branch pins (`node scripts/gateway-range.mjs`
prints its release) actually serves the RPC: a gateway answers gRPC
`UNIMPLEMENTED` for an RPC it does not have.

### 2. Update models / request parsing

If the gateway response needs JSON shaping, add or extend DTO converters in
`backend/pkg/models/`. Never serialize SDK objects directly.

```go
func FromSDKWorkspace(ws *openshell.Workspace) Workspace { ... }
```

For request bodies:
- Simple request structs live in the handler file.
- Complex SDK-building logic lives in `models/sdk_converters.go` or
  `models/builders.go`.
- Policy payloads must keep using `ParseSDKPolicy` / `marshalSDKPolicy` so the
  frontend's protojson contract stays intact.

### 3. Service interface

Handlers never hold the SDK client directly. They depend on a narrow
interface in `backend/pkg/services/`. For most resources the interface just
embeds the SDK's sub-client:

```go
// backend/pkg/services/workspace.go
type WorkspaceServiceInterface interface {
    openshell.WorkspaceInterface
}
```

Add a method to the interface only when the BFF needs behavior the SDK
sub-client does not expose (see `TemplateService.CreateSandboxFromTemplate`,
which reaches a top-level client method).

### 4. REST handler

Add the method to the resource's handler struct in `backend/pkg/handlers/`.
Handlers call `h.svc` and the exported helpers from `pkg/apiutils`:

```go
// backend/pkg/handlers/workspaces_handler.go
func (h *WorkspacesHandler) CreateWorkspace(w http.ResponseWriter, r *http.Request) {
    var body CreateWorkspaceRequest
    if !apiutils.DecodeBody(w, r, &body) {
        return
    }
    if !apiutils.ValidDNS1123(body.Name) {
        apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidName, "workspace name must be a valid DNS-1123 label")
        return
    }
    workspace, err := h.svc.Create(r.Context(), body.Name, body.Labels)
    if err != nil {
        apiutils.WriteSDKError(w, err)
        return
    }
    apiutils.WriteJSON(w, http.StatusCreated, models.FromSDKWorkspace(workspace))
}
```

Key patterns:
- No `Handler` suffix on method names (the struct carries it, not the method)
- URL params come from `r.PathValue("workspace")` — chi sets them on every
  matched route, so do not import chi in a handler
- `apiutils.DecodeBody(w, r, &dst)` for request parsing (returns false on error,
  writes the response itself)
- `apiutils.WriteSDKError(w, err)` for gateway/SDK errors
- `apiutils.WriteJSON(w, statusCode, models.FromSDK*(...))` when returning SDK resources
- `apiutils.ValidDNS1123(name)` for resource name validation
- Error codes are `apiutils.ResponseCode` constants, never bare strings — add a
  new constant to `pkg/apiutils/respond.go` rather than inlining a literal

Register the route in `Routes()` in `backend/pkg/server/app.go`. Routes are
nested: the workspace routes sit inside `r.Route("/workspaces", ...)`, and a
route that belongs to one workspace goes inside `r.Route("/{workspace}", ...)`:

```go
r.Route("/workspaces", func(r chi.Router) {
    r.Get("/", app.workspaces.ListWorkspaces)
    r.Post("/", app.workspaces.CreateWorkspace)
    r.Route("/{workspace}", func(r chi.Router) {
        r.Get("/", app.workspaces.GetWorkspace)
        // ...
    })
})
```

If the resource has no handler struct yet, add one plus its constructor, and
wire it in `NewApp`.

### 5. Frontend types

Every type is in `frontend/src/types/index.ts`; there are no per-resource type
files. Add the response type and the request body there, in the shape the BFF
sends and takes:

```typescript
export type Workspace = {
  metadata: ObjectMeta;
  phase: WorkspacePhase;
};

export type CreateWorkspaceRequest = {
  name: string;
  labels?: Record<string, string>;
};
```

### 6. Frontend API function

Add to the appropriate file in `frontend/src/api/`. Use `get`, `post`, `put`, `del` from `./client`:

```typescript
import { post } from './client';
import type { CreateWorkspaceRequest, Workspace } from '../types';

export const createWorkspace = (
  body: CreateWorkspaceRequest,
): Promise<Workspace> => post<Workspace>('/api/v1/workspaces', body);
```

### 7. Frontend hook

Add query/mutation hooks using the centralized `queryKeys` from `./queryKeys`:

```typescript
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { workspaceKeys } from './queryKeys';

export const useCreateWorkspace = () => {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: createWorkspace,
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: workspaceKeys.all }),
  });
};
```

Check `queryKeys.ts` for the factories a resource has. Use `list(...)` for a
list query and invalidate with `all`. Lists and status are polled, not pushed
(ADR 0003, decision 6): give such a query a `refetchInterval` from
`frontend/src/constants.ts`:

```typescript
export const useWorkspaces = (labelSelector?: string) =>
  useQuery({
    queryKey: workspaceKeys.list(labelSelector),
    queryFn: () => listWorkspaces(labelSelector),
    refetchInterval: RESOURCE_POLL_MS,
  });
```

### 8. Update test doubles

Add the needed behavior to `backend/pkg/handlers/mock_sdk_test.go`. Extend the
relevant mock SDK sub-client instead of inventing a parallel interface layer.

### 9. Verify

```bash
(cd backend && go build ./... && go test ./...)
(cd frontend && npm run typecheck)
```
