# Keycloak realm for OpenShell Dashboard

[`openshell-realm.json`](openshell-realm.json) is a Keycloak realm import for the [install guide](../../docs/install.md). It creates the realm `openshell` with the objects below. To create them by hand instead, use the middle column.

| Object | Settings that matter | Why |
|---|---|---|
| Client `openshell-api` | Client authentication on, no flow enabled. Client roles `openshell-admin` and `openshell-user` | The audience of access tokens and the client that holds the gateway's admin role and user role. Nothing signs in through it |
| Client `console` | Client authentication on (confidential), Standard Flow, PKCE `S256`, the built-in `roles` client scope kept, Full Scope Allowed off with the two `openshell-api` roles in scope | The dashboard's auth proxy (oauth2-proxy) signs users in with it and needs a client secret. The `roles` scope carries the client-role mapper and Audience Resolve, which puts `openshell-api` into `aud` when the token holds one of its roles |
| Client `openshell-cli` | Public, Standard Flow, Device Authorization Grant, PKCE `S256`, redirect `http://127.0.0.1/*`, the same scope | For the `openshell` CLI; the dashboard does not use it |
| Groups `openshell-admins` and `openshell-users` | Mapped to the client roles `openshell-admin` and `openshell-user` | A user gets a role by joining a group. Roles can also be assigned to users directly |

No custom audience mapper or client scope is needed.

## What it leaves out

- **Users.** Create them in the realm, each with an email address and *Email verified* on.
- **Secrets.** `console` has no secret in the file, so Keycloak generates one at import.
- **Client scopes.** The file defines none, so Keycloak creates its built-in ones.

## Import

The file has been imported into Keycloak 26.8. Either of:

- In the admin console: *Create realm*, then *Resource file*, and choose the file.
- At start: put the file in `/opt/keycloak/data/import/` and start Keycloak with `--import-realm`.

Afterwards, set the redirect URIs of client `console` for your host and copy its client secret: [step 1 of the install guide](../../docs/install.md#1-set-up-keycloak).
