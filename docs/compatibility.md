# Compatibility

A dashboard release is for one minor release line of the OpenShell gateway: every gateway release that shares a major and a minor number, so `0.1.x` is every release whose version starts with `0.1`. To know whether a dashboard works with a gateway, compare the first two numbers of their versions; the patch numbers play no part ([ADR 0005](adrs/0005-gateway-compatibility-and-releases.md)).

## Which dashboard for which gateway

| Your gateway | Dashboard | Container image |
|---|---|---|
| `0.1.x` | `0.1.x` | `ghcr.io/ai-openshell/console:0.1` for the newest release of the line, or an exact `:0.1.N`. Releases up to `0.1.3` are at `quay.io/gkrumbach07/openshell-dashboard` |
| `0.0.116` | the [`gateway-0.0.116`](https://github.com/ai-openshell/console/releases/tag/gateway-0.0.116) release | `quay.io/gkrumbach07/openshell-dashboard:gateway-0.0.116` |

`gateway-0.0.116` is a one-off build outside the version numbering, because gateway `0.0.x` releases were not compatible with each other. It has no Templates support (gateway `0.0.116` has no sandbox-template RPCs), and no further release is planned for it.

Images are built for `linux/amd64` and `linux/arm64`. `latest` is the newest build of `main` that passed CI, not a release. Image tags `1.1.1`, `1.2`, `1.2.0` and `0.2.x` are older builds from before releases were numbered by gateway release line; they are not supported and are not newer than `0.1.x`.

## The compatibility notice

Another line gives no clean error. A gateway `0.0.116` answers a dashboard built for `0.1` with `workspace '\n\adefault' not found` on every workspace-scoped call, because it reads a serialized message as the workspace name. Another mismatch can drop a request field with no error.

So the BFF compares the gateway's version with its compiled-in line and gives a verdict:

| Verdict | When | The UI shows |
|---|---|---|
| supported | The gateway is on the dashboard's line: any patch release, pre-release or development build of it, and a rebuild that carries a suffix of its own, such as `0.1.2-vendor.5` | nothing |
| unsupported | Any other line, older or newer | a warning on every page that names the dashboard's line and the gateway's version |
| unknown | The gateway reports no version that can be read; a build that was given none reports `0.0.0` | nothing |

The verdict informs and blocks nothing (ADR 0005, decision 9).

## Which line a build is for

- **A release**: the first two numbers of its version. `0.1.4` is for gateway `0.1.x`.
- **A container image**: the label `io.github.ai-openshell.console.gateway.line`.
- **A running dashboard**: **Help → About**, under *Built for gateway*. It shows no dashboard version: the image is built before a release is cut.

## How it is tested

A branch is built on one OpenShell release, named in [`deploy/ci/gateway-pins.json`](../deploy/ci/gateway-pins.json). The compatibility suite in [`backend/test/compat`](../backend/test/compat) runs against that gateway on every pull request, and nothing is released from a commit where it failed. Gateway, SDK, BFF and UI are three links, each with its own proof:

| Link | Proven by |
|---|---|
| Gateway and SDK (wire) | the compatibility suite, against the real gateway |
| SDK and BFF (source) | the compiler, `go vet` and the unit tests |
| BFF and UI | both are built from one commit into one image |

Only the pinned release is run. For the rest of the line, the claim rests on OpenShell not breaking an interface it marks Stable within a minor release line (ADR 0005, decisions 6 and 10).
