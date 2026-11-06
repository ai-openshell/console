# ADR 0005: Gateway compatibility and releases

**Status:** Accepted; amended 2026-11-06
**Date:** 2026-10-11

The [amendment of 2026-11-06](#amendment-2026-11-06-bump-branches-instead-of-next) replaces the `next` branch, the automatic release and the automatic release branches. Text it supersedes is struck through below and kept as the record.

Terms follow OpenShell's [RFC 0014](https://github.com/NVIDIA/OpenShell/tree/main/rfc/0014-release-stability): stable release, pre-release, patch and minor release, minor release line, Stable interface. A minor release line is every release that shares a major and a minor number, written `0.1` and shown as `0.1.x`.

## Context

The SDK the BFF is compiled with and the gateway image are the client half and the server half of one OpenShell tree at some commit. When the two halves do not match, the failure is not a clean error. A gateway 0.0.116 answers a dashboard built for 0.1 with `workspace '\n\adefault' not found` on every workspace-scoped call, because it reads a serialized message as a workspace name; another mismatch drops a request field without any error.

## Decisions

1. **The dashboard shares the gateway's minor release line.** Any dashboard 0.1.x works with any gateway 0.1.x. The two patch numbers are independent. To know whether a dashboard works with a gateway, compare the first two numbers of their versions. There is no table of minimum and maximum versions. Dashboard X.Y.0 means "compatible with gateway X.Y.x"; it does not mean every gateway feature is in the UI yet.
2. **A gateway minor release requires a dashboard minor release.** Dashboard X.Y.0 starts the matching line. It is not released until the compatibility suite passes against a gateway of that line, and that gateway line has no dashboard in the meantime. No release is cut from a commit whose compatibility suite failed. A gateway feature the dashboard cannot support yet is left out or marked unsupported, never shipped broken.
3. **On a gateway patch release the dashboard moves to it and releases a patch.** Dashboards already installed keep working without it. If the compatibility suite fails against the new gateway release, the dashboard stays on the last one that passed until it is fixed.
4. **The dashboard's own releases are patch releases.** Between gateway releases it publishes patch releases for fixes and features. It never starts a minor on its own.
5. **The line before the current one gets critical and security fixes, from `release/<major>.<minor>`, for as long as the gateway maintains the same line.** Nothing else is backported.
6. **A branch is built on one OpenShell release, and every change is tested against it.** `deploy/ci/gateway-pins.json` names the release: its gateway and supervisor images by digest, and the SDK at the commit of its tag, which is the version `backend/go.mod` requires. `main` and `release/<major>.<minor>` pin a stable release; ~~`next` is the only branch that pins a pre-release~~ *(superseded: see A3)*; no gateway or SDK is pinned to `dev`, `latest` or `@latest`. Each link has its own proof. Gateway and SDK: the compatibility suite in `backend/test/compat`, run against the real gateway with `-count=1`. SDK and BFF: the compiler, `go vet` and the unit tests. BFF and UI: both are built from one commit into one image. A failure is reported as the link it is.
7. **New gateway releases are picked up from the gateway's release tags, and moving to one is one commit.** The gateway images, the SDK, the release line compiled into the BFF when the minor changes, and the README's generated table move together. ~~The Follow upstream workflow makes that commit, on the `next` branch. On `main` and `next` nobody makes it by hand.~~ *(Superseded: the workflow makes it on a bump branch, A1. Nobody makes it by hand.)*
8. **Gateway pre-releases are tested before the stable release, in two ways.** Check A runs the dashboard as `main` builds it against the upcoming gateway; a failure is one issue, rewritten in place and closed when the check passes again. ~~Check B is CI on the pull request from `next`: the dashboard built on the upcoming SDK against the upcoming gateway. Work that needs the upcoming release is done on `next`, which merges into `main` when the gateway releases.~~ *(Superseded: the second test is CI on the bump pull request, and work that needs the upcoming release is done on its bump branch, A1 to A3.)*
9. **The dashboard says so when the gateway is on another line.** Three verdicts: *supported* when the gateway's major and minor number are the dashboard's line (any patch release of the line, and any pre-release, development build or rebuild of one); *unsupported* for any other line, older or newer; *unknown* when the gateway reports no version that can be read, or the BFF's own line cannot be read. The line is compiled into the BFF, so every image knows it whatever built it. The verdict informs and blocks nothing.
10. **The one requirement on OpenShell: no breaking change to a Stable interface within a minor release line,** as RFC 0014 proposes. The dashboard is tested on one release of its line and claims the whole line; the difference rests on this.
11. **The version of a release is computed, and a release is started by a person.** The version is `<line>.<n>`: the line of the pinned release, then one more than the highest patch among the release tags `v<line>.*`, or `0` when the line has none. Nobody chooses it and no commit title changes it. A person starts the release workflow on `main` or on `release/<major>.<minor>`; a dry run is the default; the commit needs a green CI run, and the image released is the one that run built, given more tags. ~~One release is automatic: the merge of `next`, recognised by where the commit came from and not by its title.~~ *(Superseded: no release is automatic, A4.)*
12. **Gateway 0.0.116 is served by one release outside the numbering, `gateway-0.0.116`.** No build works with both 0.0.116 and 0.1.x, and gateway 0.0.x releases were not compatible with each other, so a dashboard version 0.0.x would claim too much. No further release is planned for it.

## Alternatives

- *Track `latest`.* Nobody tested what `latest` was on the day.
- *Declare a minimum and a maximum gateway version per release.* The answer has to be looked up in a table, and every gateway patch newer than the maximum reads as untested although nothing is known to be wrong with it.
- *Promise the last N gateway releases unconditionally.* A wire break makes some promises impossible to keep; what works has to be found by running it.
- *Move the SDK and the gateway separately.* Both come from one OpenShell tag; moving them apart doubles the pull requests and proves combinations nobody ships.
- *Let commit titles choose the version.* Choosing a title becomes the same act as publishing.
- ~~*No automatic release at all.* The move to a new gateway release is the release most likely to be forgotten.~~ *(Chosen after all on 2026-11-06, A4.)*

## Consequences

- A reader needs two numbers, not a table.
- The dashboard claims more than it tests. If a gateway patch release breaks a Stable interface, installed dashboards break with it, and the remedy is a fix on either side.
- The compatibility suite proves only what it varies. A request field the gateway silently drops fails nothing, and the field-by-field diff of request messages that would show it is not run automatically.
- The line is written twice, in the pins file and in the BFF's source, and CI holds the two together.
- Releases batch: everything merged since the last release goes out together, and someone has to remember to cut one.
- ~~A release branch is created by the workflow and kept up to date by a person.~~ A release branch is created and kept up to date by a person (A5).
- See [`docs/releasing.md`](../releasing.md) for cutting a release and [`deploy/ci/upstream/README.md`](../../deploy/ci/upstream/README.md) for the Follow upstream workflow.

## Amendment 2026-11-06: bump branches instead of `next`

### Why

The repository moved from a personal account to the `ai-openshell` organization (`ai-openshell/console`). Three of the organization's rules break the `next` design:

- **Nobody on the project is an admin.** Maintainers have write access, and `main`'s ruleset asks for a code owner's approval given after the last push, and dismisses stale approvals. `next` was rebased and force-pushed every hour, so its approval was gone before anyone could merge it, and no one can exempt the bot.
- **The enterprise refuses fine-grained tokens that live longer than 366 days.** `UPSTREAM_BOT_TOKEN` was one. Without it the bot has only the workflow's default token, which starts no workflow when it opens a pull request and may not push a change to a workflow file, which a rebase of `next` onto `main` can carry.
- **Auto-merge and the release after the merge of `next`** depended on both of the above, and a release that starts itself is one more thing that publishes without a person in the loop.

### Decisions

- **A1. No `next`. A bump branch per upstream release.** Follow upstream runs once a day. When upstream has a stable release above the one `main` pins, it creates `bump/openshell-X.Y.Z` from `main` with one commit, the pin move (decision 7, unchanged: titled `fix: move to OpenShell X.Y.Z`, trailer `OpenShell-Release: X.Y.Z`, signed off), and opens a pull request into `main`. A person reviews and merges it like any other. It pushes and opens the pull request with a token of the organization's bump-bot GitHub App, whose secrets are in a `bump-bot` environment only `main` can use. A pull request the App opens runs CI; one opened with the workflow's default token would not, and opening one that way needs a setting the organization keeps off.
- **A2. The bot creates a bump branch once and never touches it again.** If the branch exists, or any pull request has ever come from it, the bot does nothing. It never rebases, updates or force-pushes one. People push fixes to it like any other branch. Closing its pull request means "not this release".
- **A3. A pre-release gets a bump branch only when a person asks.** Running Follow upstream with a `version` makes the bump branch for that version, a pre-release included: a prep branch for work that needs the upcoming release. Its pull request fails `pins a stable release` until upstream ships, which now runs on every pull request. When the stable release lands, the daily run opens its bump branch; the fixes are moved onto it and the prep pull request is closed. Check A is unchanged. What decision 8 called check B is CI on the bump pull request.
- **A4. Every release is started by a person.** `publish.yml` runs only when started by hand, on `main` or `release/<major>.<minor>`. Merging a bump pull request releases nothing.
- **A5. Release branches are made by hand.** Before merging a bump that starts a new gateway minor, a person creates `release/<old line>` from `main` if that line still needs patch releases.

- **A6. A tagged SDK is pinned at its tag.** From OpenShell 0.1.7 upstream tags the SDK module itself (`sdk/go/vX.Y.Z`), and `go get` of the release commit resolves to that tag. The pin is then the tag (`v0.1.7`), and the bump checks with `go list -m -json` that the tag is the release commit; before 0.1.7 it is a pseudo-version of that commit.

Decisions 1 to 5 and 9 to 12, and what decisions 6 to 8 say about the pins file, the pin move and check A, still hold.

### Consequences

- The move to a new gateway release can be forgotten: nothing releases it, and a bump pull request can sit unmerged. The daily run and check A's issue are the reminders.
- A bump branch goes stale as `main` moves, and the bot will not refresh it. A person rebases it.
- The repository needs the bump-bot App installed and the `bump-bot` environment with its secrets. *Allow GitHub Actions to create and approve pull requests* stays off.
- See [`deploy/ci/upstream/README.md`](../../deploy/ci/upstream/README.md) for the workflow and [`docs/releasing.md`](../releasing.md) for releases.
