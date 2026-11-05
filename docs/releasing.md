# Releasing

A release is started by a person. Nobody chooses its version: it is worked out from the OpenShell release the branch is built on and the release tags that exist. Merging to `main` releases nothing, whatever the pull request is titled, with one exception: the merge of `next`, the move to a new OpenShell release ([The one automatic release](#the-one-automatic-release)).

## One version, three artifacts

A release `vX.Y.Z` is three things, all made by `publish.yml`:

| Artifact | Where | Made by |
|---|---|---|
| GitHub release `vX.Y.Z`, and the git tag with it | this repository | `scripts/release/cut-release.mjs` |
| Container image tags `X.Y.Z` and `X.Y` | `ghcr.io/ai-openshell/console` | retagging the image CI already built |
| Helm chart `openshell-dashboard`, version `X.Y.Z` | `oci://ghcr.io/ai-openshell/console/helm-chart` | packaging the chart at the released commit |

The chart's registry path follows the repository's name (`publish.yml` pushes to `oci://ghcr.io/<owner>/<repository>/helm-chart`), so a package created under a new name starts out private: make it public in the organization's package settings before announcing the release. Releases up to `0.1.2` are at `oci://ghcr.io/gkrumbach07/openshell-dashboard/helm-chart`, the path before the repository moved to `ai-openshell/console`; nothing is published there any more. The image moved the same way: releases up to `0.1.3` are at `quay.io/gkrumbach07/openshell-dashboard`, and CI pushes every newer image to `ghcr.io/ai-openshell/console` with the workflow's own token, so no registry secret is needed. That package also starts out private.

The BFF and the UI ship together and share the version.

## What decides the version

The gateway does. The dashboard shares the gateway's minor release line ([ADR 0005](adrs/0005-gateway-compatibility-and-releases.md)): dashboard `0.1.x` is for gateway `0.1.x`. A version has two parts, and neither is chosen:

- **The line** is the major and minor number of the OpenShell release the branch pins: `release` in [`deploy/ci/gateway-pins.json`](../deploy/ci/gateway-pins.json). The same line is compiled into the BFF, and CI holds the two together.
- **The version** is `<line>.<n>`: `n` is one more than the highest patch among the release tags `v<line>.*` that exist, or `0` when the line has none.

| The branch pins | Release tags that exist | The release is |
|---|---|---|
| `0.1.3` | none on `0.1` | `0.1.0` |
| `0.1.3` | `v0.1.0`, `v0.1.1` | `0.1.2` |
| `0.1.4` | `v0.1.0`, `v0.1.2` | `0.1.3`: a gap is not filled |
| `0.2.0` | `v0.1.0` to `v0.1.7` | `0.2.0` |
| `0.1.9`, on `release/0.1` | `v0.1.0` to `v0.1.7`, `v0.2.0` | `0.1.8` |

- **The third number is the dashboard's own.** It counts dashboard releases on the line, not gateway patches.
- **A dashboard minor starts only when the gateway's does:** the first release after the pin moves to a new gateway minor is `X.Y.0`.
- **Only release tags of the line count,** wherever they point. A tag on another line is ignored, and so is one that is not exactly `vX.Y.Z` (`v0.1.4-beta.1`, `0.1.4`).
- **Commit titles play no part.** They are what the release notes list ([CONTRIBUTING.md](../CONTRIBUTING.md#merging-does-not-release-the-title-is-the-release-note)).

[`scripts/release/next-version.mjs`](../scripts/release/next-version.mjs) is the arithmetic, and [`scripts/release/cut-release.mjs`](../scripts/release/cut-release.mjs) runs it on the pins and the tags.

**A release tag this pipeline did not cut** makes the version come out too high, because every tag `vX.Y.Z` of the line counts, whoever pushed it. A dry run warns when a counted tag's commit was not built for the line, and a real run refuses to release. Delete the tag and run again.

## Cutting a release

1. Check that CI has passed for the tip of the branch. A release tags the image that CI run built, so a commit without a green run is refused.
2. Start the workflow with **Actions → Release → Run workflow**, on `main`, and leave **Dry run** ticked.
3. Read the run's summary: the version it would cut, the release tags it counted, and the notes it would publish.
4. Start it again with **Dry run** cleared.

```sh
gh workflow run publish.yml --ref main -f dry_run=true    # look first
gh workflow run publish.yml --ref main -f dry_run=false   # then publish
```

- **Everything since the last release goes out together.**
- **A commit is released once.** Run for a commit that already carries a release tag of its line, the workflow cuts nothing and succeeds.
- **A dry run publishes nothing,** not even the image tags and the chart of a release already cut at the commit.
- **GitHub marks a release *Latest*** only when no release tag has a higher version.

### The line before `main`'s

Once `main` has moved to a new gateway minor, the line before it gets critical and security fixes from `release/<major>.<minor>`, for as long as the gateway maintains that line ([ADR 0005](adrs/0005-gateway-compatibility-and-releases.md), decision 5). The same workflow, started on that branch, gives that line its next patch:

```sh
gh workflow run publish.yml --ref release/0.1 -f dry_run=true
```

Nothing is released from a release branch automatically, and three things differ:

- **Only that line's `X.Y` image tag moves.** `latest` and the tag of `main`'s line do not.
- **The branch has to pin its own line.** `release/0.1` with a pin on `0.2` is refused.
- **The commit has to contain the newest release of its line.** The [Follow upstream](../.github/workflows/follow-upstream.yml) workflow creates `release/X.Y` from `main` before `next` merges, so for a while both are on one line. A release cut from `main` in the meantime is missing from the branch, and a release from the branch would be numbered above it without containing it. The run says so; merge that release into the branch first.

### The one automatic release

`main` moves to a new OpenShell release by merging `next`, which the [Follow upstream](../.github/workflows/follow-upstream.yml) workflow keeps one commit, the pin move, ahead of `main` ([ADR 0005](adrs/0005-gateway-compatibility-and-releases.md), decisions 7 and 8; [`deploy/ci/upstream/README.md`](../deploy/ci/upstream/README.md)). That merge changes what the next release is built on, and is the release most likely to be forgotten. So once it is merged and CI has passed on `main`, `publish.yml` releases it without being asked. Merge that pull request with *Rebase and merge*, so its commits land on `main` as they are.

Which commits qualify is decided from where they came from, never from their title ([`scripts/release/next-merge.mjs`](../scripts/release/next-merge.mjs)): the commit was merged into `main` by a pull request whose head was the branch `next` **in this repository**. A fork's branch of that name does not count, and neither does a pull request *into* `next`. Anything else is released by hand.

Its version is worked out the same way, and it carries every commit since the last release, including work people put on `next` because it needed the new gateway. It follows the merge only if CI passes for the commit `main` ends up on; if a second merge cancels that run, cut the release by hand.

## The pipeline

```
push to main, or to release/X.Y
   │
   ▼
ci.yml ── build ──► push-manifest ─────────────► image :sha-<7>
   │                     └── image-range   (reads it back: does it declare its line?)
   │
   ├── check-frontend, check-backend, e2e, helm
   ├── compat-pins, compat (the gateway this branch pins), stable-release
   ├── release-tooling
   │
   └── on main: all green, and this commit is still the tip of main
   │                 └──► promote-latest ──────► image :latest  (same digest)
   │                           └──► publish-dev-chart ──► chart 0.0.0-dev  (appVersion sha-<7>)
   │
   ▼
publish.yml   started by a person, on main or release/X.Y   (or: CI passed for the merge of next)
   │
   ├── cut-release.mjs ── version = <pinned line>.<next patch>
   │        └── GitHub release vX.Y.Z ─────────► git tag vX.Y.Z
   │
   └── a vX.Y.Z tag is on this commit ──► tag-image ──► image :X.Y.Z, :X.Y  (same digest)
                                              └──► publish-chart ──► chart X.Y.Z
```

- **The image is built once.** `scripts/retag-image.sh` adds `latest`, `X.Y.Z` and `X.Y` later, by pointing the tag at the digest of `sha-<7>`, so a released image is byte for byte the one CI built for the commit that passed.
- **`latest` waits for everything.** `sha-<7>` and `pr-<n>` are pushed when the build finishes. `latest` moves after every job in `ci.yml` has passed on `main`.
- **`latest` only moves forward, and no release moves it.** A run can be re-run for thirty days and keeps its commit, so `promote-latest` first checks that the commit is still the tip of `main` (`scripts/branch-tip.sh`), and otherwise succeeds without touching the registry.
- **A version is written once.** `retag-image.sh` refuses to point an existing `X.Y.Z` at another digest, and `publish-chart` leaves an existing chart version untouched. `X.Y`, `latest` and the development chart `0.0.0-dev` are meant to move.

## The release notes

[`scripts/release/release-notes.mjs`](../scripts/release/release-notes.mjs) writes two parts; a dry run prints them as a real run would publish them:

- **The changes**: the title of every commit since the previous release, as merged, oldest first. A commit whose type or scope is `ci` is left out. The previous release is the nearest one in the released commit's history, so the first release of a new line lists the changes since the last release of the line before. With no earlier release, a fixed sentence says so.
- **Supported OpenShell gateways**, described below.

## What every release declares

The GitHub release and the container image each state the gateway release line the release is for and the OpenShell Go SDK it was built against ([ADR 0005](adrs/0005-gateway-compatibility-and-releases.md)). The Helm chart states nothing of its own; by default it deploys the image of the same version. [`scripts/gateway-range.mjs`](../scripts/gateway-range.mjs) derives the line from the pins file at the released commit, and everything else calls that script:

| Artifact | What it carries | Written by |
|---|---|---|
| GitHub release | a *Supported OpenShell gateways* section: the line, the gateway release it was built on and tested against, the SDK, and a line that changed since the previous release | `scripts/release/release-notes.mjs` |
| Container image | labels `io.github.ai-openshell.console.gateway.line` and `.sdk` | build args in the `build` job of `ci.yml`; `image-range` fails if the pushed image lacks them |
| README | the generated table under *Compatibility* | `scripts/readme-gateway-range.mjs --write`, checked in CI |

The BFF does not read the labels. Its line is compiled in (`BuiltInGatewayReleaseLine` in [`backend/pkg/models/gateway_release_line.go`](../backend/pkg/models/gateway_release_line.go)), so the compatibility notice also works in an image built without the build args. `node scripts/gateway-range.mjs --check` fails unless that constant and the SDK version in `backend/go.mod` agree with the pins file. The SDK moves with the gateway images in one commit ([ADR 0005](adrs/0005-gateway-compatibility-and-releases.md), decision 7), and so does the constant when the pin moves to a new minor.

A running dashboard shows no version number: the image is built before a release is cut. Its About dialog shows the gateway release line and the commit, which CI passes to the image build as `DASHBOARD_COMMIT`.

## When something fails

**Retry by re-running the run, not by starting a new one.** *Re-run all jobs* and *Re-run failed jobs* keep the run's commit. A run started from *Run workflow* is for the tip of the branch: if the branch has gained a commit since a release was cut, it cuts the next patch there and leaves the earlier release without its image tags and chart.

**`Cut the release` refuses.** It says why, and nothing was created:

- *The branch pins a pre-release.* Only `next` does; the release is cut after `next` has moved to the stable release and merged.
- *The release branch pins another line than its own.*
- *The newest release of the line is not in this commit's history.* Either this is a second run for an old commit, and there is nothing to do, or `main` released past this release branch ([The line before `main`'s](#the-line-before-mains)).
- *A counted release tag was not built for the line* (a real run only). Delete the tag and re-run the run; the merge of `next` is stopped and re-run the same way.

**`Cut the release` failed while creating the release.** `gh`'s message is in the log. The release and its tag are created in one request, so *Re-run all jobs* either cuts the release or finds the tag and carries on.

**The version is not the one you expected.** A counted tag that should not exist raises the number, or the pin is on the wrong line. The pipeline never deletes or moves a tag: a person does.

**`tag-image` failed after the release went out.** Use *Re-run failed jobs*. The job needs only the registry and is repeatable: it finds the `vX.Y.Z` tag on the commit and points the image tags at the same digest again.

**The whole publish workflow is re-run after a release was cut.** It cuts nothing, and `X.Y.Z` already names the released image. `X.Y` moves only when the release is the newest patch of its line, so an old release does not pull it backwards.

**`tag-image` says `X.Y.Z` already names another digest.** Unless someone pushed that tag by hand, the commit was built again after it was released (*Re-run all jobs* on its CI run does that), and `sha-<7>` now points at the new build. `X.Y.Z` still names the released image, nothing was pushed, and there is nothing to repair. To retry a failed job of an old run, use *Re-run failed jobs*, which leaves a build that passed alone.

**`tag-image` says it could not tell whether `X.Y.Z` exists.** The registry's answer was neither "here it is" nor "no such tag", so nothing was pushed. Re-run the job.

**`promote-latest` failed.** `latest` stays on the previous good commit and CI is red, so nothing is published. Re-run the job. If `main` has moved on, the re-run stands down and the newer commit's run promotes its own image.

**An older run of `main` is re-run.** It cannot move `latest`. If its commit was merged from `next` and never released, it is released now, unless the line has a newer release that the commit does not contain; then the run is refused. CI runs on `main` share a concurrency group that cancels the run in progress, so the re-run cancels the newest run if that is still going, and its commit is neither promoted nor released until its own run is re-run.

## Testing the release tooling

This needs only Node 22: no registry, token or Docker daemon.

```sh
node scripts/gateway-range.mjs --check          # the line in the BFF and the SDK in go.mod match the pins
node scripts/readme-gateway-range.mjs --check   # the README states the pinned release
node --test "scripts/**/*.test.mjs"             # the release scripts, with stand-ins for gh and docker

# What a release of this checkout would be. It creates nothing.
git fetch --tags origin
node scripts/release/cut-release.mjs --branch main --dry-run
```

The dry run counts the tags in your clone, so first delete any `vX.Y.Z` tag that origin does not have. The `release-tooling` job in `ci.yml` runs the first three on every pull request.

## What this pipeline does not do

- **It releases from `main` and from `release/<major>.<minor>` only.** The one-off release `gateway-0.0.116` was made by hand; its tag is not a `vX.Y.Z` tag, so nothing here counts it ([Compatibility](../README.md#compatibility)).
- **It does not rebuild images for a release.** The version is decided after the image exists, so the image's `org.opencontainers.image.version` label names the branch it was built from (`main`), not `X.Y.Z`.
