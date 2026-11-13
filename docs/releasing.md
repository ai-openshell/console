# Releasing

A release is started by a person. Nobody chooses its version: it is worked out from the OpenShell release the branch is built on and the release tags that exist. Merging to `main` releases nothing, whatever the pull request is titled, the move to a new OpenShell release included ([After a bump is merged](#after-a-bump-is-merged)).

## One version, three artifacts

A release `vX.Y.Z` is three things, all made by `publish.yml`:

| Artifact | Where | Made by |
|---|---|---|
| GitHub release `vX.Y.Z`, and the git tag with it | this repository | `scripts/release/cut-release.mjs` |
| Container image tags `X.Y.Z` and `X.Y` | `quay.io/ai-openshell/console` | retagging the image CI already built |
| Helm chart `openshell-dashboard`, version `X.Y.Z` | `oci://quay.io/ai-openshell/console/helm-chart` | packaging the chart at the released commit |

The chart's registry path follows the repository's name (`publish.yml` pushes to `oci://quay.io/<owner>/<repository>/helm-chart`), so the chart lands in the Quay repository `console/helm-chart/openshell-dashboard`, next to the image's `console`. It is a repository of its own, with its own visibility and permissions: a chart cannot share the image's repository, because chart version `X.Y.Z` and image tag `X.Y.Z` would be the same tag. Renaming or transferring this repository moves the chart path with it.

Earlier homes, which receive no new release: images up to `0.1.3` are at `quay.io/gkrumbach07/openshell-dashboard`, and charts up to `0.1.2` at `oci://ghcr.io/gkrumbach07/openshell-dashboard/helm-chart`. Between the move to `ai-openshell/console` and the move to Quay, CI pushed development images to `ghcr.io/ai-openshell/console`, which was never made public.

## Who can write to quay.io

No registry secret is stored anywhere, and the workflow's own token cannot write any registry (no job asks for `packages: write`). A job that publishes runs `scripts/quay-login.sh`, which asks GitHub for the job's OIDC token and trades it at Quay's robot federation endpoint (`https://quay.io/oauth2/federation/robot/token`) for a robot token that lasts one hour. Quay gives a robot's token only to a GitHub token whose issuer and subject the robot trusts, compared exactly, and each robot trusts one GitHub environment:

| Environment | Who may use it | Robot, and the subject it trusts | It can write | Used by |
|---|---|---|---|---|
| `release` | `main` and `release/**` only | `ai-openshell+console_release`, `repo:ai-openshell/console:environment:release` | `console` and `console/helm-chart/openshell-dashboard` | `ci.yml` on a push (`build`, `push-manifest`, `promote-latest`, `publish-dev-chart`) and `publish.yml` (`tag-image`, `publish-chart`) |
| `pr-images` | a run a maintainer approved | `ai-openshell+console_dev`, `repo:ai-openshell/console:environment:pr-images` | `console-dev` only | `ci.yml`'s `pr-image` job |

The environment is what makes the subject mean something: GitHub puts `environment:release` in the token only for a job the environment admitted, so its deployment branches must stay `main` and `release/**`, and `pr-images` must keep its required reviewers. A variable `QUAY_ROBOT` on an environment overrides the robot's name.

A pull request never publishes to `console`, even one that edits a workflow: it runs with no environment, so its token's subject is `repo:ai-openshell/console:pull_request`, which no robot trusts. It does not write the build cache that `main`'s build, and so a release, reads. `image-range` reads the pushed image anonymously, so the Quay repositories must be public.

**An image of a pull request.** The `pr-image` job waits on every pull request from a branch of this repository. When a maintainer approves it (*Review deployments* on the pull request's checks), it builds `quay.io/ai-openshell/console-dev:pr-<n>` and `:sha-<7>` for linux/amd64. Each new push waits for approval again. A fork's pull request gets no image: GitHub gives it no OIDC token. The job runs on `pull_request`, never `pull_request_target`, so it cannot share `main`'s cache.

**Setting it up on quay.io** (an owner of the `ai-openshell` organization):

1. Create the public repositories `console`, `console-dev` and `console/helm-chart/openshell-dashboard`.
2. Create the robots `console_release` (write on `console` and the chart) and `console_dev` (write on `console-dev`), and nothing else: leave the organization's default permissions empty.
3. On each robot, *Set robot federation* with the issuer `https://token.actions.githubusercontent.com` and the subject in the table above.
4. Optionally, an immutability policy on `console` for the tags `^[0-9]+\.[0-9]+\.[0-9]+$` and `^sha-`, so a release or a build cannot be overwritten. `latest` and `X.Y` are meant to move.

Every action in `ci.yml`, `publish.yml` and `follow-upstream.yml` is pinned by commit SHA, because those jobs can publish.

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

Three things differ:

- **Only that line's `X.Y` image tag moves.** `latest` and the tag of `main`'s line do not.
- **The branch has to pin its own line.** `release/0.1` with a pin on `0.2` is refused.
- **The commit has to contain the newest release of its line.** A person creates `release/X.Y` from `main` before the bump to the next minor merges ([After a bump is merged](#after-a-bump-is-merged)), so for a while both are on one line. A release cut from `main` in the meantime is missing from the branch, and a release from the branch would be numbered above it without containing it. The run says so; merge that release into the branch first.

### After a bump is merged

`main` moves to a new OpenShell release by merging a bump pull request, `bump/openshell-X.Y.Z`, which the [Follow upstream](../.github/workflows/follow-upstream.yml) workflow opens ([ADR 0005](adrs/0005-gateway-compatibility-and-releases.md), amendment of 2026-11-06; [`deploy/ci/upstream/README.md`](../deploy/ci/upstream/README.md)). The merge releases nothing. It changes what the next release is built on, so cut one once CI has passed on `main`, the same way as any other.

Before merging a bump that starts a new gateway minor, create the release branch of the line `main` is leaving, if that line still needs patch releases:

```sh
git fetch origin
git push origin origin/main:refs/heads/release/0.1
```

Nothing creates it for you.

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
publish.yml   started by a person, on main or release/X.Y
   │
   ├── cut-release.mjs ── version = <pinned line>.<next patch>
   │        └── GitHub release vX.Y.Z ─────────► git tag vX.Y.Z
   │
   └── a vX.Y.Z tag is on this commit ──► tag-image ──► image :X.Y.Z, :X.Y  (same digest)
                                              └──► publish-chart ──► chart X.Y.Z
```

- **The image is built once.** `scripts/retag-image.sh` adds `latest`, `X.Y.Z` and `X.Y` later, by pointing the tag at the digest of `sha-<7>`, so a released image is byte for byte the one CI built for the commit that passed.
- **`latest` waits for everything.** `sha-<7>` is pushed when the build of a push finishes. `latest` moves after every job in `ci.yml` has passed on `main`.
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

- *The branch pins a pre-release.* Only a prep bump branch does, and it is not released; the release is cut after the bump to the stable release has merged.
- *The release branch pins another line than its own.*
- *The newest release of the line is not in this commit's history.* Either this is a second run for an old commit, and there is nothing to do, or `main` released past this release branch ([The line before `main`'s](#the-line-before-mains)).
- *A counted release tag was not built for the line* (a real run only). Delete the tag and re-run the run.

**`Cut the release` failed while creating the release.** `gh`'s message is in the log. The release and its tag are created in one request, so *Re-run all jobs* either cuts the release or finds the tag and carries on.

**The version is not the one you expected.** A counted tag that should not exist raises the number, or the pin is on the wrong line. The pipeline never deletes or moves a tag: a person does.

**`tag-image` failed after the release went out.** Use *Re-run failed jobs*. The job needs only the registry and is repeatable: it finds the `vX.Y.Z` tag on the commit and points the image tags at the same digest again.

**The whole publish workflow is re-run after a release was cut.** It cuts nothing, and `X.Y.Z` already names the released image. `X.Y` moves only when the release is the newest patch of its line, so an old release does not pull it backwards.

**`tag-image` says `X.Y.Z` already names another digest.** Unless someone pushed that tag by hand, the commit was built again after it was released (*Re-run all jobs* on its CI run does that), and `sha-<7>` now points at the new build. `X.Y.Z` still names the released image, nothing was pushed, and there is nothing to repair. To retry a failed job of an old run, use *Re-run failed jobs*, which leaves a build that passed alone.

**`tag-image` says it could not tell whether `X.Y.Z` exists.** The registry's answer was neither "here it is" nor "no such tag", so nothing was pushed. Re-run the job.

**`promote-latest` failed.** `latest` stays on the previous good commit and CI is red, so nothing is published. Re-run the job. If `main` has moved on, the re-run stands down and the newer commit's run promotes its own image.

**An older run of `main` is re-run.** It cannot move `latest`, and it releases nothing. CI runs on `main` share a concurrency group that cancels the run in progress, so the re-run cancels the newest run if that is still going, and its commit is not promoted until its own run is re-run.

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
