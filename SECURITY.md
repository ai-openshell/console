# Security Policy

## Reporting a vulnerability

If you discover a security vulnerability in OpenShell Dashboard, please report it responsibly. **Do not open a public GitHub issue.**

Email **gkrumbac@redhat.com** with:

- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if you have one)

You should receive an acknowledgment within 48 hours. We will work with you to understand and address the issue before any public disclosure.

## Scope

This policy covers the OpenShell Dashboard codebase:

- **Go BFF** (`backend/`) — REST handlers, auth middleware, gRPC client
- **React frontend** (`frontend/`) — UI components and API hooks
- **Container image** — Dockerfile, build pipeline
- **CI/CD** — GitHub Actions workflows
- **Helm chart and example files** (`deploy/helm/console`, `deploy/keycloak`) — the chart is what keeps the auth proxy the only path to the BFF

For vulnerabilities in the OpenShell gateway itself, report to the [NVIDIA OpenShell project](https://github.com/NVIDIA/OpenShell) via NVIDIA PSIRT.

## Security design

The dashboard follows these security principles (see [docs/authentication.md](docs/authentication.md) and `.claude/rules/security.md`):

- **The dashboard holds no tokens and no sessions** — the auth proxy in front of it owns the session, in an HTTP-only, secure cookie, and the BFF only relays the bearer of each request to the gateway
- **Provider credentials are write-only** — never returned to the frontend
- **Secret fields** (annotated `[(openshell.options.v1.secret) = true]` in proto) are stripped by the BFF before browser serialization
- **No inline credentials** — the gateway URL, TLS file paths and proxy header names come from flags or environment variables
- **No CORS** — the UI and the API are served from one origin, and the BFF sends no cross-origin headers
- **Input validation at the BFF layer** — DNS-1123 names, policy JSON structure, bounded request bodies
