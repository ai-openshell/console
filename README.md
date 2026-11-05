# OpenShell Dashboard

A web admin UI for [OpenShell](https://github.com/NVIDIA/OpenShell), the open-source agent sandboxing platform. It is a standalone application: a Go backend-for-frontend (the BFF) that reaches one OpenShell gateway through the OpenShell Go SDK, and a React UI built with PatternFly 6. It ships as a container image and a Helm chart.

## Features

- **Workspaces**: create and delete workspaces, and add and remove their members.
- **Sandboxes**: create a sandbox with a security policy or from a workspace template, then stop, start and delete it.
- **Inside a sandbox**: logs, an interactive terminal, file upload and download, exposed services, settings, and the providers attached to it.
- **Policy**: revision history, a network-rule editor and an inbox of proposed policy changes for each sandbox, and one gateway-wide policy.
- **Providers**: register credentials from provider profiles, and create, import and export the profiles.
- **Gateway**: status, version, compute drivers and settings, and lists of sandboxes, providers, templates and services across all workspaces.

## Install

The dashboard is installed with its Helm chart, in the namespace of the gateway it manages. You need:

- A Kubernetes cluster with a Gateway API controller, and a Gateway whose HTTPS listener admits routes from the namespace `openshell`. On OpenShift the chart can create a Route instead.
- An OpenShell gateway `0.1.x`, installed from its Helm chart as release `openshell` in the namespace `openshell`.
- A Keycloak realm that the gateway trusts. [`deploy/keycloak/openshell-realm.json`](deploy/keycloak/openshell-realm.json) is one you can import.

Create the Secret the auth proxy reads:

```sh
kubectl -n openshell create secret generic openshell-dashboard-oidc \
  --from-literal=client-secret='<client secret of the openshell-dashboard client>' \
  --from-literal=cookie-secret="$(openssl rand -hex 16)"
```

Write a `values.yaml`:

```yaml
oidc:
  provider: keycloak-oidc
  issuer: https://keycloak.example.com/realms/openshell
  codeChallengeMethod: S256
  adminRole: openshell-admin
  userRole: openshell-user
  allowedRoles:
    - openshell-api:openshell-admin
    - openshell-api:openshell-user

httpRoute:
  enabled: true
  hostnames: [dashboard.example.com]
  gateway:
    name: <your Gateway>
    namespace: <its namespace>
    sectionName: <name of its HTTPS listener>

networkPolicy:
  ingressNamespace: <namespace of the Gateway's data-plane pods>
```

Install the chart:

```sh
helm upgrade --install openshell-dashboard \
  oci://ghcr.io/ai-openshell/console/helm-chart/openshell-dashboard \
  --version <dashboard version> --namespace openshell -f values.yaml
```

`<dashboard version>` is the newest dashboard release whose first two numbers match your gateway's: a `0.1.N` for a gateway `0.1.x` ([Compatibility](#compatibility), [releases](https://github.com/ai-openshell/console/releases)).

Then open `https://dashboard.example.com` and sign in. The [install guide](docs/install.md) has every step: Keycloak, the gateway's settings, OpenShift, sign-out and workspace membership.

## Compatibility

A dashboard release is for one minor release line of the OpenShell gateway: every gateway release that shares a major and a minor number, so `0.1.x` is `0.1.0`, `0.1.3` and every other release whose version starts with `0.1`. To know whether a dashboard works with a gateway, compare the first two numbers of their versions; the patch numbers play no part ([ADR 0005](docs/adrs/0005-gateway-compatibility-and-releases.md)).

| Your gateway | Dashboard | Container image |
|---|---|---|
| `0.1.x` | `0.1.x` | `ghcr.io/ai-openshell/console:0.1` for the newest release of the line, or an exact `:0.1.N`. Releases up to `0.1.3` are at `quay.io/gkrumbach07/openshell-dashboard` |
| `0.0.116` | the [`gateway-0.0.116`](https://github.com/ai-openshell/console/releases/tag/gateway-0.0.116) release | `quay.io/gkrumbach07/openshell-dashboard:gateway-0.0.116` |

`gateway-0.0.116` is a one-off build for gateway `0.0.116` only. It sits outside the version numbering, and no further release is planned for it.

This branch is built on:

<!-- gateway-range:begin (generated from deploy/ci/gateway-pins.json by scripts/readme-gateway-range.mjs; do not edit) -->
| | |
|---|---|
| Supported gateways | `0.1.x` |
| Tested on | `0.1.6` |
| OpenShell Go SDK | `v0.0.0-20261027154453-ac7f615cd12d` |
<!-- gateway-range:end -->

[docs/compatibility.md](docs/compatibility.md) says what the dashboard shows when the gateway is on another line, and how the claim is tested.

## List pagination

`GET /api/v1/workspaces`, `GET /api/v1/workspaces/{workspace}/sandboxes`, and
`GET /api/v1/sandboxes` accept `pageSize` (1–100), an optional opaque
`pageToken`, and `labelSelector`. A token requires `pageSize`. Paged requests
return `{"items": [...], "nextPageToken": "..."}`; an empty token marks the last
page. Requests without `pageSize` retain the existing array response. The
gateway does not return a total count.

## Documentation

- [Install](docs/install.md): the full guide, with Keycloak, OpenShift and troubleshooting
- [Authentication](docs/authentication.md): how sign-in works and what the identity provider must issue
- [Configuration](docs/configuration.md): BFF flags and environment variables, feature flags, TLS
- [Compatibility](docs/compatibility.md): gateway release lines and the compatibility notice
- [Development](docs/development.md): running it locally, tests, the layout of the code
- [Architecture](docs/architecture.md): components, principles, roles
- [Releasing](docs/releasing.md): how a release is cut and numbered
- [Decision records](docs/adrs/README.md)
- [Helm chart](deploy/helm/openshell-dashboard/README.md): every chart value

## Development

You need Go 1.26.7 or newer, Node 22, Docker with Compose, `jq`, `openssl` and `curl`.

```sh
make setup     # install the frontend and Go dependencies
make gateway   # the OpenShell gateway this branch pins, in Docker
make dev       # the UI on http://localhost:3000 and the BFF on port 8080, without sign-in
make test      # frontend unit tests and Go tests
```

`make dev-full` runs it behind sign-in. [docs/development.md](docs/development.md) has the steps in order, the other targets and the layout of the code.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## Security

To report a vulnerability, see [SECURITY.md](SECURITY.md).

## License

[Apache License 2.0](LICENSE).
