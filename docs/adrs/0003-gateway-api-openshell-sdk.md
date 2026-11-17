# ADR 0003: Gateway API: the OpenShell Go SDK, surfaced as it is

**Status:** Accepted
**Date:** 2026-10-11

## Context

The dashboard is a client of the OpenShell gateway's gRPC API. Two things go wrong when a client drifts from its server: it grows endpoints and objects the server does not have, and it keeps a second definition of the API that falls out of date.

## Decisions

1. **The BFF reaches the gateway through the OpenShell Go SDK,** `github.com/ai-openshell/sdk-go` (until OpenShell 0.1.8, `github.com/NVIDIA/OpenShell/sdk/go`), required in `backend/go.mod` at the tag of the OpenShell release the branch is built on ([ADR 0005](0005-gateway-compatibility-and-releases.md)). The repository holds no copy of the gateway's proto files and no generated stubs.
2. **The gateway's API defines what exists.** The dashboard invents no RPC, field, lifecycle state or object. Sandbox is the object the gateway has; labels categorize sandboxes, and there is no Agent object. An idea with no RPC behind it is raised with OpenShell, not simulated in the dashboard.
3. **The BFF translates nothing.** Request and response bodies mirror the gateway's messages. When the gateway expects something in a particular form, the UI sends it in that form. The OpenShell CLI and TUI are the reference for how a client calls an RPC. Beyond that the BFF adds little: its bootstrap and health routes, the compatibility verdict (ADR 0005, decision 9) and a summary of pending proposals.
4. **A direct gRPC call is the exception, for a gap in the SDK that can be named.** It lives in `backend/pkg/clients`, uses the proto client the SDK ships, states the gap at the top of its file, and is deleted when the SDK closes the gap. There are three today: running a command with stdin and no terminal (binary-safe upload), reading the names of the credentials a provider holds, and reading and writing a provider profile's endpoints in full.
5. **Secrets never reach the browser.** The converters in `backend/pkg/models` return the names of a provider's credentials and never a value. From the UI, credentials are write-only.
6. **State is polled, and the terminal is the one WebSocket.** Lists, status and logs are fetched on an interval. The terminal relays the gateway's interactive exec stream over a WebSocket. The gateway's other streaming RPCs are not wrapped.

## Alternatives

- *Generate stubs from the gateway's proto files and wrap them.* Generated code to carry, a proto sync to run by hand, and client fixes that help only this project.
- *Adapt in the BFF* (rename a key, infer a scope, coerce a type) *so that the UI can send something other than what the gateway expects.* The BFF becomes a second definition of the API, and the UI stops matching what the CLI sends.

## Consequences

- A new feature starts by reading the SDK. If the RPC is not there, the work item is an OpenShell issue.
- The hard facts about the API are in [`.claude/rules/openshell-api.md`](../../.claude/rules/openshell-api.md).
