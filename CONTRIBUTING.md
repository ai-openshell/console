# Contributing to OpenShell Dashboard

Thank you for your interest in contributing.

## Getting started

```sh
git clone https://github.com/ai-openshell/console.git && cd console
```

Then follow [Run it locally](docs/development.md#run-it-locally): `make setup`, `make gateway` and `make dev`.

## Before you contribute

- **Read the ADRs** in [`docs/adrs/`](docs/adrs/README.md). If your change conflicts with one, open a discussion before coding.
- **Read [docs/architecture.md](docs/architecture.md)** for the components and principles.
- **Check existing issues,** and open one first for a non-trivial change.

## Contribution workflow

1. Fork the repository and branch from `main`.
2. Make your changes, and run `make lint`, `make typecheck` and `make test`.
3. Open a pull request against `main`.

### Where a pull request goes

**`main`, by default.** `main` is built on one released OpenShell gateway, and CI tests every pull request against it.

**`next`, only if it cannot build or pass on `main`.** `next` is `main` moved to upstream's newest pre-release: its gateway in CI, and its Go SDK. Work that needs something only that release has (a new SDK method, an RPC the released gateway does not serve) branches from `next` and targets it, and reaches `main` when `next` merges on the day upstream releases. A workflow rebases `next` onto `main` and force-pushes it, so rebase your branch afterwards (`git fetch origin && git rebase --fork-point origin/next`). If there is no `next`, upstream has nothing ahead of `main`.

**`release/X.Y`**, for a critical or security fix to the release line before `main`'s. Nothing else is backported ([ADR 0005](docs/adrs/0005-gateway-compatibility-and-releases.md), decision 5).

Do not move the pinned gateway or the SDK yourself ([The pinned OpenShell release](#the-pinned-openshell-release)).

### Commit conventions

Use [Conventional Commits](https://www.conventionalcommits.org/), such as `fix: handle an empty workspace list`. A feature or behavior pull request should link an accepted issue.

#### Merging does not release; the title is the release note

Merging a pull request releases nothing. A person starts a release with the `Release` workflow ([docs/releasing.md](docs/releasing.md)); the one exception is the pull request from `next`, which is released once it is merged. Nobody chooses the version, and no title changes it ([ADR 0005](docs/adrs/0005-gateway-compatibility-and-releases.md), decision 11): a `feat:` does not make a minor, and a `!` does not make a major.

The title still matters: it is the release note. With a squash merge the pull request **title** is the commit message, and a release's notes list the title of every commit since the release before, as written. Only a commit whose type or scope is `ci` (`ci: …`, `fix(ci): …`) is left out; `isCiCommit` in [`scripts/release/release-notes.mjs`](scripts/release/release-notes.mjs) decides.

- **Workflow changes use the `ci:` type.** A change to `.github/workflows/`, or to the scripts CI and the release pipeline run, alters nothing in the image.
- **A revert of a CI change is titled `ci: revert …`.** The title `git revert` writes, `Revert "…"`, has no type, so it is listed.
- **`fix:` and `feat:` are for what a user of the dashboard would notice.** Mark a breaking change with a `!` (`fix(bff)!: …`).

### The pinned OpenShell release

`deploy/ci/gateway-pins.json` names the one OpenShell release a branch is built on: the gateway and supervisor images CI runs, and the Go SDK in `backend/go.mod`. The three come from one upstream release tag and move together, in one commit ([ADR 0005](docs/adrs/0005-gateway-compatibility-and-releases.md), decision 7).

You do not move it. The [Follow upstream](.github/workflows/follow-upstream.yml) workflow finds new releases from upstream's tags and makes the move on `next`; merging `next` is how `main` gets it. A pull request into `main` or `release/**` that pins a pre-release fails the `pins a stable release` check. [`deploy/ci/upstream/README.md`](deploy/ci/upstream/README.md#a-pin-move) lists every file a pin move changes.

### Developer Certificate of Origin (DCO)

Every commit needs a `Signed-off-by` line: your statement, under the [Developer Certificate of Origin](https://developercertificate.org/), that you may submit the code under the project's license. `git commit -s` adds it:

```
Signed-off-by: Your Name <your.email@example.com>
```

## Code standards

The conventions are in [`.claude/rules/`](.claude/rules/): written for coding agents, and the reference for people too. [docs/development.md](docs/development.md#layout) shows where things live.

- **Frontend**: PatternFly 6 only. Pages are self-contained: explicit props, data through the `src/api/` hooks.
- **Backend**: one handler struct per resource, as in `func (h *SandboxHandler) ListSandboxes(w http.ResponseWriter, r *http.Request)`.
- **API**: the pinned OpenShell Go SDK is the source of truth. Do not invent RPCs, fields or lifecycle states, and never return a provider credential to the browser.

## AI-assisted contributions

AI-assisted code is welcome. You must understand every line you submit: if you cannot explain a change in review, the pull request is closed. AI-generated commit messages are fine, but must describe the change accurately. Do not submit code that fabricates an API endpoint.

## Security

Report a vulnerability as [SECURITY.md](SECURITY.md) says, not in a public issue.

## License

By contributing, you agree that your contributions will be licensed under the [Apache License 2.0](LICENSE).
