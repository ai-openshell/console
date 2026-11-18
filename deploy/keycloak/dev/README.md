# A Keycloak for a local cluster

[`keycloak.yaml`](keycloak.yaml) runs Keycloak in dev mode with the realm of [`../openshell-realm.json`](../openshell-realm.json), so that the dashboard can be tried with sign-in on a throwaway cluster. It has no database and keeps nothing across a restart. It is not for production.

To run a checkout behind sign-in without a cluster, use `make dev-full` ([Run it locally](../../../docs/development.md#run-it-locally)).

Start in the repository root. The first block moves to a new temporary directory and leaves a throwaway CA and certificate there, outside the repository: `ca.crt`, `ca.key`, `ca.srl`, `san.ext`, `tls.crt`, `tls.csr` and `tls.key`. Run the later commands in the same shell: they read `ca.crt` from that directory.

## Start it

```sh
# from the repository root, into a temporary directory for the CA and the certificate
REPO=$PWD
cd "$(mktemp -d)"

kubectl create namespace keycloak

# a throwaway CA, and a server certificate for the Service name
openssl req -x509 -newkey rsa:2048 -nodes -days 30 -subj '/CN=keycloak-dev-ca' -keyout ca.key -out ca.crt
openssl req -newkey rsa:2048 -nodes -subj '/CN=keycloak.keycloak.svc.cluster.local' -keyout tls.key -out tls.csr
printf 'subjectAltName=DNS:keycloak.keycloak.svc.cluster.local\n' > san.ext
openssl x509 -req -in tls.csr -CA ca.crt -CAkey ca.key -CAcreateserial -days 30 -extfile san.ext -out tls.crt

kubectl -n keycloak create secret tls keycloak-tls --cert=tls.crt --key=tls.key
kubectl -n keycloak create secret generic keycloak-admin \
  --from-literal=username=admin --from-literal=password="$(openssl rand -hex 16)"
kubectl -n keycloak create configmap openshell-realm --from-file="$REPO/deploy/keycloak/openshell-realm.json"
kubectl -n keycloak apply -f "$REPO/deploy/keycloak/dev/keycloak.yaml"
kubectl -n keycloak rollout status deploy/keycloak
```

The issuer is `https://keycloak.keycloak.svc.cluster.local:8443/realms/openshell`, the same URL from a pod and through a port-forward.

## The client secret and a user

```sh
kc() { kubectl -n keycloak exec deploy/keycloak -- /opt/keycloak/bin/kcadm.sh "$@"; }
kubectl -n keycloak exec deploy/keycloak -- sh -c '/opt/keycloak/bin/kcadm.sh config credentials \
  --server http://localhost:8080 --realm master \
  --user "$KC_BOOTSTRAP_ADMIN_USERNAME" --password "$KC_BOOTSTRAP_ADMIN_PASSWORD"'

# the client secret, for the Secret of the install guide
ID=$(kc get clients -r openshell -q clientId=console --fields id --format csv --noquotes)
kc get "clients/$ID/client-secret" -r openshell --fields value --format csv --noquotes

# a user with an email address, Email verified on, and a group
USER_PASSWORD=$(openssl rand -hex 12)
kc create users -r openshell -s username=user1 -s enabled=true -s firstName=User -s lastName=One \
  -s email=user1@example.com -s emailVerified=true -s 'groups=["/openshell-users"]'
kc set-password -r openshell --username user1 --new-password "$USER_PASSWORD"
```

`user1` signs in with the password in `$USER_PASSWORD`. For a platform admin, create a second user in `/openshell-admins`.

## Differences from the install guide's steps

The commands above cover step 1 of the [install guide](../../../docs/install.md); the redirect URIs in the realm file are for the host `dashboard.example.com`. In the steps after it:

- The issuer, in the gateway's values and in `values.yaml`, is the URL above.
- The gateway and the dashboard both need the CA, as a ConfigMap: `kubectl -n openshell create configmap keycloak-ca --from-file=ca.crt`. Then add `--set server.oidc.caConfigMapName=keycloak-ca` to the gateway's `helm` command, and `caConfigMapName: keycloak-ca` under `oidc` in `values.yaml`.
- In the sign-out lines the Keycloak host carries its port: `keycloak.keycloak.svc.cluster.local:8443` in `whitelistDomains`, and `keycloak.keycloak.svc.cluster.local%3A8443` in `logoutURL`.

## Reaching it from a workstation

Keycloak: run `kubectl -n keycloak port-forward svc/keycloak 8443:8443` and add the line `127.0.0.1 keycloak.keycloak.svc.cluster.local` to `/etc/hosts`, so that the workstation uses the URL the pods use. Its certificate is signed by `ca.crt`.

The dashboard: through your Gateway, as in the guide, or without one. Without one, use [`values-port-forward.yaml`](../../helm/console/ci/values-port-forward.yaml) in place of the `httpRoute` and `networkPolicy` blocks, leave the sign-out lines out, add `http://localhost:4180/oauth2/callback` to the *Valid redirect URIs* of client `console`, and run `kubectl -n openshell port-forward svc/console 4180:4180`.
