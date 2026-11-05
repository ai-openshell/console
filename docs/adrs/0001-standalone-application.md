# ADR 0001: A standalone application, shipped as a container image and a Helm chart

**Status:** Accepted
**Date:** 2026-10-11

## Context

OpenShell Dashboard is a web UI for one OpenShell gateway. The BFF is given one gateway address, `OPENSHELL_GATEWAY_URL`. It serves the gateway's gRPC API to the browser as REST ([ADR 0003](0003-gateway-api-openshell-sdk.md)), hosts the browser application (static files, the `/api/v1/auth/config` bootstrap, the terminal's WebSocket) and offers what an operator needs (health checks, logs, graceful shutdown, TLS to the gateway).

## Decisions

1. **Standalone is the only mode.** The dashboard is an application of its own, with its own address, navigation and sign-in. It is not embedded in another application's UI, not loaded as a remote module and not published as a library.
2. **One container image holds the whole application.** The Go BFF serves the REST API under `/api/v1` and the built React UI from the same process. The image is `quay.io/gkrumbach07/openshell-dashboard`.
3. **It is installed with the Helm chart in `deploy/helm/openshell-dashboard`,** published as an OCI chart at `oci://ghcr.io/ai-openshell/console/helm-chart/openshell-dashboard`. The chart runs the BFF with oauth2-proxy beside it in one pod ([ADR 0002](0002-auth-relay-only-bff.md)) and exposes the proxy through a Gateway API HTTPRoute or an OpenShift Route.
4. **The BFF never does the following.** It does not terminate authentication, validate tokens or authorize (ADR 0002). It does not call the Kubernetes API. It does not fetch, store or inject provider credentials: they pass through to the gateway and only their names come back. It does not rate-limit. It holds no state: no session store, no database, nothing that ties a request to a replica.
5. **Nothing is published to npm.** `frontend/package.json` is `private`, and the UI is built only as part of the image.

## Alternatives

- *Publish the pages as a library for another UI to embed.* It means a second build, a published interface that every export belongs to, peer-dependency packaging and a second way to authenticate, all kept working for a use the project does not have.
- *Develop the UI inside another project's repository.* There would be no project to install on its own.

## Consequences

- The docs describe one way to run the dashboard.
- The frontend and the BFF have no supported programming interface. Anything a module or a Go package exports is internal and may change.
- The `openshell-dashboard` package on npm is deprecated and gets no new versions.
