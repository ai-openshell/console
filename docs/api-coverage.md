# API coverage

How much of the OpenShell gateway's user-facing API the dashboard offers, at
the release this branch pins (OpenShell 0.1.9), and how the project is told
when upstream adds something.

The goal is that a person can do in the dashboard close to everything the
gateway's API lets a signed-in person do. This page measures the distance.

- **What was measured.** Every RPC of `openshell.proto` that takes a user's
  bearer (upstream marks each RPC with who may call it; sandbox-only, peer and
  operator RPCs are left out), and every field of every message a client can
  send. For each: does the Go SDK carry it, does the BFF have a route for it,
  and how does the UI offer it.
- **How.** The list comes from upstream's protos at tag `v0.1.9`, read by
  `deploy/ci/upstream/protoapi.py`. The answers were read from the code, with
  the place recorded for each one. They live in
  [`deploy/ci/upstream/api-coverage.json`](../deploy/ci/upstream/api-coverage.json)
  (the ledger); the counts and the tables on this page are generated from it.
- **What "run" and "read" mean below.** *Measured* is a request sent to a
  gateway. *Read* is the dashboard's code and upstream's source at the tag,
  nothing sent. Most of this page is read. Audited 2026-11-20.

## Summary

<!-- api-coverage:summary:begin (generated from deploy/ci/upstream/api-coverage.json; do not edit) -->

At OpenShell 0.1.9 the gateway has **69** RPCs a signed-in person may call. The dashboard offers **39**
of them as something a person can do, shows 18 more read-only, and has nothing for **12**.

The messages a client can send have **505** fields. 152 are plumbing the dashboard fills in
(the workspace of the route, page tokens, concurrency tokens). Of the other **353**:

| A person sets it | Fields | Share |
|---|---|---|
| with an input in a form | 122 | 35% |
| by pressing a button | 20 | 6% |
| only by writing YAML or JSON, or choosing a file | 106 | 30% |
| never: the dashboard always sends one value | 14 | 4% |
| never: it is shown only | 11 | 3% |
| never: **the dashboard cannot send it** | 80 | 23% |

<!-- api-coverage:summary:end -->

The RPC count flatters the dashboard: almost every resource has its list,
create and delete. The fields show the real distance. Of what a person could
author, a third has an input, a third can be reached only by someone who can
write the gateway's YAML or JSON by hand, and a quarter cannot be sent at all.

## The gaps, by how much they block a real task

