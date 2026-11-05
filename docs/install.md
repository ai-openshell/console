# Install OpenShell Dashboard

The Helm chart installs one Deployment with two containers, the dashboard and oauth2-proxy, with a Service, a ServiceAccount, a NetworkPolicy and one route. It creates no database, no Secret and no cluster RBAC. This guide uses Keycloak; for another provider, see [Other OIDC providers](authentication.md#other-oidc-providers).

Two things here are called a gateway. "The gateway" is the OpenShell gateway, which the dashboard manages. A `Gateway` is the Gateway API resource that routes browsers to the dashboard.

## Before you start

- A Kubernetes cluster with a Gateway API controller and a Gateway, or OpenShift. The chart has no `Ingress` template.
- The Gateway's HTTPS listener admits routes from namespace `openshell` (`allowedRoutes.namespaces.from: All`, or a `Selector` that matches it), and its hostname, if it has one, covers the dashboard's host. By default a listener admits its own namespace only.
- `kubectl` and Helm 3.17.3 or 4.0.5: CI renders the chart with the first, and this guide was installed with the second.
- An OpenShell gateway of the `0.1` release line, installed from [its Helm chart](https://github.com/NVIDIA/OpenShell/tree/main/deploy/helm/openshell) as release `openshell` in namespace `openshell` ([the gateway on Kubernetes](https://github.com/NVIDIA/OpenShell/blob/main/docs/kubernetes/setup.mdx)). The dashboard goes into the same namespace: the chart mounts the gateway's CA from the Secret `openshell-client-tls` there.
- Keycloak, reachable over HTTPS at one URL from browsers, the gateway pod and the dashboard pod. The gateway accepts an HTTPS issuer only. For a local cluster, [`deploy/keycloak/dev/README.md`](../deploy/keycloak/dev/README.md) has one.
- A host name for the dashboard with a TLS certificate at the Gateway or Route: `dashboard.example.com` in the examples. Sign-in needs HTTPS, because the auth proxy's cookies are Secure.
- The browser loads one web font from Google Fonts (`fonts.googleapis.com`, `fonts.gstatic.com`). Without it the terminal uses PatternFly's monospace font.

## 1. Set up Keycloak

Import [`deploy/keycloak/openshell-realm.json`](../deploy/keycloak/openshell-realm.json) ([about the file](../deploy/keycloak/README.md)): in the admin console, *Create realm*, then *Resource file*; or start Keycloak with `--import-realm`. Then, in realm `openshell`:

1. Client `openshell-dashboard`, *Settings*: set *Valid redirect URIs* to `https://dashboard.example.com/oauth2/callback`.
2. Client `openshell-dashboard`, *Credentials*: copy the client secret.
3. Create the users: each with an email address, *Email verified* on, and membership of `openshell-admins` or `openshell-users`. A new user has *Email verified* off, and oauth2-proxy refuses the sign-in of a user whose email is not verified.

To create the objects by hand instead, use the table in the [realm file's README](../deploy/keycloak/README.md). No custom audience mapper or client scope is needed.

## 2. Check a token

In the admin console: client `openshell-dashboard`, *Client scopes*, *Evaluate*, pick a user, *Generated access token*. It must show:

```json
{ "aud": "openshell-api",
  "resource_access": { "openshell-api": { "roles": ["openshell-user"] } },
  "sub": "<the user's ID>" }
```

This is the token the gateway gets; the ID token is never forwarded.

## 3. Point the gateway at the realm

Add the OIDC settings for this realm to the values of the gateway's Helm release, and upgrade the release. This changes what tokens the gateway accepts: an `openshell` CLI signed in through another client has to use `openshell-cli` from this realm.

OpenShell gateway chart `0.1.3` or newer:

```yaml
gatewayConfig:
  openshell.gateway.oidc:
    issuer: https://keycloak.example.com/realms/openshell
    audience: openshell-api
    roles_claim: resource_access.openshell-api.roles
    admin_role: openshell-admin
    user_role: openshell-user
```

OpenShell gateway chart `0.1.0` to `0.1.2`:

```yaml
server:
  oidc:
    issuer: https://keycloak.example.com/realms/openshell
    audience: openshell-api
    rolesClaim: resource_access.openshell-api.roles
    adminRole: openshell-admin
    userRole: openshell-user
```

A `0.1.3` chart still reads these names, as deprecated aliases. OpenShell documents [the gateway's OIDC settings](https://github.com/NVIDIA/OpenShell/blob/main/docs/kubernetes/access-control.mdx) and [the old and new names of its chart's values](https://github.com/NVIDIA/OpenShell/blob/main/docs/kubernetes/migrate-gateway-config.mdx).

- The gateway chart's mutual-TLS setting can stay at its default: with OIDC on, a bearer token without a client certificate is accepted.
- For a Keycloak certificate from a private CA, both charts take a ConfigMap with the CA under the key `ca.crt`: `oidc.caConfigMapName` here, and `server.oidc.caConfigMapName` on the gateway, in either form.
- The gateway pod logs `OIDC JWT validation enabled`. A crash loop with `OIDC initialization failed` means it cannot reach or verify the issuer.

## 4. Create the Secret

```sh
kubectl -n openshell create secret generic openshell-dashboard-oidc \
  --from-literal=client-secret='<client secret of the openshell-dashboard client>' \
  --from-literal=cookie-secret="$(openssl rand -hex 16)"
```

oauth2-proxy takes a cookie secret of 16, 24 or 32 bytes. `openssl rand -hex 16` prints 32 characters, which it reads as 32 bytes.

## 5. Write values.yaml

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

- `allowedRoles` decides who reaches the UI. Without it any realm user gets in, and the gateway refuses their calls.
- `adminRole` and `userRole` must equal the gateway's admin and user roles. The UI treats a signed-in account with neither as unentitled.
- With `provider: keycloak-oidc`, signing out ends the Keycloak session too; see [Sign-out](#sign-out).
- `sectionName` is the Gateway's HTTPS listener (`kubectl -n <namespace> get gateway <name> -o jsonpath='{.spec.listeners[*].name}'` lists the names). Without it the route attaches to every listener that admits it, a plain-HTTP one included, where sign-in cannot complete.
- `networkPolicy.ingressNamespace` is where the Gateway's data-plane pods run, often not the namespace of the `Gateway` resource.

## 6. Install

```sh
helm upgrade --install openshell-dashboard \
  oci://ghcr.io/ai-openshell/console/helm-chart/openshell-dashboard \
  --version <dashboard version> --namespace openshell -f values.yaml
```

`<dashboard version>` is the newest dashboard release whose first two numbers match your gateway's: a `0.1.N` for a gateway `0.1.x` ([Compatibility](../README.md#compatibility), [releases](https://github.com/ai-openshell/console/releases)). Releases up to `0.1.2` were also published at `oci://ghcr.io/gkrumbach07/openshell-dashboard/helm-chart/openshell-dashboard`, the chart's home before the repository moved; that path receives no newer release. Then check:

```sh
kubectl -n openshell rollout status deploy/openshell-dashboard
kubectl -n openshell get httproute openshell-dashboard -o jsonpath='{.status.parents[*].conditions}'
helm -n openshell get notes openshell-dashboard
```

The second command has to show the condition `Accepted` with status `True`; otherwise the Gateway does not admit the namespace (reason `NotAllowedByListeners`), or `sectionName` is not one of its listeners (`NoMatchingParent`). The notes print the dashboard's URL, the callback to register and the gateway's address.

## 7. Sign in

Open `https://dashboard.example.com` and sign in. A user with the role `openshell-admin` sees everything. Everyone else needs a workspace membership.

## Workspace membership

A role lets a user sign in. A standard user also needs a membership in each workspace; until then the user's workspace list is empty, and a call into a workspace fails with the gateway's `not a member of workspace` message.

1. The subject is the token's `sub`: the user's ID in Keycloak. The user can copy it from the user menu, *Copy my subject ID*.
2. A platform admin opens the workspace, *Members*, *Add member*, pastes the subject and picks the role.
3. Or with the CLI: `openshell workspace member add --workspace default --subject '<sub>' --role user`.

## Sign-out

With `provider: keycloak-oidc`, signing out ends the user's Keycloak session as well as the dashboard's: oauth2-proxy calls Keycloak's logout endpoint with the session's ID token, and the next visit asks for a password. The chart derives that URL from the issuer (`oidc.backendLogoutURL`, default `auto`). Ending the Keycloak session also signs the user out of other applications that share it. To keep it, set `oidc.backendLogoutURL: ""`.

For another provider the chart cannot derive the URL. Set `oidc.backendLogoutURL` to the provider's logout URL if a request from the pod ends its session. Otherwise send the browser there: set `dashboard.logoutURL` to `/oauth2/sign_out?rd=` followed by the URL-encoded logout URL, and put the provider's host in `authProxy.whitelistDomains`. That list switches off `--skip-provider-button`; add it back with `authProxy.extraArgs`. Without either, signing out ends only the dashboard session, the next visit signs the same user in again, and the post-install notes say so.

## OpenShift

This variant is rendered from the chart. Unlike the steps above, it has not been installed on a cluster. Replace the `httpRoute` and `networkPolicy` blocks of step 5 with:

```yaml
openshiftRoute:
  enabled: true
  host: dashboard.apps.example.com
podSecurityContext: null
securityContext:
  runAsUser: null
```

- The Route terminates TLS at the edge and redirects HTTP. Its timeouts are 300s, and 1h for WebSockets.
- Set `host`. The callback is `https://<route host>/oauth2/callback`, and the chart cannot derive it from a host the router assigns. Use the same host in the client's redirect URIs.
- The two `null` values let the `restricted-v2` SCC assign the user and group.
- The NetworkPolicy admits namespace `openshift-ingress` by default. A router on the host network needs `networkPolicy.ingressFrom`.
- For reencrypt, see [`values-route-reencrypt.yaml`](../deploy/helm/openshell-dashboard/ci/values-route-reencrypt.yaml).

## Other ways to expose it

- Let the chart create the Gateway (`httpRoute.gateway.create: true`, `className`, `listener.tls.certificateRefs`). It then names the listener and sets `sectionName` itself.
- Bring your own Ingress or load balancer to Service `openshell-dashboard`, port 4180. Leave both routes off, and set `oidc.redirectURL` to `https://<host>/oauth2/callback` and `networkPolicy.ingressNamespace` to the controller's namespace.
- Try it through a port-forward, with [`values-port-forward.yaml`](../deploy/helm/openshell-dashboard/ci/values-port-forward.yaml). Add `http://localhost:4180/oauth2/callback` to the client's *Valid redirect URIs*. The browser has to keep Secure cookies on `http://localhost`.

## Upgrade and uninstall

To upgrade, run the command of step 6 with a newer `<dashboard version>` of the same release line. `dashboard.image.digest` pins the image by digest. Do not upgrade with `--reuse-values`: it keeps the defaults of the chart version you upgrade from, so a new default, such as the Keycloak sign-out, does not apply. Pass your values file again, or use `--reset-then-reuse-values`. To uninstall:

```sh
helm -n openshell uninstall openshell-dashboard
kubectl -n openshell delete secret openshell-dashboard-oidc
```

## Troubleshooting

| Symptom | Cause and fix |
| --- | --- |
| Keycloak answers `Invalid parameter: redirect_uri` | The client's *Valid redirect URIs* lack the callback `helm get notes` prints |
| 403 or 500 from the auth proxy after signing in | 403: the user has neither `openshell-api` role. 500: the user's *Email verified* is off |
| Every call fails with an `invalid token` error about `aud` | The access token's `aud` lacks the gateway's audience: repeat step 2 |
| Calls fail with `ExpiredSignature` until the next refresh | Set `authProxy.cookie.refresh` (default `2m`) below the realm's access-token lifespan |
| `not a member of workspace` | No membership yet: see [Workspace membership](#workspace-membership) |
| A sandbox delete answers 504 but completes | A delete takes about 30s, and the chart's route sets no timeout: raise the request timeout for the route with your controller's policy. Envoy Gateway defaults to 15s, and a `BackendTrafficPolicy` raises it. The auth proxy allows 120s (`authProxy.upstreamTimeout`) |
| Signed straight back in after signing out | The sign-out lines of step 5 are missing |
| The pod is Ready, but the host answers 404 | The route is not attached: see the `Accepted` condition of step 6 |
| Over `http://`, sign-in ends with 403, `Unable to find a valid CSRF token` | The route is on a plain-HTTP listener: set `sectionName` to the HTTPS one |
| The route is accepted, but requests end in 503, `connection timeout` | Set `networkPolicy.ingressNamespace` to the namespace of the Gateway's data-plane pods |
| No pod, and the ReplicaSet reports a Pod Security violation | The namespace enforces `restricted` and the chart sets no seccomp profile: set `securityContext.seccompProfile.type: RuntimeDefault` |
| A notice says the gateway is on another release line | Install the dashboard release that matches the gateway: see [the compatibility notice](compatibility.md#the-compatibility-notice) |
