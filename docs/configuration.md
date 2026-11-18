# Configuration

The dashboard is one process, the BFF, which also serves the built UI. Configure it with flags or environment variables; a flag wins over its variable.

## BFF flags and environment variables

| Flag | Variable | Default | What it sets |
|---|---|---|---|
| `-port` | `PORT` | `8080` | Listen port |
| `-listen-address` | `LISTEN_ADDRESS` | | Listen address; empty is every interface |
| `-gateway-url` | `OPENSHELL_GATEWAY_URL` | `localhost:50051` | The gateway's gRPC address. `grpcs://` or `https://` for TLS; `grpc://`, `http://` or no scheme for plaintext |
| `-gateway-ca-cert` | `GATEWAY_CA_CERT` | | CA the gateway's certificate is verified against; empty is the system trust store |
| `-gateway-client-cert` | `GATEWAY_CLIENT_CERT` | | Client certificate for the gateway |
| `-gateway-client-key` | `GATEWAY_CLIENT_KEY` | | Its private key |
| `-tls-cert` | `TLS_CERT_FILE` | | Server certificate for HTTPS |
| `-tls-key` | `TLS_KEY_FILE` | | Its private key |
| `-static-dir` | `STATIC_DIR` | | Directory of the built UI; empty serves the API only |
| `-auth-disabled` | `AUTH_DISABLED` | `false` | `true` turns sign-in off, for local development only: requests run as `dev-user` and no token goes to the gateway |
| `-auth-token-header` | `AUTH_TOKEN_HEADER` | `x-forwarded-access-token` | Header the auth proxy puts the access token in |
| `-auth-user-header` | `AUTH_USER_HEADER` | `x-auth-request-user` | Header the auth proxy puts the user name in |
| `-admin-role` | `ADMIN_ROLE` | `admin` | Role the UI shows the admin navigation for. The gateway decides access |
| `-user-role` | `USER_ROLE` | `user` | Role that counts as an entitled OpenShell user in the UI (with adminRole). Must match the gateway's user role |
| `-logout-url` | `LOGOUT_URL` | `/oauth2/sign_out` | Where the UI sends the browser on sign-out |
| `-gateway-release-line` | `GATEWAY_RELEASE_LINE` | compiled in | Overrides the gateway release line the build is for, as `major.minor`. For tests and local development ([the compatibility notice](compatibility.md#the-compatibility-notice)) |
| `-healthcheck` | | | Starts no server: requests `/api/v1/healthz` on `127.0.0.1:$PORT` and exits 0 on a `200`. The chart's probes use it |

The Helm chart sets `LISTEN_ADDRESS=127.0.0.1`, `AUTH_DISABLED=false`, `AUTH_TOKEN_HEADER=x-forwarded-access-token` and `AUTH_USER_HEADER=x-forwarded-user`, and fills the port, the gateway address and certificates, `ADMIN_ROLE`, `USER_ROLE` and `LOGOUT_URL` from its values. It cannot set any other variable; the [chart README](../deploy/helm/console/README.md) lists its values.

## Feature flags

Six variables hide features in the UI. Each is on unless set to something other than `true`.

| Variable | Hidden when off |
|---|---|
| `FEATURE_TERMINAL` | The sandbox Terminal tab and the terminal action in the sandbox list |
| `FEATURE_FILE_TRANSFER` | The sandbox Files tab |
| `FEATURE_SETTINGS` | Settings in the navigation, the sandbox Settings tab, and the proposal-approval field of the create forms |
| `FEATURE_GLOBAL_POLICY` | Global policy in the navigation |
| `FEATURE_SERVICES` | The Services tabs of a sandbox, a workspace and the all-workspaces page |
| `FEATURE_DRAFT_POLICY` | The sandbox Proposals tab and the pending proposals in the sandbox list |

Flags hide features in the UI; they are not access control. The BFF serves every route whatever they say, the Settings and Global policy pages still open by their address, and the gateway decides access. The Helm chart sets none, so a chart install has every feature on.

A seventh, `FEATURE_CREDENTIAL_REFRESH`, hides nothing: the BFF serves its value, nothing in the UI acts on it, and it is to be removed (ADR 0004, decision 4).

## TLS

The BFF has three connections, each configured on its own:

- **Browser or auth proxy to the BFF** (`TLS_CERT_FILE`, `TLS_KEY_FILE`). With both, the BFF serves HTTPS on `PORT`, TLS 1.3 or newer; with neither, plain HTTP; with only one, it does not start. The files are read at start, so a replaced certificate needs a restart.
- **BFF to the gateway** (`OPENSHELL_GATEWAY_URL`, `GATEWAY_CA_CERT`). TLS is used when the address starts with `https://` or `grpcs://`.
- **The BFF's client certificate** (`GATEWAY_CLIENT_CERT`, `GATEWAY_CLIENT_KEY`), for a gateway that asks for one. Set both or neither.

In a chart install the BFF serves plain HTTP on `127.0.0.1` inside the pod, where only oauth2-proxy reaches it. When you run the BFF without the chart, HTTPS looks like this:

```sh
TLS_CERT_FILE=/etc/tls/private/tls.crt
TLS_KEY_FILE=/etc/tls/private/tls.key
PORT=8443        # any free port
```

## Connecting to a gateway outside the chart

**A local gateway.** Installed as [OpenShell's installation page](https://github.com/NVIDIA/OpenShell/blob/main/docs/about/installation.mdx) describes, a gateway listens on `127.0.0.1`, port 17670, and serves TLS. The `openshell` CLI keeps a gateway's client certificates in `~/.config/openshell/gateways/<name>/mtls/`. For a gateway named `openshell`:

```sh
export OPENSHELL_GATEWAY_URL=https://localhost:17670
export GATEWAY_CA_CERT="$HOME/.config/openshell/gateways/openshell/mtls/ca.crt"
export GATEWAY_CLIENT_CERT="$HOME/.config/openshell/gateways/openshell/mtls/tls.crt"
export GATEWAY_CLIENT_KEY="$HOME/.config/openshell/gateways/openshell/mtls/tls.key"
```

OpenShell documents [how a client authenticates to a gateway](https://github.com/NVIDIA/OpenShell/blob/main/docs/how-it-works/gateways/authentication.mdx) and [how to create the certificates for a gateway in a container](https://github.com/NVIDIA/OpenShell/blob/main/docs/how-it-works/gateways/container-deployment.mdx#full-mtls-setup).

**A remote gateway.** Give its address with `https://`; its certificate has to be valid for the name in the address. A client certificate identifies the BFF and not the person at the browser: on a gateway that uses OIDC, every request still carries the user's token ([authentication.md](authentication.md)).