1. **The Create provider profile form cannot make a usable profile.** The
   gateway refuses what the form builds whenever the profile has both a
   credential and an endpoint, which is the reason to make a profile. Measured;
   the detail is in [the next section](#the-provider-profile-form). Importing
   a YAML or JSON file works, so the task is possible only for someone who can
   write upstream's profile file by hand.
2. **A sandbox's life after create is stop, start and delete.**
   - `SuspendSandbox` and `ResumeSandbox` (new in 0.1.6) have no route and no
     button. The SDK has both.
   - The `SUSPENDING` and `SUSPENDED` phases are not in the frontend's phase
     type (`frontend/src/types/index.ts:54`), so a sandbox suspended from the
     CLI shows a grey unknown label with Stop disabled, and only Delete works.
   - `UpdateSandbox` (new in 0.1.9: change the image, command, environment,
     labels or annotations of a stopped sandbox) has no route. A wrong image
     means delete and create again.
   - Stop is offered for `READY` only and Start for `STOPPED` and `COMPLETED`
     only (`frontend/src/utils/sandboxLifecycle.ts:9`). The gateway takes both
     in more phases, so a sandbox in `ERROR` can only be deleted.
3. **Policy can be added to in a form, and changed only as a document.** The
   rule view adds an endpoint, appends a method-and-path rule and removes
   things. Nothing edits an existing endpoint or rule, and `remove_binary` is
   never issued. MCP, GraphQL and JSON-RPC endpoints, `credential_binding`,
   AWS signing, TLS mode, more than one port, every L7 matcher except method
   and path, and all of `network_middlewares` are document only. The global
   policy is a document and nothing else. Two inputs are wrong today:
   - The add-endpoint form offers "rewrite credentials in WebSocket messages"
     on a REST endpoint (`frontend/src/components/PolicyRuleEditor/utils.ts:67`).
     Gateways 0.1.8 and newer refuse it there. Read.
   - The YAML policy reader knows three MCP revisions
     (`frontend/src/utils/policyFile.ts:108`) and refuses `2026-07-28`, which
     the gateway accepts. The JSON form passes it. Read.
4. **A service is always exposed in one authorization mode, and re-exposing
   resets it.** `ExposeServiceRequest.authorization_mode` is always `STRIP`:
   the BFF's body has no field (`backend/pkg/handlers/services_handler.go:18`).
   `BEARER_PASSTHROUGH` cannot be chosen, the mode is not shown, and because
   expose is create-or-update, exposing an existing name again from the
   dashboard turns a passthrough endpoint back to strip without saying so.
   The same holds for services exposed at sandbox create. Read.
5. **Providers after create.** An expiry, once set, cannot be removed
   (`clear_credential_expiration_keys` has no path through the BFF). A stored
   credential cannot be removed. Labels cannot be set. Nothing shows whether a
   provider change has reached a sandbox (`GetSandboxProviderStatus`: no SDK
   wrapper, no route). Attach, detach and update send no resource version,
   though the BFF and the gateway both take one, so two people editing at once
   overwrite each other silently.
6. **Sandbox create.** The restart policy is always `NEVER`
   (`backend/pkg/models/sdk_converters.go:207`). A template's `user_namespaces`,
   labels, annotations, environment, `agent_socket` and resource requests have
   no input. The template form's environment field splits on commas, so
   `NO_PROXY=a,b` cannot be entered. A GPU can be asked for only with a count.
7. **Logs.** The Minimum level filter runs in the browser against a list that
   has no `OCSF` level (`frontend/src/components/sandbox/SandboxLogsTab.tsx:45`),
   so choosing any level hides every OCSF line, the lines that say what the
   policy denied. While the live stream is up, level, source, line count and
   since never reach the gateway: they filter the last 2000 lines already
   fetched. Platform events, which say why a sandbox is not ready, are never
   requested. Read.
8. **A command can be run only in the terminal.** There is no one-shot command
   without a TTY and no timeout (`execution_timeout`); the CLI has
   `sandbox exec --no-tty --timeout`.
9. **Left to the CLI on purpose.** SSH sessions, TCP forwarding and rootfs
   staging need something on the person's own machine (a local listener, a
   path on the gateway's host). The Connect card prints the commands instead.
   The ledger records them as not offered; they are a boundary, not a defect.

Smaller things the audit turned up, none blocking:

- **No request ever carries a `request_id`.** No SDK wrapper sets one, so the
  gateway cannot recognise a retried mutation.
- **There are four direct gRPC calls, not three.**
  `backend/pkg/clients/rawlogs.go` calls `WatchSandbox` on the generated
  client and is listed nowhere. SDK 0.1.9 has `SandboxLogsWatch().WatchLogs`,
  which closes the gap it was added for, so by ADR 0003, decision 4, it is due
  for deletion.
- **Two rules are stale.** Hard fact 1 in `.claude/rules/openshell-api.md`
  says there is no suspend and no Suspended state. The Streaming RPCs
  paragraph of the same file says `WatchSandbox` is deferred in favour of
  polling.
- **Provider labels.** The SDK's `Providers().List` drops `label_selector`,
  which the gateway honours; two comments in the dashboard say the gateway has
  no such filter.

## The provider profile form

`frontend/src/components/CreateProfileModal.tsx` is the only form that makes a
profile. `ProfileFileModal` imports and updates profiles from YAML or JSON
files, through `frontend/src/utils/profileFile.ts`, which reads every field of
upstream's profile file at 0.1.9. There is no edit form: to change a profile a
person exports it, edits the file and chooses it again.

PR 139 (`b15816c`) fixed the form's layout. It added no field.

What the form can set, out of the profile's schema:

| Message | The form sets | The form cannot set (file only) |
|---|---|---|
| `ProviderProfile` | `id`, `display_name`, `description`, `category`, `inference_capable` | `binaries`, `discovery`, `files`, `annotations` |
| `ProviderProfileCredential` | `name`, `env_vars`, `required` | `description`, `auth_style`, `header_name`, `query_param`, `path_template`, `stable_placeholder`, `refresh` (strategy, token URL, scopes, material, additional outputs, refresh-before, max lifetime), `token_grant` (grant type, token endpoint, audience and overrides, scopes, subject token, cache TTL and the rest) |
| `NetworkEndpoint` (each of `endpoints`) | `host`, `port` | `protocol`, `access`, `rules`, `deny_rules`, `tls`, `enforcement`, `allow_uninspected_credentials`, `ports`, `path`, `allowed_ips`, `allow_encoded_slash`, `websocket_credential_rewrite`, `request_body_credential_rewrite`, `persisted_queries`, `graphql_persisted_queries`, `graphql_max_body_bytes`, `json_rpc_max_body_bytes`, `mcp`, `credential_signing`, `signing_service`, `signing_region` |

The BFF is not the limit: it takes all of it (`networkEndpoints` as protojson,
`backend/pkg/models/provider_profile.go:181`).

Which of the missing fields make a profile unusable:

| Missing | What happens | How it is known |
|---|---|---|
| An endpoint's `protocol` with `access` or `rules`, or else `allow_uninspected_credentials` | A profile with a credential and an endpoint is refused: `credentialed endpoint 'api.example.com:443' uses L4-only; configure L7 inspection or explicitly set allow_uninspected_credentials: true`. The modal shows the diagnostic and has no input that answers it. The rule has been in the gateway since 0.1.0. | Measured: the form's body sent to `POST .../provider-profiles/lint` on gateway 0.1.6, 2026-11-20. Read at 0.1.9: `crates/openshell-providers/src/profiles.rs`, same rule |
| A way to require the port | The form lets the port stay empty and sends none; the gateway answers `invalid endpoint 'api.example.com:0'`. | Measured, as above |
| `access` or `rules` once a protocol is named | The BFF's short endpoint form accepts `protocol`, but with it alone the gateway answers `protocol requires rules or access to define allowed traffic`. So adding a protocol select to the form would not be enough. | Measured, as above |
| `auth_style` and `header_name` (or `query_param`, `path_template`) | Accepted. At run time the proxy then puts the secret in the `Authorization` header only. A service that reads `x-api-key`, a query parameter or a path segment never receives it. | Read: `docs/how-it-works/providers/profiles.mdx` at v0.1.9 |
| Any endpoint (a credential-only profile, the one shape the form makes that the gateway accepts and that holds a secret) | Accepted, and "endpointless": the gateway withholds its static credentials from a sandbox until a policy endpoint names the provider in `credential_binding`, which the policy editors offer in the document only. | Read: same document |
| `binaries` | Accepted. Upstream's documentation says a rule with an empty `binaries` list "matches no binary and allows nothing" (`docs/how-it-works/policies/network-rules.mdx` at v0.1.9), which is what the UX sweep measured on 0.1.6 for a policy rule. Not measured for a provider's own rule at 0.1.9. | Read; not measured |

So the three shapes the form can produce are: credential and endpoint
(refused), endpoint only (accepted, carries no secret), credential only
(accepted, and the secret is withheld until a policy binds it by hand).

## Why the tests missed it

One compat test sends the gateway something the UI built. `TestPolicyPresets`
(`backend/test/compat/sandbox_create_defaults_test.go:176`) loads
`backend/test/compat/testdata/policy_presets.json`, and
`frontend/src/components/__tests__/policyTemplates.spec.ts` fails when that
file differs from the presets the create form offers. Every other body in the
compat suite is a Go literal written by hand to be accepted. A literal has the
same keys as the form's body on the day it is written and does not follow the
form afterwards. The suite's own rule is "send what the UI sends"
(`backend/test/compat/harness_test.go:193`); nothing enforces it.

The frontend's tests cannot catch a refusal either, at any of three layers:
Jest specs mock the API module, Cypress answers every import with
`imported: true` (`frontend/cypress/support/intercepts.ts:206`), and the Vite
mock answers every write with success.

For the profile form specifically:

- `CreateProfileModal.spec.tsx` never adds an endpoint.
- The two profile bodies in the compat suite are the two shapes the form
  cannot make: a credential with no endpoint (`provider_test.go:72`) and
  upstream's whole github profile through `networkEndpoints`
  (`profile_test.go:108`). Comments beside both say that a profile with a
  credential and an endpoint "has to say more". The form's shape sits between
  them, untested.
- The Cypress test types exactly the refused combination (credential
  `api_key`, endpoint `api.example.com:443`) and passes, because the stub
  accepts it (`frontend/cypress/e2e/provider-profiles.cy.ts:65`).

Flows with no test that sends what the form builds to a real gateway:

| Flow | What a real gateway sees today |
|---|---|
| Provider profile, form | Nothing of the form's shape |
| Provider profile, file import and update | One hand-written github body; the reader's output for the 17 upstream files is never sent |
| Provider refresh configure | `strategy: "static"`, for its refusal. The form's four strategies, material and expiry never cross the wire |
| Proposal approve, reject, edit, approve all | Refusals against an empty inbox; no real chunk and no review token |
| Policy rule editor | `addRule`, `addAllowRules`, `removeEndpoint`, `removeRule` by hand. Never `addDenyRules`, `allowedIps`, the credential flags, or any protocol but REST |
| Policy as YAML (create, replace, global) | Nothing converted from YAML by `policyFile.ts` |
| Sandbox create with providers or a GPU | Not sent |
| Everything else (workspace, member, sandbox options, template, provider create and edit, settings, service, files, terminal) | A hand-written body of the same shape |

The probes that would have caught the profile bug, and would catch the next
one. Each follows the preset pattern: a fixture under
`backend/test/compat/testdata/`, a frontend spec that builds the bodies from
the real component and fails when they differ from the fixture, and a compat
test that sends the fixture through the BFF in a non-default workspace. A case
the UI is meant to block is recorded as `"expect": "refused"`, so that a
refusal is a tested state and not a surprise.

| Probe | The frontend spec pins | The compat test sends and asserts |
|---|---|---|
| `profile_form.json` → `TestProfileFormBodies` | The modal's body for: required fields only; one credential; a credential with several env vars; endpoint only; **credential and endpoint**; every category | Lint, then import: `valid`, `imported`, no error diagnostic. Then create a provider of the profile and attach it to a sandbox. This is the probe that fails today |
| `profile_files.json` → `TestProfileFileBodies` | `parseProfileFile` output for each upstream example profile | Lint all in one request as the modal does, import, read back, nothing dropped |
| `profile_update.json` → `TestProfileExportedFileUpdate` | Export, edit, parse again, with the resource version | Update: `updated`, nothing dropped |
| `refresh_configure.json` → `TestRefreshConfigureBodies` | The form's body for each of its four strategies | Not refused for its shape (unknown strategy, unknown field) |
| `policy_merge_ops.json` → `TestRuleEditorOperations` | `addEndpointOperation` for each protocol, with `allowedIps` and each credential flag; allow and deny rules | Merge, read back, compare. Catches the WebSocket-on-REST refusal |
| `policy_documents.json` → `TestPolicyDocuments` | `parsePolicyFile` output for the upstream example policies | Accepted and loaded. Catches the MCP revision |
| `sandbox_create.json`, `template_create.json` | The create bodies with every optional field set | Every field read back: a dropped field fails nothing otherwise (hard fact 18) |

Two smaller holes in CI: the preset fixture is compared, not generated, and it
lives under `backend/`, which the frontend job's path filter does not watch,
so a pull request that edits only the fixture skips the spec that guards it.

## Being told when upstream adds something

Nobody here reads upstream's commits, and until now nothing did it for them:
suspend and resume arrived in 0.1.6 and were found by accident.

The Follow upstream workflow now reads the API. When it finds a release ahead
of the pinned one, its `plan` step fetches the `proto` directory of both
tags, compares what a signed-in person can reach, and puts the result in
three places: the bump pull request's description, the run summary, and check
A's issue. Each new RPC, field and enum value is looked up in the ledger; what
the ledger does not list is searched for in `frontend/src` and `backend/pkg`,
and the report says whether the name occurs. Removed and changed things are
listed apart, because those can break what works.

It needs no workflow change: `plan` already asks upstream for its tags.
A failure to read or parse the protos does not stop a bump; the report then
says the comparison is missing and how to run it by hand.

```sh
python3 deploy/ci/upstream/follow.py api-diff --from 0.1.5 --to 0.1.6    # fetches both tags
python3 deploy/ci/upstream/follow.py api-diff --from 0.1.5 --to 0.1.6 --repo ~/Documents/OpenShell   # or reads a clone
```

What it says for 0.1.5 → 0.1.6, the release that was missed. The Dashboard
column is today's: suspend and resume are still not offered, and the two
phases are still named nowhere in the source.

> OpenShell 0.1.5 → 0.1.6: 2 new RPCs, 2 new fields in what it returns, 2 new enum values, 2 new messages.
>
> | RPC | Who may call it | What it does | Dashboard |
> |---|---|---|---|
> | `ResumeSandbox` | workspace user (`sandbox:write`) | Restore a suspended sandbox in a new runtime generation. | **not offered** |
> | `SuspendSandbox` | workspace user (`sandbox:write`) | Freeze a ready sandbox and retain its memory state for a later resume. | **not offered** |
>
> | Enum | Value | What it is | Used in | Dashboard |
> |---|---|---|---|---|
> | `SandboxPhase` | `SANDBOX_PHASE_SUSPENDING` | The gateway requested suspension and the driver has not confirmed it. | `SandboxStatus.phase` | not named anywhere in `frontend/src` or `backend/pkg` |
> | `SandboxPhase` | `SANDBOX_PHASE_SUSPENDED` | The workload is frozen and its memory state is retained by the isolation backend. | `SandboxStatus.phase` | not named anywhere in `frontend/src` or `backend/pkg` |

For 0.1.6 → 0.1.9 it reports `UpdateSandbox`, `GetServiceRouteSnapshot` and
`WatchServiceRoutes`. For the pre-release now upstream (0.1.10-pre.3) it
reports six new RPCs, the workspace secrets (`CreateSecret`, `GetSecret`,
`ListSecrets`, `UpdateSecret`, `DeleteSecret`,
`InlineProviderRefreshSecrets`), and seven new fields.

What it does not do:

- **It reads protos, not behaviour.** A rule the gateway starts enforcing
  without a proto change (the WebSocket-on-REST refusal in 0.1.8) is
  invisible to it. The probes above are what catch those.
- **It does not fail a pull request.** A new release leaves the ledger behind,
  and `follow.py api-coverage --upstream` then lists what is unclassified, but
  no CI job runs it. Whether an unclassified RPC should turn the bump pull
  request red is a decision for the maintainers.
- **The search is a hint.** A field named in a type is not a field a form
  offers. Only a ledger entry, written by someone who read the code, says a
  thing is covered.

## Keeping this page true

The ledger is edited by hand when a feature lands or a gap closes.

```sh
python3 deploy/ci/upstream/follow.py api-coverage --write       # rewrite the generated parts of this page
python3 deploy/ci/upstream/follow.py api-coverage               # check the ledger's shape and that this page matches it
python3 deploy/ci/upstream/follow.py api-coverage --upstream    # also: is anything of the pinned release unclassified?
```

The unit tests in `deploy/ci/upstream/tests/test_apicoverage.py` run the
second of these on every pull request, with no network. After a bump, the
third names the RPCs and fields the new release added that nobody has
classified yet.

How to read the tables: **SDK** and **BFF** say whether each carries the RPC
or field. **UI** is one of: *form field* (a dedicated input), *action* (a
button or a flow), *document only* (YAML or JSON in an editor, or a file),
*read-only* (shown), *fixed value* (the dashboard always sends the same
thing), *plumbing* (the dashboard fills it in: the workspace of the route,
page tokens, concurrency and idempotency tokens) and **not offered**.

# The matrix

<!-- api-coverage:matrix:begin (generated from deploy/ci/upstream/api-coverage.json; do not edit) -->

## Workspaces, members and the gateway

| RPC | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `CreateWorkspace` | Workspaces().Create | POST /api/v1/workspaces | form field | `frontend/src/components/CreateWorkspaceModal.tsx:63` |  |
| `GetWorkspace` | Workspaces().Get | GET /api/v1/workspaces/{workspace} | read-only | `frontend/src/pages/WorkspaceDetailPage.tsx:67` |  |
| `ListWorkspaces` | Workspaces().List (paged) and Workspaces().ListAll | GET /api/v1/workspaces | read-only | `frontend/src/pages/WorkspaceListPage.tsx:108` |  |
| `DeleteWorkspace` | Workspaces().Delete | DELETE /api/v1/workspaces/{workspace} | action | `frontend/src/pages/WorkspaceListPage.tsx:136` |  |
| `AddWorkspaceMember` | Workspaces().AddMember | POST /api/v1/workspaces/{workspace}/members | form field | `frontend/src/components/AddMemberModal.tsx:59` |  |
| `RemoveWorkspaceMember` | Workspaces().RemoveMember | DELETE /api/v1/workspaces/{workspace}/members/{subject} | action | `frontend/src/pages/MemberListPage.tsx:195` |  |
| `ListWorkspaceMembers` | Workspaces().ListAllMembers | GET /api/v1/workspaces/{workspace}/members | read-only | `frontend/src/pages/MemberListPage.tsx:48` |  |
| `Health` | Health().Check | GET /api/v1/gateway/compatibility; GET /api/v1/readyz (public probe) | read-only | `frontend/src/components/GatewayStatusIndicator.tsx:95` | Shown as the masthead's gateway status and the release-line verdict. |
| `GetCurrentUser` | Health().GetCurrentUser | GET /api/v1/auth/whoami | read-only | `frontend/src/components/CurrentUserModal.tsx:64` | With AUTH_DISABLED the BFF answers a synthetic dev-user and does not call the gateway. |
| `GetGatewayInfo` | Health().GetGatewayInfo | GET /api/v1/gateway | read-only | `frontend/src/pages/GatewayOverviewPage.tsx:318` |  |

### `AddWorkspaceMemberRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `principal_subject` | yes | yes | form field | `frontend/src/components/AddMemberModal.tsx:90` | Free text, trimmed. |
| `request_id` | no | no | plumbing | `backend/pkg/handlers/workspaces_handler.go:131` | Idempotency key. Never sent: no SDK wrapper sets it, so the gateway cannot recognise a retried request. |
| `role` | yes | yes | form field | `frontend/src/components/AddMemberModal.tsx:109` | Select offers both settable enum values, USER and ADMIN; ADMIN only to platform admins, which is the gateway's own rule. |
| `workspace_scope` | yes | yes | plumbing | `frontend/src/components/AddMemberModal.tsx:38` | Named workspace from the route's {workspace}. |

### `CreateWorkspaceRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `labels` | yes | yes | form field | `frontend/src/components/CreateWorkspaceModal.tsx:110` | Key/value editor; create only, the gateway has no workspace update. |
| `name` | yes | yes | form field | `frontend/src/components/CreateWorkspaceModal.tsx:87` |  |
| `request_id` | no | no | plumbing | `backend/pkg/handlers/workspaces_handler.go:72` | Idempotency key. Never sent: no SDK wrapper sets it, so the gateway cannot recognise a retried request. |

### `DeleteWorkspaceRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `allow_missing` | yes | fixed | fixed value | `backend/pkg/handlers/workspaces_handler.go:90` | Always false: the BFF passes no DeleteOptions. |
| `name` | yes | yes | action | `frontend/src/pages/WorkspaceListPage.tsx:136` | The row or selection being deleted. |
| `request_id` | no | no | plumbing | `backend/pkg/handlers/workspaces_handler.go:90` | Idempotency key. Never sent: no SDK wrapper sets it, so the gateway cannot recognise a retried request. |

### `GetWorkspaceRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `name` | yes | yes | plumbing | `frontend/src/pages/WorkspaceDetailPage.tsx:67` | From the route's {workspace}. |

### `ListWorkspaceMembersRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `page_size` | yes | fixed | plumbing | `backend/pkg/handlers/workspaces_handler.go:105` | Always unset (gateway default): the BFF reads every page with ListAllMembers. |
| `page_token` | yes | no | plumbing | `backend/pkg/handlers/workspaces_handler.go:105` | Filled in by the SDK pager inside ListAllMembers. |
| `workspace_scope` | yes | yes | plumbing | `frontend/src/pages/MemberListPage.tsx:48` | Named workspace from the route's {workspace}. |

### `ListWorkspacesRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `label_selector` | yes | yes | form field | `frontend/src/pages/WorkspaceListPage.tsx:312` | Search box taking the gateway's own key=value,key=value form. |
| `page_size` | yes | yes | plumbing | `frontend/src/pages/WorkspaceListPage.tsx:109` | The table's per-page choice, 1 to 100; lists without pageSize (navigation count) read every page. |
| `page_token` | yes | yes | plumbing | `frontend/src/pages/WorkspaceListPage.tsx:110` | The gateway's nextPageToken, passed back by the cursor pagination. |

### `ObjectMeta`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `annotations` | yes | yes | form field | `frontend/src/components/CreateTemplateModal.tsx:305` | Create forms with an annotations input: sandbox, sandbox from template, sandbox template; without one: provider (the BFF body has no annotations field), workspace (the RPC takes none), member and service endpoint. |
| `created_time` | yes | no | read-only | `frontend/src/pages/ProviderDetailPage.tsx:322` | Gateway-assigned; shown, never sent. |
| `deletion_time` | yes | no | **not offered** | `backend/pkg/models/sdk_converters.go:472` | Gateway-assigned; the BFF returns it as deletionTimestampMs and no page shows it. |
| `id` | yes | no | read-only | `frontend/src/pages/ProviderDetailPage.tsx:258` | Gateway-assigned; shown, never sent. |
| `labels` | yes | yes | form field | `frontend/src/components/CreateTemplateModal.tsx:278` | Create forms with a labels input: sandbox, sandbox from template, sandbox template, workspace; without one: provider (the BFF body accepts labels but the Add Provider form sends none), member and service endpoint; no label can be changed after create. |
| `name` | yes | yes | form field | `frontend/src/components/provider/ProviderFormModal.tsx:395` | Typed on the provider and sandbox template create forms, the two creates that send an ObjectMeta; afterwards taken from the route. |
| `resource_version` | yes | no | **not offered** | `backend/pkg/handlers/providers_handler.go:209` | Shown read-only on the provider and workspace pages but never sent back: UpdateProvider reads it as the expected version, and the BFF builds the update with a name only, so a provider edit is not checked against the version the form was opened on. |
| `workspace` | yes | no | read-only | `frontend/src/pages/ProviderDetailPage.tsx:302` | Gateway-assigned from the request's workspace_scope; shown, never sent. |

### `RemoveWorkspaceMemberRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `allow_missing` | yes | fixed | fixed value | `backend/pkg/handlers/workspaces_handler.go:146` | Always false: the BFF passes no DeleteOptions. |
| `principal_subject` | yes | yes | action | `frontend/src/pages/MemberListPage.tsx:163` | The row whose Remove action is clicked. |
| `request_id` | no | no | plumbing | `backend/pkg/handlers/workspaces_handler.go:146` | Idempotency key. Never sent: no SDK wrapper sets it, so the gateway cannot recognise a retried request. |
| `workspace_scope` | yes | yes | plumbing | `frontend/src/pages/MemberListPage.tsx:49` | Named workspace from the route's {workspace}. |

### `WorkspaceSelector`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `all_workspaces` | yes | yes | plumbing | `backend/pkg/handlers/allworkspaces_handler.go:57` | Sent by exactly the four lists that accept it, GET /api/v1/{sandboxes,providers,templates,services}, behind the All workspaces, Sandboxes and Gateway pages; the gateway answers platform admins only. |
| `workspace` | yes | yes | plumbing | `backend/pkg/server/app.go:200` | The route's {workspace} segment on every workspace-scoped call. |

## Sandboxes and sandbox templates

| RPC | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `CreateSandbox` | Sandboxes().Create; Client.CreateSandboxFromTemplate when a workload template is named | POST /api/v1/workspaces/{workspace}/sandboxes; POST /api/v1/workspaces/{workspace}/sandboxes/from-template | form field | `frontend/src/components/CreateSandboxModal.tsx:78` | Two forms: Create sandbox (inline workload) and Create sandbox from a template row (TemplatesTab.tsx:310). |
| `GetSandbox` | Sandboxes().Get | GET /api/v1/workspaces/{workspace}/sandboxes/{name} | action | `frontend/src/pages/SandboxDetailPage.tsx:74` | Polled by the sandbox detail page. |
| `ListSandboxes` | Sandboxes().List (paged) / ListAll | GET /api/v1/workspaces/{workspace}/sandboxes; GET /api/v1/sandboxes (all workspaces) | action | `frontend/src/pages/SandboxListPage.tsx:168` |  |
| `DeleteSandbox` | Sandboxes().Delete | DELETE /api/v1/workspaces/{workspace}/sandboxes/{name} | action | `frontend/src/pages/SandboxDetailPage.tsx:257` | Also row and bulk delete on the list page. |
| `StopSandbox` | Sandboxes().Stop | POST /api/v1/workspaces/{workspace}/sandboxes/{name}/stop | action | `frontend/src/pages/SandboxDetailPage.tsx:212` | Offered only in READY (frontend/src/utils/sandboxLifecycle.ts:9); gateway 0.1.9 also stops Completed, Error, Suspending and Suspended sandboxes, and tells the caller to stop an Error sandbox before starting it. |
| `StartSandbox` | Sandboxes().Start | POST /api/v1/workspaces/{workspace}/sandboxes/{name}/start | action | `frontend/src/pages/SandboxDetailPage.tsx:193` | Offered only for STOPPED and COMPLETED (frontend/src/utils/sandboxLifecycle.ts:12); gateway 0.1.9 also starts a Suspended sandbox (discarding its memory state) and one in Error after a failed main process. |
| `SuspendSandbox` | SandboxSuspendResume().Suspend | none | **not offered** | `backend/pkg/server/app.go:220` | No route, handler, API function or button; SUSPENDING/SUSPENDED are not in the frontend SandboxPhase type and render as an unstyled grey label with Stop disabled and no Start. |
| `ResumeSandbox` | SandboxSuspendResume().Resume | none | **not offered** | `backend/pkg/server/app.go:221` | No route or UI: a sandbox suspended with the CLI cannot be resumed from the dashboard. |
| `SandboxUpdates.UpdateSandbox` | generated client only | none | **not offered** | `backend/pkg/server/app.go:218` | Separate gRPC service (pb.NewSandboxUpdatesClient) with no SDK wrapper; nothing in the dashboard changes a sandbox's image, command, environment, labels or annotations after create. |
| `BeginRootfsTarStaging` | generated client only | none | **not offered** | `backend/pkg/server/app.go:216` | Used by the CLI's `sandbox create --from <rootfs tar>` for the VM driver on local gateways only; the client must then write the archive to a path on the gateway host, which a browser cannot do. |
| `CreateSandboxTemplate` | SandboxTemplates().Create | POST /api/v1/workspaces/{workspace}/templates | form field | `frontend/src/components/CreateTemplateModal.tsx:150` | Button shown to workspace admins only (TemplatesTab.tsx:212). |
| `GetSandboxTemplate` | SandboxTemplates().Get | GET /api/v1/workspaces/{workspace}/templates/{name} | **not offered** | `frontend/src/api/templates.ts:68` | Route and useTemplate hook exist but no component calls them; the Templates tab renders everything from the list. |
| `ListSandboxTemplates` | SandboxTemplates().ListAll | GET /api/v1/workspaces/{workspace}/templates; GET /api/v1/templates (all workspaces) | action | `frontend/src/components/TemplatesTab.tsx:89` |  |
| `DeleteSandboxTemplate` | SandboxTemplates().Delete | DELETE /api/v1/workspaces/{workspace}/templates/{name} | action | `frontend/src/components/TemplatesTab.tsx:166` | Row action shown to workspace admins only. |

### `BeginRootfsTarStagingRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `file_name` | n/a | no | **not offered** | `backend/pkg/server/app.go:216` | No BFF route for BeginRootfsTarStaging. |
| `size_bytes` | n/a | no | **not offered** | `backend/pkg/server/app.go:216` | No BFF route for BeginRootfsTarStaging. |
| `workspace_scope` | n/a | no | **not offered** | `backend/pkg/server/app.go:216` | No BFF route for BeginRootfsTarStaging. |

### `CreateSandboxRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `annotations` | yes | yes | form field | `frontend/src/components/CreateSandboxModal.tsx:368` | Key/value editor on both create forms. |
| `await_main_process_attachment` | no | no | **not offered** | `backend/pkg/models/builders.go:38` | SDK Create never sets it; it is the CLI's hint that it will attach to the main process, which the dashboard does not do at create. |
| `labels` | yes | yes | form field | `frontend/src/components/CreateSandboxModal.tsx:258` | One text input of comma-separated key=value pairs, on both create forms. |
| `name` | yes | yes | form field | `frontend/src/components/CreateSandboxModal.tsx:244` | Optional; empty lets the gateway generate one. |
| `request_id` | no | no | plumbing | `backend/pkg/handlers/sandboxes_handler.go:85` | Idempotency key. Never sent: no SDK wrapper sets it, so the gateway cannot recognise a retried request. |
| `service_exposures` | yes | yes | form field | `frontend/src/components/sandbox/SandboxLaunchFields.tsx:87` | Rows of name and port on both create forms. |
| `spec` | yes | yes | form field | `frontend/src/hooks/useCreateSandboxForm.ts:163` |  |
| `workload_template` | yes | yes | action | `frontend/src/components/TemplatesTab.tsx:310` | Chosen by the 'Create sandbox' action of a row on the workspace Templates tab; the Create sandbox form itself has no template picker. |
| `workspace_scope` | yes | yes | plumbing | `backend/pkg/handlers/sandboxes_handler.go:85` |  |

### `CreateSandboxTemplateRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `request_id` | no | no | plumbing | `backend/pkg/handlers/templates_handler.go:56` | Idempotency key. Never sent: no SDK wrapper sets it, so the gateway cannot recognise a retried request. |
| `template` | yes | yes | form field | `frontend/src/components/CreateTemplateModal.tsx:132` |  |
| `workspace_scope` | yes | yes | plumbing | `backend/pkg/handlers/templates_handler.go:56` |  |

### `DeleteSandboxRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `allow_missing` | yes | no | **not offered** | `backend/pkg/handlers/sandboxes_handler.go:145` | Handler passes no DeleteOptions, so it is always false and a sandbox that is already gone answers 404. |
| `name` | yes | yes | plumbing | `backend/pkg/handlers/sandboxes_handler.go:145` | The sandbox of the page or row. |
| `request_id` | no | no | plumbing | `backend/pkg/handlers/sandboxes_handler.go:145` | Idempotency key. Never sent: no SDK wrapper sets it, so the gateway cannot recognise a retried request. |
| `workspace_scope` | yes | yes | plumbing | `backend/pkg/handlers/sandboxes_handler.go:145` |  |

### `DeleteSandboxTemplateRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `allow_missing` | yes | no | **not offered** | `backend/pkg/handlers/templates_handler.go:77` | Handler passes no DeleteOptions, so it is always false. |
| `name` | yes | yes | plumbing | `backend/pkg/handlers/templates_handler.go:77` | The template of the row. |
| `request_id` | no | no | plumbing | `backend/pkg/handlers/templates_handler.go:77` | Idempotency key. Never sent: no SDK wrapper sets it, so the gateway cannot recognise a retried request. |
| `workspace_scope` | yes | yes | plumbing | `backend/pkg/handlers/templates_handler.go:77` |  |

### `GetSandboxRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `name` | yes | yes | plumbing | `backend/pkg/handlers/sandboxes_handler.go:113` | From the route. |
| `workspace_scope` | yes | yes | plumbing | `backend/pkg/handlers/sandboxes_handler.go:113` |  |

### `GetSandboxTemplateRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `name` | yes | yes | **not offered** | `backend/pkg/handlers/templates_handler.go:66` | The BFF forwards it but no UI calls the route. |
| `workspace_scope` | yes | yes | **not offered** | `backend/pkg/handlers/templates_handler.go:66` | The BFF forwards it but no UI calls the route. |

### `GpuResourceRequirements`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `count` | yes | yes | form field | `frontend/src/components/CreateSandboxModal.tsx:286` | A count of 1 or more only: gpuCount 0 is not sent (sdk_converters.go:234) and the template form sends gpu only when the count is above 0 (CreateTemplateModal.tsx:118), so a GPU with no count (driver default) cannot be requested. |

### `ListSandboxTemplatesRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `label_selector` | yes | yes | form field | `frontend/src/pages/AllWorkspacesPage.tsx:225` | Only the all-workspaces Templates table (platform admin) has a selector input; the workspace Templates tab lists without one (TemplatesTab.tsx:89) although its route accepts labelSelector. |
| `page_size` | yes | no | **not offered** | `backend/pkg/handlers/templates_handler.go:30` | ListAll sends 0 (gateway default page size) and reads every page; the template tables have no server paging. |
| `page_token` | yes | no | plumbing | `backend/pkg/handlers/templates_handler.go:30` | Filled by the SDK's ListAll pager, never by the browser. |
| `workspace_scope` | yes | yes | plumbing | `backend/pkg/handlers/templates_handler.go:30` | Named workspace from the route, or all workspaces on GET /api/v1/templates. |

### `ListSandboxesRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `label_selector` | yes | yes | form field | `frontend/src/pages/SandboxListPage.tsx:334` | Free-text selector plus a second 'type label' input, combined into one selector. |
| `page_size` | yes | yes | form field | `frontend/src/pages/SandboxListPage.tsx:169` | Per-page select of the cursor pagination; the BFF accepts 1 to 100. |
| `page_token` | yes | yes | plumbing | `frontend/src/pages/SandboxListPage.tsx:170` |  |
| `workspace_scope` | yes | yes | plumbing | `backend/pkg/handlers/sandboxes_handler.go:29` | Named workspace from the route, or all workspaces on GET /api/v1/sandboxes. |

### `ResourceRequirements`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `gpu` | yes | yes | form field | `frontend/src/components/CreateSandboxModal.tsx:286` | Set through the GPU count input of the Create sandbox form; present only when a count is given. |

### `ResumeSandboxRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `name` | yes | no | **not offered** | `backend/pkg/server/app.go:221` | No BFF route for suspend or resume. |
| `restart_if_restore_fails` | yes | no | **not offered** | `backend/pkg/server/app.go:221` | No BFF route for suspend or resume. |
| `workspace_scope` | yes | no | **not offered** | `backend/pkg/server/app.go:221` | No BFF route for suspend or resume. |

### `SandboxCommandUpdate`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `argv` | n/a | no | **not offered** | `backend/pkg/server/app.go:218` | No BFF route for UpdateSandbox and no SDK wrapper. |

### `SandboxMapUpdate`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `values` | n/a | no | **not offered** | `backend/pkg/server/app.go:218` | No BFF route for UpdateSandbox and no SDK wrapper. |

### `SandboxResources`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `cpu` | yes | yes | form field | `frontend/src/components/CreateTemplateModal.tsx:249` | Template form only. |
| `gpu` | yes | yes | form field | `frontend/src/components/CreateTemplateModal.tsx:238` | Sent only with a count above 0 (CreateTemplateModal.tsx:118); gpu without a count (driver default), which the BFF body can carry, has no input. |
| `memory` | yes | yes | form field | `frontend/src/components/CreateTemplateModal.tsx:259` | Template form only. |

### `SandboxServiceExposure`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `authorization_mode` | yes | fixed | fixed value | `backend/pkg/models/sdk_converters.go:255` | Always STRIP: the BFF's ServiceExposure has no mode field (builders.go:9) and the SDK sends STRIP for an unset mode, so BEARER_PASSTHROUGH (CLI --expose-authorization-mode) cannot be chosen at create. |
| `service` | yes | yes | form field | `frontend/src/components/sandbox/ServiceExposureEditor.tsx:44` | Optional; empty is the sandbox's unnamed service. |
| `target_port` | yes | yes | form field | `frontend/src/components/sandbox/ServiceExposureEditor.tsx:56` |  |

### `SandboxServiceLevel`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `startup` | yes | yes | form field | `frontend/src/components/CreateTemplateModal.tsx:329` | Template form, Advanced options. |

### `SandboxSpec`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `command` | yes | yes | form field | `frontend/src/components/sandbox/SandboxLaunchFields.tsx:37` | Text area, one argument per line, on both create forms. |
| `environment` | yes | yes | form field | `frontend/src/components/CreateSandboxModal.tsx:342` | Key/value editor on the Create sandbox form only; the SDK refuses it beside a workload template. |
| `log_level` | yes | yes | form field | `frontend/src/components/CreateSandboxModal.tsx:393` | Select of trace/debug/info/warn/error or gateway default; Create sandbox form only. |
| `policy` | yes | yes | form field | `frontend/src/components/CreateSandboxModal.tsx:145` | Required by the BFF (sandboxes_handler.go:69): a preset select fills a YAML/JSON editor with file upload, and the result is posted as protojson in `policy` and parsed by ParseSDKPolicy into spec.policy; there is no structured rule editor at create. |
| `provider_attachment_epoch` | no | no | plumbing | `backend/pkg/models/sdk_converters.go:207` | Not a gap: gateway-owned, the gateway overwrites whatever a caller sends at create (upstream grpc/sandbox.rs:607 at v0.1.9). |
| `providers` | yes | yes | form field | `frontend/src/components/CreateSandboxModal.tsx:212` | A checkbox per provider of the workspace, on both create forms. |
| `resource_requirements` | yes | yes | form field | `frontend/src/components/CreateSandboxModal.tsx:286` | Only gpu.count, through the BFF's gpuCount; Create sandbox form only. |
| `restart_policy` | yes | fixed | fixed value | `backend/pkg/models/sdk_converters.go:207` | Always NEVER: the BFF never sets SandboxSpec.RestartPolicy and the SDK converter sends NEVER for the empty value, so on-failure and always (CLI --restart-policy) cannot be chosen, and the BFF's Sandbox DTO does not return the value either. |
| `template` | yes | yes | form field | `frontend/src/components/CreateSandboxModal.tsx:184` | Built by the BFF from image, runtimeClassName, driverConfig, cpu and memory (sdk_converters.go:210) and always sent, even when all are empty. |
| `tty` | yes | yes | form field | `frontend/src/components/sandbox/SandboxLaunchFields.tsx:67` | Checkbox, enabled and sent only beside a command. |

### `SandboxStartup`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `max_burst` | yes | yes | form field | `frontend/src/components/CreateTemplateModal.tsx:346` |  |
| `ready_within` | yes | yes | form field | `frontend/src/components/CreateTemplateModal.tsx:335` | Whole seconds, minutes or hours (30s, 5m, 1h), sent as readyWithinMs. |

### `SandboxTemplate`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `agent_socket` | yes | no | **not offered** | `backend/pkg/models/builders.go:38` | No field on the BFF's create body, and FromSDKSandbox leaves it out of the response (sdk_converters.go:127). |
| `annotations` | yes | no | read-only | `frontend/src/components/sandbox/SandboxSpecDetails.tsx:117` | Shown on the sandbox page when set; the create body has no field for it (the form's Annotations are the sandbox's own metadata). |
| `driver_config` | yes | yes | document only | `frontend/src/components/CreateSandboxModal.tsx:424` | A JSON object typed into a text area. |
| `environment` | yes | no | read-only | `frontend/src/components/sandbox/SandboxSpecDetails.tsx:107` | Shown on the sandbox page when set; the form's environment goes to SandboxSpec.environment. |
| `image` | yes | yes | form field | `frontend/src/components/CreateSandboxModal.tsx:184` | A bare name is rewritten to the community registry reference before it is sent (useCreateSandboxForm.ts:14). |
| `labels` | yes | no | read-only | `frontend/src/components/sandbox/SandboxSpecDetails.tsx:112` | Shown on the sandbox page when set; the create body has no field for it. |
| `resources` | yes | yes | form field | `frontend/src/components/CreateSandboxModal.tsx:296` | Only limits.cpu and limits.memory (sdk_converters.go:221); requests.cpu and requests.memory, which the gateway also reads, can be shown but not set. |
| `runtime_class_name` | yes | yes | form field | `frontend/src/components/CreateSandboxModal.tsx:406` |  |
| `user_namespaces` | yes | no | read-only | `frontend/src/components/sandbox/SandboxSpecDetails.tsx:102` | Shown on the sandbox page when set; the create body has no field for it. |

### `SandboxWorkloadConfig`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `environment` | yes | yes | form field | `frontend/src/components/CreateTemplateModal.tsx:212` | BROKEN: one text input split on commas by parseLabels (useCreateSandboxForm.ts:28), so a value that contains a comma (NO_PROXY=a,b) or is empty is refused by the form and cannot be put in a template. |
| `image` | yes | yes | form field | `frontend/src/components/CreateTemplateModal.tsx:194` | Optional; a bare name is rewritten to the community registry reference. |
| `resources` | yes | yes | form field | `frontend/src/components/CreateTemplateModal.tsx:230` |  |

### `SandboxWorkloadTemplate`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `metadata` | yes | yes | form field | `frontend/src/components/CreateTemplateModal.tsx:177` | Name, labels (comma-separated text) and annotations (key/value editor); the BFF sets nothing else of ObjectMeta. |
| `spec` | yes | yes | form field | `frontend/src/components/CreateTemplateModal.tsx:137` |  |

### `SandboxWorkloadTemplateSpec`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `desired_service_level` | yes | yes | form field | `frontend/src/components/CreateTemplateModal.tsx:329` |  |
| `driver_config` | yes | yes | document only | `frontend/src/components/CreateTemplateModal.tsx:387` | A JSON object typed into a text area. |
| `workload` | yes | yes | form field | `frontend/src/components/CreateTemplateModal.tsx:141` | Always sent, even empty, because the gateway refuses a template without a workload. |

### `StartSandboxRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `name` | yes | yes | plumbing | `backend/pkg/handlers/sandboxes_handler.go:136` | The sandbox of the page or row. |
| `request_id` | no | no | plumbing | `backend/pkg/handlers/sandboxes_handler.go:136` | Idempotency key. Never sent: no SDK wrapper sets it, so the gateway cannot recognise a retried request. |
| `workspace_scope` | yes | yes | plumbing | `backend/pkg/handlers/sandboxes_handler.go:136` |  |

### `StopSandboxRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `name` | yes | yes | plumbing | `backend/pkg/handlers/sandboxes_handler.go:125` | The sandbox of the page or row. |
| `request_id` | no | no | plumbing | `backend/pkg/handlers/sandboxes_handler.go:125` | Idempotency key. Never sent: no SDK wrapper sets it, so the gateway cannot recognise a retried request. |
| `workspace_scope` | yes | yes | plumbing | `backend/pkg/handlers/sandboxes_handler.go:125` |  |

### `SuspendSandboxRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `name` | yes | no | **not offered** | `backend/pkg/server/app.go:220` | No BFF route for suspend or resume. |
| `workspace_scope` | yes | no | **not offered** | `backend/pkg/server/app.go:220` | No BFF route for suspend or resume. |

### `UpdateSandboxRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `annotations` | n/a | no | **not offered** | `backend/pkg/server/app.go:218` | No BFF route for UpdateSandbox and no SDK wrapper. |
| `command` | n/a | no | **not offered** | `backend/pkg/server/app.go:218` | No BFF route for UpdateSandbox and no SDK wrapper. |
| `environment` | n/a | no | **not offered** | `backend/pkg/server/app.go:218` | No BFF route for UpdateSandbox and no SDK wrapper. |
| `expected_resource_version` | n/a | no | **not offered** | `backend/pkg/server/app.go:218` | No BFF route for UpdateSandbox and no SDK wrapper. |
| `image` | n/a | no | **not offered** | `backend/pkg/server/app.go:218` | No BFF route for UpdateSandbox and no SDK wrapper. |
| `labels` | n/a | no | **not offered** | `backend/pkg/server/app.go:218` | No BFF route for UpdateSandbox and no SDK wrapper. |
| `name` | n/a | no | **not offered** | `backend/pkg/server/app.go:218` | No BFF route for UpdateSandbox and no SDK wrapper. |
| `request_id` | n/a | no | plumbing | `backend/pkg/server/app.go:218` | Idempotency key. Never sent: no SDK wrapper sets it, so the gateway cannot recognise a retried request. |
| `workspace_scope` | n/a | no | **not offered** | `backend/pkg/server/app.go:218` | No BFF route for UpdateSandbox and no SDK wrapper. |

## Terminal, files, logs and services

| RPC | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `ExecSandbox` | Exec().Run, Exec().Stream | POST /api/v1/workspaces/{workspace}/sandboxes/{name}/files (mkdir -p), GET /api/v1/workspaces/{workspace}/sandboxes/{name}/files (stat, then cat or tar) | action | `frontend/src/components/sandbox/SandboxFilesTab.tsx:252` | Only behind file transfer with commands the BFF chooses; no route runs a one-shot command of the user's own without a TTY. |
| `ExecSandboxInteractive` | Exec().Interactive | GET /api/v1/workspaces/{workspace}/sandboxes/{name}/terminal (WebSocket); POST /api/v1/workspaces/{workspace}/sandboxes/{name}/files via direct gRPC (rawexec.go, tty=false) | form field | `frontend/src/components/sandbox/SandboxTerminalTab.tsx:149` |  |
| `CreateSshSession` | SSH().CreateSession | none | **not offered** | `frontend/src/components/ConnectCard.tsx:96` | The Connect card only prints `openshell sandbox connect` and `ssh-config` commands to copy; nothing calls the RPC. |
| `RevokeSshSession` | SSH().RevokeSession | none | **not offered** | `frontend/src/components/ConnectCard.tsx:96` |  |
| `ForwardTcp` | TCP().Forward, TCP().Listen, SSH().Tunnel | none | **not offered** | `frontend/src/components/ConnectCard.tsx:152` | The Connect card prints `openshell forward start` commands with a port input; nothing calls the RPC. |
| `GetSandboxLogs` | Sandboxes().GetLogs | GET /api/v1/workspaces/{workspace}/sandboxes/{name}/logs | form field | `frontend/src/components/sandbox/SandboxLogsTab.tsx:118` | Called only as the fallback after the live stream fails or ends; the Logs tab starts on WatchSandbox. |
| `WatchSandbox` | Sandboxes().Watch (status only), SandboxLogsWatch().WatchLogs (logs and platform events) | GET /api/v1/workspaces/{workspace}/sandboxes/{name}/logs/stream (SSE) via direct gRPC (rawlogs.go) | action | `frontend/src/components/sandbox/SandboxLogsTab.tsx:175` | Log lines and warnings only: the relay drops status snapshots and never asks for platform events; rawlogs.go is a fourth direct gRPC call that the rules do not list and whose stated gap SDK v0.1.9 closes with WatchLogs. |
| `ExposeService` | Services().Expose | POST /api/v1/workspaces/{workspace}/sandboxes/{name}/services | form field | `frontend/src/components/sandbox/SandboxServicesTab.tsx:83` | Create-or-update on the gateway: exposing an existing name again rewrites it, authorization mode included. |
| `GetService` | Services().Get | none | **not offered** | `backend/pkg/server/app.go:247` | No route; the UI reads endpoints through ListServices only. |
| `ListServices` | Services().ListAll | GET /api/v1/workspaces/{workspace}/sandboxes/{name}/services, GET /api/v1/workspaces/{workspace}/services, GET /api/v1/services (all workspaces) | action | `frontend/src/components/sandbox/SandboxServicesTab.tsx:59` |  |
| `DeleteService` | Services().Delete | DELETE /api/v1/workspaces/{workspace}/sandboxes/{name}/services/{svc}, DELETE /api/v1/workspaces/{workspace}/sandboxes/{name}/services (unnamed endpoint) | action | `frontend/src/components/sandbox/SandboxServicesTab.tsx:182` |  |
| `ServiceDiscovery.GetServiceRouteSnapshot` | generated client only | none | **not offered** | `backend/pkg/server/app.go:247` | No SDK wrapper, no route, no UI for a workspace's installed application routes. |
| `ServiceDiscovery.WatchServiceRoutes` | generated client only | none | **not offered** | `backend/pkg/server/app.go:247` |  |

### `CreateSshSessionRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `sandbox` | yes | no | **not offered** | `frontend/src/components/ConnectCard.tsx:96` |  |
| `workspace_scope` | yes | no | **not offered** | `frontend/src/components/ConnectCard.tsx:96` |  |

### `DeleteServiceRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `allow_missing` | yes | no | **not offered** | `backend/pkg/handlers/services_handler.go:72` | The BFF passes no DeleteOptions, so it is always false. |
| `name` | yes | yes | plumbing | `frontend/src/components/sandbox/SandboxServicesTab.tsx:182` | The name of the row whose Delete action was chosen; empty for the unnamed endpoint. |
| `request_id` | no | no | plumbing | `backend/pkg/handlers/services_handler.go:72` | Idempotency key. Never sent: no SDK wrapper sets it, so the gateway cannot recognise a retried request. |
| `sandbox` | yes | yes | plumbing | `frontend/src/components/sandbox/SandboxServicesTab.tsx:182` | From the route. |
| `workspace_scope` | yes | yes | plumbing | `frontend/src/components/sandbox/SandboxServicesTab.tsx:182` | From the route. |

### `ExecSandboxInput`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `resize` | yes | yes | plumbing | `frontend/src/components/sandbox/SandboxTerminalTab.tsx:206` | Sent as a JSON text frame whenever xterm is refitted. |
| `start` | yes | yes | form field | `frontend/src/components/sandbox/SandboxTerminalTab.tsx:160` | The Session options form becomes the first WebSocket frame (?start=message). |
| `stdin` | yes | yes | form field | `frontend/src/components/sandbox/SandboxTerminalTab.tsx:200` | Keystrokes typed in the terminal; uploads send file bytes in 256 KiB frames through rawexec.go. |

### `ExecSandboxRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `cols` | yes | yes | plumbing | `frontend/src/components/sandbox/SandboxTerminalTab.tsx:147` | xterm's fitted width as ?cols=; file-transfer commands send 0. |
| `command` | yes | yes | form field | `frontend/src/components/sandbox/SandboxTerminalTab.tsx:267` | Terminal Session options only, one argument per line (default /bin/bash); file-transfer commands are fixed by the BFF. |
| `environment` | yes | yes | form field | `frontend/src/components/sandbox/SandboxTerminalTab.tsx:314` | Terminal Session options only; file-transfer commands always send LC_ALL=C. |
| `execution_timeout` | no | no | **not offered** | `backend/pkg/handlers/files_handler.go:89` | The SDK's ExecOptions has no timeout; the BFF bounds file-transfer commands with its own 30 s context deadline and the terminal has none (the CLI has --timeout). |
| `no_login_shell` | yes | yes | form field | `frontend/src/components/sandbox/SandboxTerminalTab.tsx:338` | Terminal Session options only (Skip shell startup files). |
| `request_id` | no | no | plumbing | `backend/pkg/handlers/terminal_handler.go:245` | Idempotency key. Never sent: no SDK wrapper sets it, so the gateway cannot recognise a retried request. |
| `rows` | yes | yes | plumbing | `frontend/src/components/sandbox/SandboxTerminalTab.tsx:147` | xterm's fitted height as ?rows=; file-transfer commands send 0. |
| `sandbox` | yes | yes | plumbing | `frontend/src/components/sandbox/SandboxTerminalTab.tsx:147` | From the route. |
| `stdin` | no | no | **not offered** | `backend/pkg/clients/rawexec.go:138` | Exec().Run and Stream take no stdin; uploads send it as ExecSandboxInput.stdin frames instead, which also avoids the 1 MiB message limit. |
| `tty` | no | fixed | fixed value | `backend/pkg/handlers/terminal_handler.go:245` | Always true for the terminal (the SDK's Interactive hardcodes it) and false for file transfer, so a user's command cannot run without a PTY (the CLI has --no-tty). |
| `workdir` | yes | yes | form field | `frontend/src/components/sandbox/SandboxTerminalTab.tsx:300` | Terminal Session options only. |
| `workspace_scope` | yes | yes | plumbing | `frontend/src/components/sandbox/SandboxTerminalTab.tsx:147` | From the route. |

### `ExposeServiceRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `authorization_mode` | yes | fixed | fixed value | `backend/pkg/handlers/services_handler.go:60` | Always SERVICE_AUTHORIZATION_MODE_STRIP: the BFF passes no ExposeServiceOptions and its body has no field for it, so BEARER_PASSTHROUGH is unreachable and re-exposing an endpoint the CLI set to it resets it to STRIP. |
| `domain` | yes | yes | fixed value | `frontend/src/components/sandbox/SandboxServicesTab.tsx:83` | The form always sends true; gateway 0.1.9 still stores true whatever is sent (grpc/service.rs). |
| `name` | yes | yes | form field | `frontend/src/components/sandbox/SandboxServicesTab.tsx:208` | Optional; empty is the sandbox's unnamed endpoint. |
| `request_id` | no | no | plumbing | `backend/pkg/handlers/services_handler.go:60` | Idempotency key. Never sent: no SDK wrapper sets it, so the gateway cannot recognise a retried request. |
| `sandbox` | yes | yes | plumbing | `frontend/src/components/sandbox/SandboxServicesTab.tsx:83` | From the route. |
| `target_port` | yes | yes | form field | `frontend/src/components/sandbox/SandboxServicesTab.tsx:225` |  |
| `workspace_scope` | yes | yes | plumbing | `frontend/src/components/sandbox/SandboxServicesTab.tsx:83` | From the route. |

### `GetSandboxLogsRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `lines` | yes | yes | form field | `frontend/src/components/sandbox/SandboxLogsTab.tsx:391` | Line count select (100, 200, 500, 2000). Reaches the gateway only in the polling fallback; while the stream is up the control filters the retained lines in the browser. |
| `min_level` | yes | yes | form field | `frontend/src/components/sandbox/SandboxLogsTab.tsx:364` | Minimum level select. Reaches the gateway only in the polling fallback; while the stream is up the control filters the retained lines in the browser. |
| `sandbox` | yes | yes | plumbing | `frontend/src/components/sandbox/SandboxLogsTab.tsx:118` | From the route. |
| `since_time` | yes | yes | form field | `frontend/src/components/sandbox/SandboxLogsTab.tsx:404` | Typed as a duration back from now (30s, 5m, 1h), sent as ?sinceMs=; no absolute time. Reaches the gateway only in the polling fallback; while the stream is up the control filters the retained lines in the browser. |
| `sources` | yes | yes | form field | `frontend/src/components/sandbox/SandboxLogsTab.tsx:379` | Source select, one of gateway or sandbox. Reaches the gateway only in the polling fallback; while the stream is up the control filters the retained lines in the browser. |
| `workspace_scope` | yes | yes | plumbing | `frontend/src/components/sandbox/SandboxLogsTab.tsx:118` | From the route. |

### `GetServiceRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `name` | yes | no | **not offered** | `backend/pkg/server/app.go:247` |  |
| `sandbox` | yes | no | **not offered** | `backend/pkg/server/app.go:247` |  |
| `workspace_scope` | yes | no | **not offered** | `backend/pkg/server/app.go:247` |  |

### `GetServiceRouteSnapshotRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `workspace_scope` | n/a | no | **not offered** | `backend/pkg/server/app.go:247` |  |

### `ListServicesRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `page_size` | yes | no | **not offered** | `backend/pkg/handlers/services_handler.go:39` | The BFF calls ListAll with no page size (0, the gateway's default) and returns every page as one array. |
| `page_token` | yes | no | plumbing | `backend/pkg/handlers/services_handler.go:39` | Filled by the SDK pager inside ListAll; the browser never pages. |
| `sandbox` | yes | yes | plumbing | `frontend/src/components/sandbox/SandboxServicesTab.tsx:59` | The sandbox of the route on the Services tab; empty on the workspace and all-workspaces lists. |
| `workspace_scope` | yes | yes | plumbing | `frontend/src/components/sandbox/SandboxServicesTab.tsx:59` | The route's workspace, or all workspaces on GET /api/v1/services. |

### `RevokeSshSessionRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `allow_missing` | yes | no | **not offered** | `frontend/src/components/ConnectCard.tsx:96` |  |
| `token` | yes | no | **not offered** | `frontend/src/components/ConnectCard.tsx:96` |  |

### `TcpForwardFrame`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `data` | yes | no | **not offered** | `frontend/src/components/ConnectCard.tsx:152` |  |
| `init` | yes | no | **not offered** | `frontend/src/components/ConnectCard.tsx:152` |  |

### `TcpForwardInit`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `authorization_token` | yes | no | **not offered** | `frontend/src/components/ConnectCard.tsx:152` | Set only by the SDK's SSH().Tunnel, to the session token it has just created. |
| `sandbox` | yes | no | **not offered** | `frontend/src/components/ConnectCard.tsx:152` |  |
| `service_id` | yes | no | **not offered** | `frontend/src/components/ConnectCard.tsx:152` |  |
| `ssh` | yes | no | **not offered** | `frontend/src/components/ConnectCard.tsx:152` |  |
| `tcp` | yes | no | **not offered** | `frontend/src/components/ConnectCard.tsx:152` |  |
| `workspace` | yes | no | **not offered** | `frontend/src/components/ConnectCard.tsx:152` |  |

### `TcpRelayTarget`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `host` | no | no | **not offered** | `frontend/src/components/ConnectCard.tsx:152` | The SDK's TCP().Forward hardcodes 127.0.0.1. |
| `port` | yes | no | **not offered** | `frontend/src/components/ConnectCard.tsx:152` | The Connect card's Port input only fills in a CLI command to copy. |

### `WatchSandboxRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `event_tail` | yes | no | **not offered** | `backend/pkg/services/sandbox.go:45` |  |
| `follow_events` | yes | no | **not offered** | `backend/pkg/services/sandbox.go:45` | Never set, and the SSE relay forwards only log and warning payloads, so platform events cannot be shown. |
| `follow_logs` | yes | fixed | fixed value | `backend/pkg/services/sandbox.go:53` | Always true. |
| `follow_status` | yes | fixed | fixed value | `backend/pkg/services/sandbox.go:52` | Always true, yet the relay discards every status snapshot it then receives (logs_handler.go:165). |
| `log_min_level` | yes | yes | **not offered** | `backend/pkg/handlers/logs_handler.go:91` | The route takes ?level= but the UI never sends it: the Minimum level select filters the retained lines in the browser, and hides every OCSF line the gateway's own filter would keep. |
| `log_sources` | yes | yes | **not offered** | `backend/pkg/handlers/logs_handler.go:90` | The route takes ?source= but the UI never sends it: the Source select filters the retained lines in the browser. |
| `log_tail_lines` | yes | yes | fixed value | `frontend/src/components/sandbox/SandboxLogsTab.tsx:178` | The UI always sends lines=2000; the Line count select only trims what is shown. |
| `resume_after_cursor` | yes | no | **not offered** | `backend/pkg/services/sandbox.go:45` | The relay drops each event's cursor, so a dropped stream cannot resume; the UI falls back to polling instead. |
| `sandbox` | yes | yes | plumbing | `frontend/src/components/sandbox/SandboxLogsTab.tsx:175` | From the route. |
| `since_time` | no | no | **not offered** | `backend/pkg/services/sandbox.go:45` | No SDK wrapper sets it; the Since input filters the retained lines in the browser. |
| `stop_on_terminal` | yes | no | **not offered** | `backend/pkg/services/sandbox.go:45` |  |
| `workspace_scope` | yes | yes | plumbing | `frontend/src/components/sandbox/SandboxLogsTab.tsx:175` | From the route. |

### `WatchServiceRoutesRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `workspace_scope` | n/a | no | **not offered** | `backend/pkg/server/app.go:247` |  |

Requests with nothing for a person to author: `ExecSandboxWindowResize` (`cols`, `rows`).

## Providers and credential refresh

| RPC | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `CreateProvider` | Providers().Create | POST /api/v1/workspaces/{workspace}/providers | form field | `frontend/src/components/provider/ProviderFormModal.tsx:292` | Form sends name, type, profileWorkspace, credentials and config; labels, annotations and credential expiry cannot be set at create. |
| `GetProvider` | Providers().Get | GET /api/v1/workspaces/{workspace}/providers/{name} (plus direct gRPC, rawprovider.go, for the credential keys) | read-only | `frontend/src/pages/ProviderDetailPage.tsx:82` |  |
| `ListProviders` | Providers().ListAll | GET /api/v1/workspaces/{workspace}/providers; GET /api/v1/providers (all workspaces); plus direct gRPC (rawprovider.go, rawprovider_allworkspaces.go) for the credential keys | read-only | `frontend/src/pages/ProviderListPage.tsx:82` | No label selector and no paging reach the browser; the SDK's provider List drops ListOptions.LabelSelector. |
| `UpdateProvider` | Providers().Update | PUT /api/v1/workspaces/{workspace}/providers/{name} | form field | `frontend/src/components/provider/ProviderFormModal.tsx:276` | Edit form can add or replace credential values, set expiries and change config; it cannot remove a credential, clear an expiry or send the provider's resource_version. |
| `DeleteProvider` | Providers().Delete | DELETE /api/v1/workspaces/{workspace}/providers/{name} | action | `frontend/src/pages/ProviderListPage.tsx:108` |  |
| `ConfigureProviderRefresh` | Providers().Refresh().Configure | POST /api/v1/workspaces/{workspace}/providers/{name}/refresh | form field | `frontend/src/pages/ProviderDetailPage.tsx:470` |  |
| `GetProviderRefreshStatus` | Providers().Refresh().GetStatus | GET /api/v1/workspaces/{workspace}/providers/{name}/refresh-status | read-only | `frontend/src/pages/ProviderDetailPage.tsx:84` | Always read for the whole provider (credential_key empty). |
| `RotateProviderCredential` | Providers().Refresh().Rotate | POST /api/v1/workspaces/{workspace}/providers/{name}/refresh/rotate | action | `frontend/src/pages/ProviderDetailPage.tsx:350` |  |
| `DeleteProviderRefresh` | Providers().Refresh().Delete | DELETE /api/v1/workspaces/{workspace}/providers/{name}/refresh?credentialKey= | action | `frontend/src/pages/ProviderDetailPage.tsx:495` |  |
| `AttachSandboxProvider` | Sandboxes().AttachProvider | POST /api/v1/workspaces/{workspace}/sandboxes/{name}/providers/{provider} | action | `frontend/src/components/sandbox/SandboxProvidersTab.tsx:106` | Runs without expected_resource_version although the component's comment (line 36) says it passes one. |
| `DetachSandboxProvider` | Sandboxes().DetachProvider | DELETE /api/v1/workspaces/{workspace}/sandboxes/{name}/providers/{provider} | action | `frontend/src/components/sandbox/SandboxProvidersTab.tsx:155` | Runs without expected_resource_version: the client's del() sends no body. |
| `ListSandboxProviders` | Sandboxes().ListAllProviders | GET /api/v1/workspaces/{workspace}/sandboxes/{name}/providers (plus direct gRPC, rawprovider.go, for the credential keys) | read-only | `frontend/src/components/sandbox/SandboxProvidersTab.tsx:42` |  |
| `GetSandboxProviderStatus` | generated client only | none | **not offered** | `backend/pkg/server/app.go:228` | No wrapper, route or UI: the dashboard cannot tell whether an attach, detach or provider update has reached a sandbox, and the SDK also drops the receipt those responses carry. |

### `AttachSandboxProviderRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `expected_resource_version` | yes | yes | **not offered** | `frontend/src/api/sandboxes.ts:227` | Not sent: the BFF accepts expectedResourceVersion and the SDK forwards it, but the UI sends none, so the gateway attaches without a version check (0 = retry on conflict). |
| `provider` | yes | yes | form field | `frontend/src/components/sandbox/SandboxProvidersTab.tsx:83` | Select of the workspace's providers not yet attached. |
| `request_id` | no | no | plumbing | `backend/pkg/handlers/logs_handler.go:273` | Idempotency key. Never sent: no SDK wrapper sets it, so the gateway cannot recognise a retried request. |
| `sandbox` | yes | yes | plumbing | `frontend/src/components/sandbox/SandboxProvidersTab.tsx:44` | From the route's {name}. |
| `workspace_scope` | yes | yes | plumbing | `frontend/src/components/sandbox/SandboxProvidersTab.tsx:44` | Named workspace from the route's {workspace}. |

### `ConfigureProviderRefreshRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `credential_key` | yes | yes | form field | `frontend/src/components/provider/ConfigureRefreshModal.tsx:134` | Select of the keys the provider holds plus those its profile declares. |
| `expiration_time` | yes | yes | form field | `frontend/src/components/provider/ConfigureRefreshModal.tsx:230` | datetime-local input with no guard against a past time (the provider edit form has one), and the gateway writes it as the credential's expiry. |
| `material` | yes | yes | form field | `frontend/src/components/provider/ConfigureRefreshModal.tsx:182` | Key/value rows, prefilled from the profile's declared refresh material. |
| `provider` | yes | yes | plumbing | `frontend/src/pages/ProviderDetailPage.tsx:85` | From the route's {name}. |
| `request_id` | no | no | plumbing | `backend/pkg/handlers/providers_handler.go:278` | Idempotency key. Never sent: no SDK wrapper sets it, so the gateway cannot recognise a retried request. |
| `secret_material_keys` | yes | yes | form field | `frontend/src/components/provider/ConfigureRefreshModal.tsx:201` | Secret checkbox per material row. |
| `strategy` | yes | yes | form field | `frontend/src/components/provider/ConfigureRefreshModal.tsx:147` | Select offers 4 of the 6 enum values (OAuth2 refresh token, OAuth2 client credentials, Google service account JWT, AWS STS assume role); STATIC and EXTERNAL are left out because the gateway refuses them here as not gateway-mintable, though the BFF map still accepts both. |
| `workspace_scope` | yes | yes | plumbing | `frontend/src/pages/ProviderDetailPage.tsx:85` | Named workspace from the route's {workspace}. |

### `CreateProviderRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `provider` | yes | yes | form field | `frontend/src/components/provider/ProviderFormModal.tsx:292` |  |
| `request_id` | no | no | plumbing | `backend/pkg/handlers/providers_handler.go:160` | Idempotency key. Never sent: no SDK wrapper sets it, so the gateway cannot recognise a retried request. |
| `workspace_scope` | yes | yes | plumbing | `frontend/src/components/provider/ProviderFormModal.tsx:126` | Named workspace from the route's {workspace}. |

### `CredentialHandle`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `driver` | yes | no | **not offered** | `backend/pkg/handlers/providers_handler.go:150` | Gateway-internal: the gateway refuses a create or update that supplies credential_handles and clears them on every read, so there is nothing to author. |
| `handle` | yes | no | **not offered** | `backend/pkg/handlers/providers_handler.go:150` | Gateway-internal: the gateway refuses a create or update that supplies credential_handles and clears them on every read, so there is nothing to author. |
| `metadata` | yes | no | **not offered** | `backend/pkg/handlers/providers_handler.go:150` | Gateway-internal: the gateway refuses a create or update that supplies credential_handles and clears them on every read, so there is nothing to author. |

### `DeleteProviderRefreshRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `allow_missing` | yes | fixed | fixed value | `backend/pkg/handlers/providers_handler.go:318` | Always false: the BFF passes no DeleteOptions. |
| `credential_key` | yes | yes | action | `frontend/src/pages/ProviderDetailPage.tsx:495` | The status row whose Delete button is clicked. |
| `provider` | yes | yes | plumbing | `frontend/src/pages/ProviderDetailPage.tsx:90` | From the route's {name}. |
| `request_id` | no | no | plumbing | `backend/pkg/handlers/providers_handler.go:318` | Idempotency key. Never sent: no SDK wrapper sets it, so the gateway cannot recognise a retried request. |
| `workspace_scope` | yes | yes | plumbing | `frontend/src/pages/ProviderDetailPage.tsx:90` | Named workspace from the route's {workspace}. |

### `DeleteProviderRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `allow_missing` | yes | fixed | fixed value | `backend/pkg/handlers/providers_handler.go:188` | Always false: the BFF passes no DeleteOptions. |
| `name` | yes | yes | action | `frontend/src/pages/ProviderListPage.tsx:396` | The row or selection being deleted. |
| `request_id` | no | no | plumbing | `backend/pkg/handlers/providers_handler.go:188` | Idempotency key. Never sent: no SDK wrapper sets it, so the gateway cannot recognise a retried request. |
| `workspace_scope` | yes | yes | plumbing | `frontend/src/pages/ProviderListPage.tsx:108` | Named workspace from the route's {workspace}. |

### `DetachSandboxProviderRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `expected_resource_version` | yes | yes | **not offered** | `frontend/src/api/sandboxes.ts:235` | Not sent: the BFF would read expectedResourceVersion from a body, but detachProvider() calls del() with none. |
| `provider` | yes | yes | action | `frontend/src/components/sandbox/SandboxProvidersTab.tsx:155` | The row whose Detach action is clicked. |
| `request_id` | no | no | plumbing | `backend/pkg/handlers/logs_handler.go:297` | Idempotency key. Never sent: no SDK wrapper sets it, so the gateway cannot recognise a retried request. |
| `sandbox` | yes | yes | plumbing | `frontend/src/components/sandbox/SandboxProvidersTab.tsx:45` | From the route's {name}. |
| `workspace_scope` | yes | yes | plumbing | `frontend/src/components/sandbox/SandboxProvidersTab.tsx:45` | Named workspace from the route's {workspace}. |

### `GetProviderRefreshStatusRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `credential_key` | yes | fixed | fixed value | `backend/pkg/handlers/providers_handler.go:241` | Always empty: the status of every credential of the provider is read. |
| `provider` | yes | yes | plumbing | `frontend/src/pages/ProviderDetailPage.tsx:84` | From the route's {name}. |
| `workspace_scope` | yes | yes | plumbing | `frontend/src/pages/ProviderDetailPage.tsx:84` | Named workspace from the route's {workspace}. |

### `GetProviderRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `name` | yes | yes | plumbing | `frontend/src/pages/ProviderDetailPage.tsx:82` | From the route's {name}. |
| `workspace_scope` | yes | yes | plumbing | `frontend/src/pages/ProviderDetailPage.tsx:82` | Named workspace from the route's {workspace}. |

### `GetSandboxProviderStatusRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `provider` | n/a | no | **not offered** | `backend/pkg/server/app.go:228` | RPC has no SDK wrapper, BFF route or UI. |
| `receipt_id` | n/a | no | **not offered** | `backend/pkg/server/app.go:228` | RPC has no SDK wrapper, BFF route or UI. The receipts it would name come back on attach, detach and update, where the SDK drops them. |
| `sandbox` | n/a | no | **not offered** | `backend/pkg/server/app.go:228` | RPC has no SDK wrapper, BFF route or UI. |
| `workspace_scope` | n/a | no | **not offered** | `backend/pkg/server/app.go:228` | RPC has no SDK wrapper, BFF route or UI. |

### `ListProvidersRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `label_selector` | no | no | **not offered** | `backend/pkg/handlers/allworkspaces_handler.go:102` | The gateway filters providers by label (CLI: provider list --label-selector) but the SDK's Providers().List never sets it, and comments in the BFF (line 99) and frontend/src/api/allWorkspaces.ts:42 say the gateway offers no filter. |
| `page_size` | yes | fixed | plumbing | `backend/pkg/handlers/providers_handler.go:100` | Always unset (gateway default): the BFF reads every page with ListAll and the table pages in the browser. |
| `page_token` | yes | no | plumbing | `backend/pkg/handlers/providers_handler.go:100` | Filled in by the SDK pager inside ListAll; the browser never pages. |
| `workspace_scope` | yes | yes | plumbing | `frontend/src/pages/ProviderListPage.tsx:82` | Named workspace from the route; all_workspaces on GET /api/v1/providers (All workspaces and Gateway pages). |

### `ListSandboxProvidersRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `page_size` | yes | fixed | plumbing | `backend/pkg/handlers/logs_handler.go:240` | Always unset (gateway default): the BFF reads every page with ListAllProviders. |
| `page_token` | yes | no | plumbing | `backend/pkg/handlers/logs_handler.go:240` | Filled in by the SDK pager inside ListAllProviders. |
| `sandbox` | yes | yes | plumbing | `frontend/src/components/sandbox/SandboxProvidersTab.tsx:42` | From the route's {name}. |
| `workspace_scope` | yes | yes | plumbing | `frontend/src/components/sandbox/SandboxProvidersTab.tsx:42` | Named workspace from the route's {workspace}. |

### `Provider`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `config` | yes | yes | form field | `frontend/src/components/provider/ProviderFormModal.tsx:622` | Key/value editor on create and edit; an edit sends only changed entries and an empty value for each removed one. |
| `credential_expiration_times` | yes | yes | form field | `frontend/src/components/provider/ProviderFormModal.tsx:586` | Edit form only, future times only; the create body has no such field, so a new provider cannot be given an expiry. |
| `credential_handles` | yes | no | **not offered** | `backend/pkg/handlers/providers_handler.go:150` | Gateway-internal: the gateway refuses a create or update that supplies credential_handles and clears them on every read, so there is nothing to author. |
| `credentials` | yes | yes | form field | `frontend/src/components/provider/ProviderFormModal.tsx:505` | Password input per profile credential, sent under its env var name (select at line 550 when several); the edit form never sends an empty value, so a stored credential cannot be removed. |
| `metadata` | yes | yes | form field | `frontend/src/components/provider/ProviderFormModal.tsx:395` | Only the name is authored; the BFF body also takes labels, which no form sends, and has no annotations field. |
| `profile_workspace` | yes | yes | form field | `frontend/src/components/provider/ProviderFormModal.tsx:298` | No input of its own: derived from the scope of the profile picked in the Type select (the workspace for a workspace profile, empty otherwise), so the CLI default of the workspace for a platform profile cannot be produced. |
| `type` | yes | yes | form field | `frontend/src/components/provider/ProviderFormModal.tsx:412` | Select of the profiles the workspace sees; create only (immutable afterwards). |

### `RotateProviderCredentialRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `credential_key` | yes | yes | action | `frontend/src/pages/ProviderDetailPage.tsx:350` | The status row whose Rotate now button is clicked. |
| `provider` | yes | yes | plumbing | `frontend/src/pages/ProviderDetailPage.tsx:89` | From the route's {name}. |
| `request_id` | no | no | plumbing | `backend/pkg/handlers/providers_handler.go:299` | Idempotency key. Never sent: no SDK wrapper sets it, so the gateway cannot recognise a retried request. |
| `workspace_scope` | yes | yes | plumbing | `frontend/src/pages/ProviderDetailPage.tsx:89` | Named workspace from the route's {workspace}. |

### `UpdateProviderRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `clear_credential_expiration_keys` | yes | no | **not offered** | `backend/pkg/handlers/providers_handler.go:222` | The SDK clears a key given a zero time, but the BFF refuses any expiry at or before 1970 and has no clear field, so an expiry once set cannot be removed (CLI: --credential-expires-at KEY=0). |
| `credential_expiration_times` | yes | yes | form field | `frontend/src/components/provider/ProviderFormModal.tsx:586` | Same input as Provider.credential_expiration_times: the SDK copies the map into both places. |
| `provider` | yes | yes | form field | `frontend/src/components/provider/ProviderFormModal.tsx:276` | Carries the name and whichever of credentials, config and expiries the form changed; metadata.resource_version is not sent. |
| `request_id` | no | no | plumbing | `backend/pkg/handlers/providers_handler.go:232` | Idempotency key. Never sent: no SDK wrapper sets it, so the gateway cannot recognise a retried request. |
| `workspace_scope` | yes | yes | plumbing | `frontend/src/components/provider/ProviderFormModal.tsx:127` | Named workspace from the route's {workspace}. |

## Provider profiles

A profile's `endpoints` are `NetworkEndpoint` messages, the same as a policy's. Their rows under Policy describe the sandbox policy editors; what the Create profile form can set of an endpoint is in the section on that form above.

| RPC | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `ListProviderProfiles` | Profiles().List (not used) | GET /api/v1/workspaces/{workspace}/provider-profiles and /api/v1/provider-profiles (platform) — direct gRPC (rawprofile.go) | read-only | `frontend/src/components/provider/ProfilesPanel.tsx:236` | The SDK's Profiles() client exists, but its endpoint type keeps host, port and protocol only, so the BFF calls the generated client (rawprofile.go, hard fact 24). |
| `GetProviderProfile` | Profiles().Get (not used) | GET .../provider-profiles/{profileId} — direct gRPC (rawprofile.go) | read-only | `frontend/src/pages/ProviderDetailPage.tsx:57` | Read for the export and for the profile a provider was made from. |
| `ImportProviderProfiles` | Profiles().Import (not used) | POST .../provider-profiles — direct gRPC (rawprofile.go) | form field | `frontend/src/components/CreateProfileModal.tsx:104` | BROKEN: the form's body is refused by the gateway whenever it has both a credential and an endpoint (measured on 0.1.6, same rule in source at 0.1.9). Importing a YAML or JSON file works (frontend/src/components/provider/ProfileFileModal.tsx). |
| `LintProviderProfiles` | Profiles().Lint (not used) | POST .../provider-profiles/lint — direct gRPC (rawprofile.go) | action | `frontend/src/components/provider/ProfileFileModal.tsx:105` | Run on every file chosen for import. The Create profile form does not lint: it imports and shows the refusal. |
| `UpdateProviderProfiles` | Profiles().Update (not used) | PUT .../provider-profiles/{profileId} — direct gRPC (rawprofile.go) | document only | `frontend/src/components/provider/ProfileFileModal.tsx:216` | Update is from a file only (export, edit on disk, choose the file): there is no edit form and no in-browser editor. |
| `DeleteProviderProfile` | Profiles().Delete | DELETE .../provider-profiles/{profileId} | action | `frontend/src/components/provider/ProfilesPanel.tsx:323` |  |

### `DeleteProviderProfileRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `allow_missing` | yes | no | **not offered** | `backend/pkg/handlers/provider_profiles_handler.go:215` | Never sent; deleting a profile that is already gone is an error. |
| `id` | yes | yes | action | `frontend/src/components/provider/ProfilesPanel.tsx:324` |  |
| `request_id` | no | no | plumbing | `backend/pkg/handlers/provider_profiles_handler.go:215` | Idempotency key. Never sent: no SDK wrapper sets it, so the gateway cannot recognise a retried request. |
| `workspace_scope` | yes | yes | plumbing | `backend/pkg/clients/rawprofile.go:43` | The workspace of the route, or none for a platform profile. |

### `GetProviderProfileRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `id` | yes | yes | plumbing | `backend/pkg/clients/rawprofile.go:67` |  |
| `workspace_scope` | yes | yes | plumbing | `backend/pkg/clients/rawprofile.go:43` | The workspace of the route, or none for a platform profile. |

### `ImportProviderProfilesRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `profiles` | yes | yes | form field | `frontend/src/components/CreateProfileModal.tsx:115` | One profile from the form; several from files. |
| `request_id` | no | no | plumbing | `backend/pkg/clients/rawprofile.go:80` | Idempotency key. Never sent: no SDK wrapper sets it, so the gateway cannot recognise a retried request. |
| `workspace_scope` | yes | yes | plumbing | `backend/pkg/clients/rawprofile.go:43` | The workspace of the route, or none for a platform profile. |

### `LintProviderProfilesRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `profiles` | yes | yes | document only | `frontend/src/components/provider/ProfileFileModal.tsx:105` |  |
| `workspace_scope` | yes | yes | plumbing | `backend/pkg/clients/rawprofile.go:43` | The workspace of the route, or none for a platform profile. |

### `ListProviderProfilesRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `page_size` | yes | fixed | plumbing | `backend/pkg/clients/rawprofile.go:43` | The BFF reads every page and returns one list. |
| `page_token` | yes | fixed | plumbing | `backend/pkg/clients/rawprofile.go:49` |  |
| `workspace_scope` | yes | yes | plumbing | `backend/pkg/clients/rawprofile.go:43` | The workspace of the route, or none for a platform profile. |

### `ProviderCredentialRefresh`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `additional_outputs` | yes | yes | document only | `frontend/src/utils/profileFile.ts:705` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |
| `material` | yes | yes | document only | `frontend/src/utils/profileFile.ts:688` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |
| `max_lifetime` | yes | yes | document only | `frontend/src/utils/profileFile.ts:720` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |
| `refresh_before` | yes | yes | document only | `frontend/src/utils/profileFile.ts:719` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |
| `scopes` | yes | yes | document only | `frontend/src/utils/profileFile.ts:686` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |
| `strategy` | yes | yes | document only | `frontend/src/utils/profileFile.ts:677` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |
| `token_url` | yes | yes | document only | `frontend/src/utils/profileFile.ts:685` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |

### `ProviderCredentialRefreshMaterial`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `description` | yes | yes | document only | `frontend/src/utils/profileFile.ts:691` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |
| `name` | yes | yes | document only | `frontend/src/utils/profileFile.ts:688` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |
| `required` | yes | yes | document only | `frontend/src/utils/profileFile.ts:694` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |
| `secret` | yes | yes | document only | `frontend/src/utils/profileFile.ts:695` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |

### `ProviderCredentialRefreshOutput`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `credential` | yes | yes | document only | `frontend/src/utils/profileFile.ts:710` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |
| `output` | yes | yes | document only | `frontend/src/utils/profileFile.ts:709` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |

### `ProviderCredentialTokenGrant`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `audience` | yes | yes | document only | `frontend/src/utils/profileFile.ts:744` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |
| `audience_overrides` | yes | yes | document only | `frontend/src/utils/profileFile.ts:750` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |
| `cache_ttl` | yes | yes | document only | `frontend/src/utils/profileFile.ts:768` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |
| `client_assertion_type` | yes | yes | document only | `frontend/src/utils/profileFile.ts:746` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |
| `grant_type` | yes | yes | document only | `frontend/src/utils/profileFile.ts:735` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |
| `jwt_svid_audience` | yes | yes | document only | `frontend/src/utils/profileFile.ts:745` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |
| `requested_token_type` | yes | yes | document only | `frontend/src/utils/profileFile.ts:748` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |
| `scopes` | yes | yes | document only | `frontend/src/utils/profileFile.ts:747` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |
| `subject_token` | yes | yes | document only | `frontend/src/utils/profileFile.ts:772` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |
| `token_endpoint` | yes | yes | document only | `frontend/src/utils/profileFile.ts:742` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |

### `ProviderCredentialTokenGrantAudienceOverride`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `audience` | yes | yes | document only | `frontend/src/utils/profileFile.ts:754` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |
| `host` | yes | yes | document only | `frontend/src/utils/profileFile.ts:756` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |
| `path` | yes | yes | document only | `frontend/src/utils/profileFile.ts:758` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |
| `port` | yes | yes | document only | `frontend/src/utils/profileFile.ts:757` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |
| `scopes` | yes | yes | document only | `frontend/src/utils/profileFile.ts:759` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |

### `ProviderCredentialTokenGrantSubjectToken`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `credential` | yes | yes | document only | `frontend/src/utils/profileFile.ts:775` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |
| `source` | yes | yes | document only | `frontend/src/utils/profileFile.ts:774` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |
| `subject_token_type` | yes | yes | document only | `frontend/src/utils/profileFile.ts:777` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |

### `ProviderProfile`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `annotations` | yes | yes | document only | `frontend/src/utils/profileFile.ts:889` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |
| `binaries` | yes | yes | document only | `frontend/src/utils/profileFile.ts:847` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. A profile without binaries contributes a network rule no program is named in. |
| `category` | yes | yes | form field | `frontend/src/components/CreateProfileModal.tsx:182` | All seven categories of the enum are offered. |
| `credentials` | yes | yes | form field | `frontend/src/components/CreateProfileModal.tsx:209` | The form sets a credential's name, env vars and required flag only; see ProviderProfileCredential. |
| `description` | yes | yes | form field | `frontend/src/components/CreateProfileModal.tsx:173` |  |
| `discovery` | yes | yes | document only | `frontend/src/utils/profileFile.ts:913` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |
| `display_name` | yes | yes | form field | `frontend/src/components/CreateProfileModal.tsx:160` |  |
| `endpoints` | no | yes | form field | `frontend/src/components/CreateProfileModal.tsx:299` | BROKEN: the form sets host and port only. Protocol, access, rules, TLS, enforcement and allow_uninspected_credentials are file-only, and without one of them the gateway refuses any profile that also declares a credential. The SDK type drops all but host, port and protocol. |
| `files` | yes | yes | document only | `frontend/src/utils/profileFile.ts:828` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. Experimental upstream. |
| `id` | yes | yes | form field | `frontend/src/components/CreateProfileModal.tsx:142` |  |
| `inference_capable` | yes | yes | form field | `frontend/src/components/CreateProfileModal.tsx:200` |  |
| `resource_version` | yes | yes | plumbing | `frontend/src/utils/profileFile.ts:884` | Written by the export and read back for the update's concurrency check. |
| `scope` | yes | yes | read-only | `frontend/src/components/provider/ProfilesPanel.tsx:303` | Set by the gateway; ignored when written. |
| `source` | yes | yes | read-only | `frontend/src/components/provider/ProfilesPanel.tsx:303` | Set by the gateway; ignored when written. |

### `ProviderProfileCredential`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `auth_style` | yes | yes | document only | `frontend/src/utils/profileFile.ts:803` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. Without it the proxy puts the secret in the Authorization header only, so a service that reads x-api-key, a query parameter or a path segment never gets it. |
| `description` | yes | yes | document only | `frontend/src/utils/profileFile.ts:800` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |
| `env_vars` | yes | yes | form field | `frontend/src/components/CreateProfileModal.tsx:233` | Comma-separated in one input. |
| `header_name` | yes | yes | document only | `frontend/src/utils/profileFile.ts:804` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. Required by the gateway for auth_style bearer and header. |
| `name` | yes | yes | form field | `frontend/src/components/CreateProfileModal.tsx:217` |  |
| `path_template` | yes | yes | document only | `frontend/src/utils/profileFile.ts:806` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. Required for auth_style path. |
| `query_param` | yes | yes | document only | `frontend/src/utils/profileFile.ts:805` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. Required for auth_style query. |
| `refresh` | yes | yes | document only | `frontend/src/utils/profileFile.ts:673` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. A provider's refresh can be configured afterwards on the provider page, for the strategies the profile declares. |
| `required` | yes | yes | form field | `frontend/src/components/CreateProfileModal.tsx:249` |  |
| `stable_placeholder` | yes | yes | document only | `frontend/src/utils/profileFile.ts:802` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |
| `token_grant` | yes | yes | document only | `frontend/src/utils/profileFile.ts:731` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |
| `token_grant_owners` | yes | yes | read-only | `backend/pkg/models/provider_profile.go:403` | Output only; the gateway derives it. |

### `ProviderProfileDiscovery`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `credentials` | yes | yes | document only | `frontend/src/utils/profileFile.ts:913` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |

### `ProviderProfileFile`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `content` | yes | yes | document only | `frontend/src/utils/profileFile.ts:836` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |
| `env_var` | yes | yes | document only | `frontend/src/utils/profileFile.ts:838` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |
| `path` | yes | yes | document only | `frontend/src/utils/profileFile.ts:835` | File import only (YAML or JSON chosen from disk); the Create profile form has no input for it. |

### `ProviderProfileImportItem`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `profile` | yes | yes | form field | `frontend/src/components/CreateProfileModal.tsx:105` |  |
| `source` | yes | yes | **not offered** | `backend/pkg/models/provider_profile.go:335` | The label the gateway repeats in its diagnostics (the CLI sends the file path). The BFF takes importSource; the UI never sends it, so diagnostics for several files cannot name the file. |

### `UpdateProviderProfilesRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `expected_resource_version` | yes | yes | plumbing | `frontend/src/components/provider/ProfileFileModal.tsx:216` | Taken from the file's resource_version, which the export writes. |
| `id` | yes | yes | plumbing | `frontend/src/components/provider/ProfileFileModal.tsx:216` | The profile the file is chosen for. |
| `profile` | yes | yes | document only | `frontend/src/components/provider/ProfileFileModal.tsx:216` |  |
| `request_id` | no | no | plumbing | `backend/pkg/clients/rawprofile.go:91` | Idempotency key. Never sent: no SDK wrapper sets it, so the gateway cannot recognise a retried request. |
| `workspace_scope` | yes | yes | plumbing | `backend/pkg/clients/rawprofile.go:43` | The workspace of the route, or none for a platform profile. |

## Policy, settings and proposals

The `NetworkEndpoint` rows describe the sandbox policy editors: the rule view (add an endpoint, add a rule, remove) and the document editor.

| RPC | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `GetSandboxConfig` | Config().GetSandbox | GET /api/v1/workspaces/{workspace}/sandboxes/{name}/settings; GET /api/v1/workspaces/{workspace}/sandboxes/{name}/policy/effective | read-only | `frontend/src/components/sandbox/SandboxSettingsTab.tsx:101` |  |
| `UpdateConfig` | Config().Update | PUT /api/v1/workspaces/{workspace}/sandboxes/{name}/policy; POST /api/v1/workspaces/{workspace}/sandboxes/{name}/policy/merge; PUT, DELETE /api/v1/workspaces/{workspace}/sandboxes/{name}/settings; PUT, DELETE /api/v1/global-policy; PUT, DELETE /api/v1/settings/global | form field | `frontend/src/components/PolicyRuleEditor/index.tsx:118` | Network rules go by form as merge operations, settings by form, whole policies (sandbox and global) by document only; annotations and request_id are never sent. |
| `GetGatewayConfig` | Config().GetGateway | GET /api/v1/settings/global | read-only | `frontend/src/pages/SettingsPage.tsx:70` |  |
| `ListSandboxPolicies` | Policy().ListAll | GET /api/v1/global-policy | read-only | `frontend/src/pages/GlobalPolicyPage.tsx:62` | Called for the global scope only; a sandbox's history is read with one GetSandboxPolicyStatus per version instead. |
| `GetSandboxPolicyStatus` | Policy().GetStatus | GET /api/v1/workspaces/{workspace}/sandboxes/{name}/policy; GET /api/v1/workspaces/{workspace}/sandboxes/{name}/policy/revisions/{version}; GET /api/v1/global-policy; GET /api/v1/global-policy/revisions/{version} | read-only | `frontend/src/components/PolicyRuleEditor/index.tsx:74` | The sandbox revisions/{version} route has no UI caller (getSandboxPolicyRevision in api/policy.ts is unused). |
| `GetDraftPolicy` | Policy().GetDraft | GET /api/v1/workspaces/{workspace}/sandboxes/{name}/drafts; also behind GET /api/v1/draft-summary and GET /api/v1/workspaces/{workspace}/draft-summary | read-only | `frontend/src/components/SandboxDraftsTab/index.tsx:57` |  |
| `ApproveDraftChunk` | Policy().ApproveDraftChunk | POST /api/v1/workspaces/{workspace}/sandboxes/{name}/drafts/{chunk}/approve | action | `frontend/src/components/SandboxDraftsTab/DraftChunkRow.tsx:156` |  |
| `RejectDraftChunk` | Policy().RejectDraftChunk | POST /api/v1/workspaces/{workspace}/sandboxes/{name}/drafts/{chunk}/reject | form field | `frontend/src/components/SandboxDraftsTab/DraftChunkRow.tsx:229` |  |
| `ApproveAllDraftChunks` | Policy().ApproveAllDraftChunks | POST /api/v1/workspaces/{workspace}/sandboxes/{name}/drafts/approve-all | form field | `frontend/src/components/SandboxDraftsTab/index.tsx:302` |  |
| `EditDraftChunk` | Policy().EditDraftChunk | PUT /api/v1/workspaces/{workspace}/sandboxes/{name}/drafts/{chunk} | document only | `frontend/src/components/SandboxDraftsTab/EditDraftModal.tsx:104` | The proposed rule is edited as protojson in a text area; no form and no YAML. |
| `UndoDraftChunk` | Policy().UndoDraftChunk | POST /api/v1/workspaces/{workspace}/sandboxes/{name}/drafts/{chunk}/undo | action | `frontend/src/components/SandboxDraftsTab/DraftChunkRow.tsx:185` |  |
| `ClearDraftChunks` | Policy().ClearDraftChunks | POST /api/v1/workspaces/{workspace}/sandboxes/{name}/drafts/clear | action | `frontend/src/components/SandboxDraftsTab/index.tsx:320` |  |
| `GetDraftHistory` | Policy().GetDraftHistory | GET /api/v1/workspaces/{workspace}/sandboxes/{name}/drafts/history | read-only | `frontend/src/components/SandboxDraftsTab/DraftHistorySection.tsx:26` |  |

### `AddAllowRules`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `rules` | yes | yes | form field | `frontend/src/components/PolicyRuleEditor/utils.ts:292` | One method+path rule per submit. |
| `target` | yes | yes | plumbing | `frontend/src/components/PolicyRuleEditor/utils.ts:289` | Built from the endpoint row the rule is added to. |

### `AddDenyRules`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `deny_rules` | yes | yes | form field | `frontend/src/components/PolicyRuleEditor/utils.ts:293` | One method+path rule per submit. |
| `target` | yes | yes | plumbing | `frontend/src/components/PolicyRuleEditor/utils.ts:289` | Built from the endpoint row the rule is added to. |

### `AddNetworkRule`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `rule` | yes | yes | form field | `frontend/src/components/PolicyRuleEditor/utils.ts:146` | Always one endpoint, with the fields the add-endpoint form has. |
| `rule_name` | yes | yes | form field | `frontend/src/components/PolicyRuleEditor/AddEndpointModal.tsx:90` | Empty means the CLI-style generated name allow_<host>_<port>. |

### `ApproveAllDraftChunksRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `approvals` | yes | yes | plumbing | `frontend/src/components/SandboxDraftsTab/index.tsx:303` | Every pending chunk on screen with its review token; the BFF reads them itself when the browser sends none. |
| `include_security_flagged` | yes | yes | form field | `frontend/src/components/SandboxDraftsTab/index.tsx:332` |  |
| `request_id` | no | no | plumbing | `backend/pkg/handlers/drafts_handler.go:421` | Idempotency key. Never sent: no SDK wrapper sets it, so the gateway cannot recognise a retried request. |
| `sandbox` | yes | yes | plumbing | `backend/pkg/handlers/drafts_handler.go:421` | From the route. |
| `workspace_scope` | yes | yes | plumbing | `backend/pkg/handlers/drafts_handler.go:421` | From the route. |

### `ApproveDraftChunkRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `chunk_id` | yes | yes | action | `frontend/src/components/SandboxDraftsTab/DraftChunkRow.tsx:157` | The row whose Approve button is pressed. |
| `request_id` | no | no | plumbing | `backend/pkg/handlers/drafts_handler.go:318` | Idempotency key. Never sent: no SDK wrapper sets it, so the gateway cannot recognise a retried request. |
| `review_token` | yes | yes | plumbing | `frontend/src/components/SandboxDraftsTab/DraftChunkRow.tsx:158` | Sent from the chunk as last read; the BFF resolves it when absent. |
| `sandbox` | yes | yes | plumbing | `backend/pkg/handlers/drafts_handler.go:318` | From the route. |
| `workspace_scope` | yes | yes | plumbing | `backend/pkg/handlers/drafts_handler.go:318` | From the route. |

### `ClearDraftChunksRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `request_id` | no | no | plumbing | `backend/pkg/handlers/drafts_handler.go:499` | Idempotency key. Never sent: no SDK wrapper sets it, so the gateway cannot recognise a retried request. |
| `sandbox` | yes | yes | plumbing | `backend/pkg/handlers/drafts_handler.go:499` | From the route. |
| `workspace_scope` | yes | yes | plumbing | `backend/pkg/handlers/drafts_handler.go:499` | From the route. |

### `EditDraftChunkRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `chunk_id` | yes | yes | action | `frontend/src/components/SandboxDraftsTab/DraftChunkRow.tsx:173` | The row whose Edit button is pressed (pending chunks only). |
| `proposed_rule` | yes | yes | document only | `frontend/src/components/SandboxDraftsTab/EditDraftModal.tsx:104` | JSON text area holding the whole rule as protojson; no YAML and no form. |
| `request_id` | no | no | plumbing | `backend/pkg/handlers/drafts_handler.go:476` | Idempotency key. Never sent: no SDK wrapper sets it, so the gateway cannot recognise a retried request. |
| `sandbox` | yes | yes | plumbing | `backend/pkg/handlers/drafts_handler.go:476` | From the route. |
| `workspace_scope` | yes | yes | plumbing | `backend/pkg/handlers/drafts_handler.go:476` | From the route. |

### `FilesystemPolicy`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `include_workdir` | yes | yes | document only | `frontend/src/utils/policyFile.ts:1114` | Create-time only; the three create presets all send true and the gateway refuses a change on a live sandbox. |
| `read_only` | yes | yes | document only | `frontend/src/utils/policyFile.ts:1115` | Create presets all send the same list; on a live sandbox the gateway accepts additions only. |
| `read_write` | yes | yes | document only | `frontend/src/utils/policyFile.ts:1116` | Create presets all send the same list; on a live sandbox the gateway accepts additions only. |

### `GetDraftHistoryRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `sandbox` | yes | yes | plumbing | `backend/pkg/handlers/drafts_handler.go:512` | From the route. |
| `workspace_scope` | yes | yes | plumbing | `backend/pkg/handlers/drafts_handler.go:512` | From the route. |

### `GetDraftPolicyRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `sandbox` | yes | yes | plumbing | `backend/pkg/handlers/drafts_handler.go:285` | From the route. |
| `status_filter` | yes | yes | form field | `frontend/src/components/SandboxDraftsTab/index.tsx:265` | Dropdown: all, pending, approved, rejected. |
| `workspace_scope` | yes | yes | plumbing | `backend/pkg/handlers/drafts_handler.go:285` | From the route. |

### `GetSandboxConfigRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `name` | yes | yes | plumbing | `backend/pkg/handlers/settings_handler.go:107` | From the route. |
| `workspace_scope` | yes | yes | plumbing | `backend/pkg/handlers/settings_handler.go:107` | From the route. |

### `GetSandboxPolicyStatusRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `global` | yes | yes | plumbing | `backend/pkg/handlers/policies_handler.go:225` | True on the /global-policy routes, false on the sandbox routes. |
| `sandbox` | yes | yes | plumbing | `backend/pkg/handlers/policies_handler.go:65` | From the route; omitted for the global scope. |
| `version` | yes | yes | action | `frontend/src/pages/GlobalPolicyPage.tsx:45` | Global: the revision row that is opened; sandbox: the BFF reads every version itself and the per-version route has no UI caller. |
| `workspace_scope` | yes | yes | plumbing | `backend/pkg/handlers/policies_handler.go:65` | From the route; omitted for the global scope. |

### `GraphqlOperation`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `fields` | yes | yes | document only | `frontend/src/utils/policyFile.ts:767` |  |
| `operation_name` | yes | yes | document only | `frontend/src/utils/policyFile.ts:766` |  |
| `operation_type` | yes | yes | document only | `frontend/src/utils/policyFile.ts:765` |  |

### `L7Allow`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `command` | yes | yes | document only | `frontend/src/utils/policyFile.ts:750` |  |
| `fields` | yes | yes | document only | `frontend/src/utils/policyFile.ts:754` |  |
| `method` | yes | yes | form field | `frontend/src/components/PolicyRuleEditor/AddL7RuleModal.tsx:115` | Add request rule (allow) on REST and WebSocket endpoints, sent as addAllowRules/addDenyRules; JSON-RPC and MCP methods need the document. |
| `operation_name` | yes | yes | document only | `frontend/src/utils/policyFile.ts:753` |  |
| `operation_type` | yes | yes | document only | `frontend/src/utils/policyFile.ts:752` |  |
| `params` | yes | yes | document only | `frontend/src/utils/policyFile.ts:755` | YAML also takes the MCP `tool` shorthand and nested params. |
| `path` | yes | yes | form field | `frontend/src/components/PolicyRuleEditor/AddL7RuleModal.tsx:133` | Form on REST and WebSocket endpoints only. |
| `query` | yes | yes | document only | `frontend/src/utils/policyFile.ts:751` |  |

### `L7DenyRule`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `command` | yes | yes | document only | `frontend/src/utils/policyFile.ts:750` |  |
| `fields` | yes | yes | document only | `frontend/src/utils/policyFile.ts:754` |  |
| `method` | yes | yes | form field | `frontend/src/components/PolicyRuleEditor/AddL7RuleModal.tsx:115` | Add request rule (deny) on REST and WebSocket endpoints, sent as addAllowRules/addDenyRules; JSON-RPC and MCP methods need the document. |
| `operation_name` | yes | yes | document only | `frontend/src/utils/policyFile.ts:753` |  |
| `operation_type` | yes | yes | document only | `frontend/src/utils/policyFile.ts:752` |  |
| `params` | yes | yes | document only | `frontend/src/utils/policyFile.ts:755` | YAML also takes the MCP `tool` shorthand and nested params. |
| `path` | yes | yes | form field | `frontend/src/components/PolicyRuleEditor/AddL7RuleModal.tsx:133` | Form on REST and WebSocket endpoints only. |
| `query` | yes | yes | document only | `frontend/src/utils/policyFile.ts:751` |  |

### `L7QueryMatcher`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `any` | yes | yes | document only | `frontend/src/utils/policyFile.ts:678` |  |
| `glob` | yes | yes | document only | `frontend/src/utils/policyFile.ts:674` |  |

### `L7Rule`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `allow` | yes | yes | form field | `frontend/src/components/PolicyRuleEditor/AddL7RuleModal.tsx:86` | The form fills method and path only; every other matcher is document only. |

### `L7RuleTarget`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `any_binary` | yes | yes | plumbing | `frontend/src/components/PolicyRuleEditor/utils.ts:273` | Declared by the app from the endpoint and rule the request rule is added to. |
| `binaries` | yes | yes | plumbing | `frontend/src/components/PolicyRuleEditor/utils.ts:271` | Declared by the app from the endpoint and rule the request rule is added to. |
| `host` | yes | yes | plumbing | `frontend/src/components/PolicyRuleEditor/utils.ts:264` | Declared by the app from the endpoint and rule the request rule is added to. |
| `path` | yes | yes | plumbing | `frontend/src/components/PolicyRuleEditor/utils.ts:268` | Always sent, empty for an endpoint without a path scope. |
| `ports` | yes | yes | plumbing | `frontend/src/components/PolicyRuleEditor/utils.ts:265` | Declared by the app from the endpoint and rule the request rule is added to. |
| `rule_name` | yes | yes | plumbing | `frontend/src/components/PolicyRuleEditor/utils.ts:263` | Declared by the app from the endpoint and rule the request rule is added to. |

### `LandlockPolicy`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `compatibility` | yes | yes | document only | `frontend/src/utils/policyFile.ts:1123` | Create-time only; presets all send best_effort and the gateway refuses a change on a live sandbox. |

### `ListSandboxPoliciesRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `global` | yes | fixed | fixed value | `backend/pkg/handlers/policies_handler.go:241` | Always true: the BFF lists only global revisions with this RPC. |
| `page_size` | yes | no | **not offered** | `backend/pkg/handlers/policies_handler.go:241` | ListAll runs with the gateway's default page size. |
| `page_token` | yes | no | plumbing | `backend/pkg/handlers/policies_handler.go:241` | Followed inside the SDK pager by ListAll; never reaches the browser. |
| `sandbox` | yes | no | **not offered** | `backend/pkg/handlers/policies_handler.go:241` | Never sent: sandbox history comes from GetSandboxPolicyStatus per version. |
| `workspace_scope` | yes | no | **not offered** | `backend/pkg/handlers/policies_handler.go:241` | Never sent: only the global listing is called. |

### `McpOptions`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `allow_all_known_mcp_methods` | yes | yes | document only | `frontend/src/utils/policyFile.ts:786` |  |
| `strict_tool_names` | yes | yes | document only | `frontend/src/utils/policyFile.ts:785` |  |
| `versions` | yes | yes | document only | `frontend/src/utils/policyFile.ts:108` | BROKEN: the YAML reader and writer know three revisions and refuse 2026-07-28, which gateway 0.1.9 accepts; the JSON form passes it. |

### `MiddlewareEndpointSelector`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `exclude` | yes | yes | document only | `frontend/src/utils/policyFile.ts:1041` |  |
| `include` | yes | yes | document only | `frontend/src/utils/policyFile.ts:1040` |  |

### `NetworkBinary`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `path` | yes | yes | form field | `frontend/src/components/PolicyRuleEditor/AddEndpointModal.tsx:227` | Set only while adding an endpoint; a binary cannot be removed from a rule except through the document. |

### `NetworkCredentialBinding`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `provider` | yes | yes | document only | `frontend/src/utils/policyFile.ts:933` |  |

### `NetworkEndpoint`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `access` | yes | yes | form field | `frontend/src/components/PolicyRuleEditor/AddEndpointModal.tsx:180` | Set when the endpoint is added; changing it on an existing endpoint needs the document. |
| `advisor_proposed` | yes | yes | document only | `frontend/src/utils/policyFile.ts:1661` | Gateway provenance mark carried only by the JSON form; DROPPED when a policy is edited or converted as YAML, which has no field for it. |
| `allow_encoded_slash` | yes | yes | document only | `frontend/src/utils/policyFile.ts:899` |  |
| `allow_uninspected_credentials` | yes | yes | form field | `frontend/src/components/PolicyRuleEditor/AddEndpointModal.tsx:313` |  |
| `allowed_ips` | yes | yes | form field | `frontend/src/components/PolicyRuleEditor/AddEndpointModal.tsx:254` |  |
| `credential_binding` | yes | yes | document only | `frontend/src/utils/policyFile.ts:932` | Sandbox policies only: the gateway refuses it in a global policy. |
| `credential_signing` | yes | yes | document only | `frontend/src/utils/policyFile.ts:911` |  |
| `deny_rules` | yes | yes | form field | `frontend/src/components/PolicyRuleEditor/AddL7RuleModal.tsx:94` | Add request rule appends method+path deny rules to REST and WebSocket endpoints; anything else is document only. |
| `enforcement` | yes | yes | form field | `frontend/src/components/PolicyRuleEditor/AddEndpointModal.tsx:205` |  |
| `graphql_max_body_bytes` | yes | yes | document only | `frontend/src/utils/policyFile.ts:904` |  |
| `graphql_persisted_queries` | yes | yes | document only | `frontend/src/utils/policyFile.ts:901` |  |
| `host` | yes | yes | form field | `frontend/src/components/PolicyRuleEditor/AddEndpointModal.tsx:114` |  |
| `json_rpc_max_body_bytes` | yes | yes | document only | `frontend/src/utils/policyFile.ts:916` | YAML spells it json_rpc.max_body_bytes or mcp.max_body_bytes. |
| `mcp` | yes | yes | document only | `frontend/src/utils/policyFile.ts:930` | The form offers no MCP protocol at all. |
| `path` | yes | yes | document only | `frontend/src/utils/policyFile.ts:906` | No input in the add-endpoint form; the L7 append only reads it back as the target path. |
| `persisted_queries` | yes | yes | document only | `frontend/src/utils/policyFile.ts:900` |  |
| `port` | yes | yes | form field | `frontend/src/components/PolicyRuleEditor/AddEndpointModal.tsx:134` |  |
| `ports` | yes | yes | form field | `frontend/src/components/PolicyRuleEditor/utils.ts:99` | The form always sends [port], one port; several ports need the document. |
| `protocol` | yes | yes | form field | `frontend/src/components/PolicyRuleEditor/AddEndpointModal.tsx:156` | Form offers rest, websocket, sql, tcp and none; graphql, json-rpc and mcp are document only. |
| `provider_credentialed` | yes | yes | read-only | `frontend/src/utils/policyFile.ts:1660` | Gateway-derived; shown in the JSON view, and a JSON document that sets it is forwarded and then cleared by the gateway. |
| `request_body_credential_rewrite` | yes | yes | form field | `frontend/src/components/PolicyRuleEditor/AddEndpointModal.tsx:294` |  |
| `rules` | yes | yes | form field | `frontend/src/components/PolicyRuleEditor/AddL7RuleModal.tsx:86` | Add request rule appends method+path allow rules to REST and WebSocket endpoints; anything else is document only. |
| `signing_region` | yes | yes | document only | `frontend/src/utils/policyFile.ts:913` |  |
| `signing_service` | yes | yes | document only | `frontend/src/utils/policyFile.ts:912` |  |
| `tls` | yes | yes | document only | `frontend/src/utils/policyFile.ts:886` | Only skip is accepted (NETWORK_TLS_MODE_SKIP in JSON). |
| `token_grant_owner` | no | no | **not offered** | `backend/pkg/models/policyproto.go:20` | DROPPED: the SDK endpoint type has no field, so a tokenGrantOwner in a JSON document passes protojson and is discarded; the gateway derives it and ignores a written value anyway. |
| `websocket_credential_rewrite` | yes | yes | form field | `frontend/src/components/PolicyRuleEditor/AddEndpointModal.tsx:275` | BROKEN: the checkbox is enabled for REST, the default protocol, and gateways 0.1.8+ refuse it there (allowed on websocket only). |

### `NetworkMiddlewareConfig`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `config` | yes | yes | document only | `frontend/src/utils/policyFile.ts:1031` |  |
| `endpoints` | yes | yes | document only | `frontend/src/utils/policyFile.ts:1038` |  |
| `middleware` | yes | yes | document only | `frontend/src/utils/policyFile.ts:1028` |  |
| `name` | yes | yes | document only | `frontend/src/utils/policyFile.ts:1027` |  |
| `on_error` | yes | yes | document only | `frontend/src/utils/policyFile.ts:1034` |  |
| `order` | yes | yes | document only | `frontend/src/utils/policyFile.ts:1047` |  |

### `NetworkPolicyRule`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `binaries` | yes | yes | form field | `frontend/src/components/PolicyRuleEditor/AddEndpointModal.tsx:227` | Set only while adding an endpoint to a rule; removing one needs the document. |
| `endpoints` | yes | yes | form field | `frontend/src/components/PolicyRuleEditor/utils.ts:141` | One endpoint per add; existing endpoints can be removed but not edited by form. |
| `name` | yes | yes | form field | `frontend/src/components/PolicyRuleEditor/AddEndpointModal.tsx:90` | The form sends the rule key (typed or generated) as the name. |

### `PolicyMergeOperation`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `add_allow_rules` | yes | yes | form field | `frontend/src/components/PolicyRuleEditor/index.tsx:141` | REST and WebSocket endpoints only, method+path. |
| `add_deny_rules` | yes | yes | form field | `frontend/src/components/PolicyRuleEditor/index.tsx:141` | REST and WebSocket endpoints with an allow base only, method+path. |
| `add_rule` | yes | yes | form field | `frontend/src/components/PolicyRuleEditor/index.tsx:129` | Add endpoint: one endpoint, optionally with binaries, into a new or named rule. |
| `remove_binary` | yes | yes | **not offered** | `backend/pkg/models/policyproto.go:596` | The BFF and SDK carry it but no UI issues it; removing a binary means replacing the policy document. |
| `remove_endpoint` | yes | yes | action | `frontend/src/components/PolicyRuleEditor/index.tsx:386` | One operation per port after a confirmation that lists every endpoint the host:port removal touches. |
| `remove_rule` | yes | yes | action | `frontend/src/components/PolicyRuleEditor/index.tsx:382` | Trash button on the rule row, sent without confirmation. |

### `ProcessPolicy`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `run_as_group` | yes | yes | document only | `frontend/src/utils/policyFile.ts:1138` | Create-time only; presets all send sandbox and the gateway refuses a change on a live sandbox. |
| `run_as_user` | yes | yes | document only | `frontend/src/utils/policyFile.ts:1137` | Create-time only; presets all send sandbox and the gateway refuses a change on a live sandbox. |

### `RejectDraftChunkRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `chunk_id` | yes | yes | action | `frontend/src/components/SandboxDraftsTab/DraftChunkRow.tsx:243` | The row whose Reject is confirmed. |
| `reason` | yes | yes | form field | `frontend/src/components/SandboxDraftsTab/DraftChunkRow.tsx:229` |  |
| `request_id` | no | no | plumbing | `backend/pkg/handlers/drafts_handler.go:351` | Idempotency key. Never sent: no SDK wrapper sets it, so the gateway cannot recognise a retried request. |
| `sandbox` | yes | yes | plumbing | `backend/pkg/handlers/drafts_handler.go:351` | From the route. |
| `workspace_scope` | yes | yes | plumbing | `backend/pkg/handlers/drafts_handler.go:351` | From the route. |

### `RemoveNetworkBinary`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `binary_path` | yes | yes | **not offered** | `backend/pkg/models/policyproto.go:599` | No UI issues removeBinary. |
| `rule_name` | yes | yes | **not offered** | `backend/pkg/models/policyproto.go:598` | No UI issues removeBinary. |

### `RemoveNetworkEndpoint`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `host` | yes | yes | action | `frontend/src/components/PolicyRuleEditor/utils.ts:173` | From the endpoint row that is removed. |
| `port` | yes | yes | action | `frontend/src/components/PolicyRuleEditor/utils.ts:173` | One operation per port of the endpoint; an endpoint with no port cannot be removed by form. |
| `rule_name` | yes | yes | action | `frontend/src/components/PolicyRuleEditor/utils.ts:173` | From the endpoint row that is removed. |

### `RemoveNetworkRule`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `rule_name` | yes | yes | action | `frontend/src/components/PolicyRuleEditor/utils.ts:153` | From the rule row. |

### `SandboxPolicy`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `filesystem` | yes | yes | document only | `frontend/src/utils/policyFile.ts:1112` | Read-only card on the Policy tab; set only in the create or global policy document. |
| `landlock` | yes | yes | document only | `frontend/src/utils/policyFile.ts:1122` | Read-only card on the Policy tab; set only in the create or global policy document. |
| `network_middlewares` | yes | yes | document only | `frontend/src/utils/policyFile.ts:1150` | Not shown anywhere but the document view. |
| `network_policies` | yes | yes | form field | `frontend/src/components/PolicyRuleEditor/index.tsx:377` | Sandbox scope by form (merge operations) or document; global policy by document only. |
| `process` | yes | yes | document only | `frontend/src/utils/policyFile.ts:1135` | Read-only card on the Policy tab; set only in the create or global policy document. |
| `version` | yes | yes | document only | `frontend/src/utils/policyFile.ts:1098` | A YAML policy file must say version 1. |

### `SettingValue`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `bool_value` | yes | yes | form field | `frontend/src/components/SettingValueField.tsx:71` |  |
| `bytes_value` | yes | no | **not offered** | `backend/pkg/models/sdk_converters.go:867` | The BFF takes a JSON string, boolean or integer only; gateway 0.1.9 refuses bytes for every registered key. |
| `int_value` | yes | yes | form field | `frontend/src/components/SettingValueField.tsx:82` | No key registered in gateway 0.1.9 takes an integer, so the gateway refuses every value this type sends. |
| `string_value` | yes | yes | form field | `frontend/src/components/SettingValueField.tsx:82` |  |

### `UndoDraftChunkRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `chunk_id` | yes | yes | action | `frontend/src/components/SandboxDraftsTab/DraftChunkRow.tsx:185` | The approved row whose Undo is pressed. |
| `request_id` | no | no | plumbing | `backend/pkg/handlers/drafts_handler.go:486` | Idempotency key. Never sent: no SDK wrapper sets it, so the gateway cannot recognise a retried request. |
| `sandbox` | yes | yes | plumbing | `backend/pkg/handlers/drafts_handler.go:486` | From the route. |
| `workspace_scope` | yes | yes | plumbing | `backend/pkg/handlers/drafts_handler.go:486` | From the route. |

### `UpdateConfigRequest`

| Field | SDK | BFF | UI | Where | Note |
|---|---|---|---|---|---|
| `annotations` | yes | no | **not offered** | `backend/pkg/handlers/policies_handler.go:124` | The SDK carries it but no BFF body has it, so dashboard policy revisions carry no caller provenance. |
| `delete_setting` | yes | yes | action | `frontend/src/pages/SettingsPage.tsx:174` | Delete on a setting row at either scope; also how Delete global policy is sent (key "policy"). |
| `expected_resource_version` | yes | yes | plumbing | `frontend/src/components/PolicyRuleEditor/index.tsx:203` | Sent only when a sandbox policy document is replaced; merges and setting writes send none (the merge body has no field for it). |
| `global` | yes | yes | plumbing | `backend/pkg/handlers/policies_handler.go:302` | Chosen by the page: Global policy and gateway Settings send true, the sandbox tabs false. |
| `merge_operations` | yes | yes | form field | `frontend/src/components/PolicyRuleEditor/index.tsx:118` | Five of the six operations are issued; removeBinary is not. |
| `policy` | yes | yes | document only | `frontend/src/components/PolicyRuleEditor/PolicyDocumentEditor.tsx:226` | YAML or JSON document for a sandbox (Policy tab, Document) and for the global policy; no structured editor writes a whole policy. |
| `request_id` | no | no | plumbing | `backend/pkg/handlers/policies_handler.go:124` | Idempotency key. Never sent: no SDK wrapper sets it, so the gateway cannot recognise a retried request. |
| `sandbox` | yes | yes | plumbing | `backend/pkg/handlers/policies_handler.go:125` | From the route; empty for the global scope. |
| `setting_key` | yes | yes | form field | `frontend/src/pages/SettingsPage.tsx:407` | Free text at gateway scope and at sandbox scope (SandboxSettingsTab.tsx:507); the gateway accepts its four registered keys. |
| `setting_value` | yes | yes | form field | `frontend/src/pages/SettingsPage.tsx:418` | Typed string, boolean or integer; the create form also writes proposal_approval_mode this way. |
| `workspace_scope` | yes | yes | plumbing | `backend/pkg/handlers/policies_handler.go:124` | From the route; omitted for the global scope. |

Requests with nothing for a person to author: `DraftChunkApproval` (`chunk_id`, `review_token`).

<!-- api-coverage:matrix:end -->
