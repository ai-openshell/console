# The mock API

For UI work without a gateway. A **Vite dev-server plugin** intercepts all `/api/v1/*` requests and returns realistic mock data, allowing the full dashboard UI to run locally without a real OpenShell gateway, BFF, or Keycloak.

## Files

| File | Purpose |
|---|---|
| `frontend/mock/data.ts` | All mock data — workspaces, providers, sandboxes, profiles, settings, policies, drafts, logs, services, templates, members, gateway info, auth config |
| `frontend/mock/mockPlugin.ts` | Vite plugin with a compiled route matcher that intercepts GET requests with pattern-matched routes (`:param` syntax), and stubs POST/PUT/DELETE generically |
| `frontend/vite.config.ts` | Imports `mockApiPlugin` and conditionally adds it to the Vite plugin array when `MOCK_API=true` |

## How to run

```bash
cd frontend && MOCK_API=true npm start
```

Then open **http://localhost:3000** and click "Continue as developer."

No BFF or gateway needed. The mock plugin runs as Vite middleware *before* the existing `/api` proxy, so when `MOCK_API` is unset, the proxy to the real BFF on `:8080` still works exactly as before.

Both are also `make` targets: `make dev-mock`, and `make dev-mock-empty` for the mode below.

### Empty-gateway mode

```bash
cd frontend && MOCK_API=true MOCK_EMPTY=true npm start
```

Every list answers empty and only the `default` workspace a gateway starts with is returned. That is the one state the full fixtures cannot reach, and it is what the Gateway page's get-started walkthrough — which stands in for the count tiles — is shown for.

## Mock data coverage

- **Auth**: dev-mode config + platform admin user with `['admin', 'user']` roles
- **Gateway**: healthy v0.1.2, 2 compute drivers (Podman + K8s), 3 extensions (including NVIDIA NIM interceptor), compatibility status "supported"
- **Workspaces** (4): `default`, `ml-training`, `dev-sandbox`, `staging` — each with members
- **Providers** (3–4 per workspace): OpenAI, Anthropic, GitHub, NVIDIA NIM — with credential expiry and refresh status
- **Provider Profiles** (5 platform-scope): OpenAI, Anthropic, GitHub, NVIDIA NIM, Slack — with full credential schemas and network endpoints
- **Sandboxes** (11 total): varied phases — READY, PROVISIONING, STOPPED, STOPPING, SUSPENDED, ERROR, COMPLETED — with attached providers, endpoint statuses, and conditions
- **Sandbox Logs**: 10 lines per sandbox with gateway + sandbox sources, levels (INFO/DEBUG/WARN), and structured decision fields
- **Sandbox Policy**: per-sandbox policy views with loaded revisions and network policy rules
- **Effective Policy**: sandbox-scoped with OpenAI endpoint rule
- **Draft Policy**: 2 pending proposals (PyPI access + Hugging Face Hub) with rationale, confidence scores, and hit counts
- **Draft Summary**: cross-workspace notification badges (`code-assistant`: 2 pending, `training-run-42`: 1 pending with security flag)
- **Global Policy**: empty (no global override)
- **Gateway Settings**: 5 entries (default image, max sandboxes, idle timeout, driver config flag, policy validation mode)
- **Sandbox Settings**: 3 entries mixing SANDBOX and GLOBAL scopes
- **Templates**: `python-dev`, `node-dev`, `pytorch-gpu`, `jupyter-notebook` across workspaces
- **Services**: 4 exposed endpoints with generated URLs

## Architecture notes

- The mock plugin only activates for `GET` requests via the compiled router; `POST`/`PUT`/`DELETE` return generic success stubs so UI actions (create, delete, etc.) don't error out, but they don't mutate the in-memory data.
- Timestamps use `Date.now()` offsets so data always looks fresh.
- The plugin has zero effect when `MOCK_API` is not set to `true`.
