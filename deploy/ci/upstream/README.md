# Following upstream

Everything `.github/workflows/follow-upstream.yml` decides, and the pins
validation `ci.yml` runs on every PR, lives here as plain Python so it can be
run and tested without a runner, a network or a container. The decision
behind it is
[ADR 0005](../../../docs/adrs/0005-gateway-compatibility-and-releases.md),
decisions 6 to 8 and the amendment of 2026-11-06.

Standard library only. Works on Python 3.9 and newer. The tests need `git`.

```sh
python3 -m unittest discover -s deploy/ci/upstream -v     # the tests
python3 deploy/ci/upstream/follow.py validate-pins        # check gateway-pins.json
python3 deploy/ci/upstream/follow.py format-pins          # rewrite it in canonical form after a hand edit
python3 deploy/ci/upstream/follow.py require-stable       # fail if it names a pre-release
python3 deploy/ci/upstream/follow.py plan --out /tmp/plan.json   # what is upstream ahead of this branch?
```

`plan` asks the real upstream (`git ls-remote`, and `docker buildx imagetools
inspect` for the target's image digests). It only reads; it starts nothing.

## One release per branch

`deploy/ci/gateway-pins.json` names the one OpenShell release a branch is
built on. The gateway and supervisor images and the Go SDK all come from that
release's tag and move together, in one commit.

| Branch | Pins | Who moves it |
|---|---|---|
| `main` | a stable release, `X.Y.Z` | the merge of a bump pull request |
| `release/X.Y` | a stable release of the line before `main`'s | a person, for critical and security fixes |
| `bump/openshell-X.Y.Z` | the release in its name; a pre-release only when a person asked for one | this workflow, once, when it creates the branch |

`ci.yml` runs the compatibility suite against the pinned gateway for every
change. Its `pins a stable release` check fails for every pull request whose
pins name a pre-release. That is what keeps a prep branch from merging before
upstream ships, and it is a check of its own so that it is not read as a
compatibility failure.

## What the workflow does

It runs once a day, and when a person starts it.

| Input | Default | What it does |
|---|---|---|
| `version` | empty | The OpenShell release to bump to, without the `v` (`0.2.0`, `0.2.0-pre.1`). Empty means the newest stable release above what `main` pins. |
| `dry_run` | ticked | Say what would be done. Nothing is pushed, opened or edited. |

1. **`plan`.** Read upstream's tags. Only `vX.Y.Z` and `vX.Y.Z-pre.N` count;
   `dev`, `vm-runtime` and anything else are ignored. The target is the
   `version` asked for, if there is one. Otherwise it is the newest stable
   release above the one `main` pins, else the newest pre-release that leads
   up to a release above it, else nothing. Several stable releases waiting are
   not walked through: the target is the newest. The target's image digests
   are resolved once, here, so the bump pins what check A tested. A scheduled
   run bumps only to a stable release; a run with a `version` bumps to that
   version, stable or pre-release.
2. **`bump`.** Make `bump/openshell-X.Y.Z`:
   - If that branch exists, or any pull request has ever come from it in this
     repository (open, closed or merged), do nothing.
   - Otherwise, take `main` and add one commit, the pin move: titled
     `fix: move to OpenShell X.Y.Z`, with the trailer
     `OpenShell-Release: X.Y.Z`, signed off, by `github-actions[bot]`.
   This job holds no write token and needs no environment, so a dry run from
   any branch previews the bump in full. The commit is handed on as a git
   bundle.
3. **`publish-bump`.** Only on `main`, and never in a dry run:
   - Check `main` out as it is now and refuse the bump unless its target is
     still ahead of what `main` pins (`follow.py check-forward`). The commit
     was made from `main` as the run found it, and a bump that `main` has
     caught up with would move it back.
   - Take the commit from the bundle, then get a token for the bump-bot App.
   - Push the new branch. The push is refused if the branch appeared in the
     meantime.
   - Open the pull request into `main`. The App opens it, so CI runs on it
     like on anyone's pull request.
4. **`check-a`.** Build the BFF from `main` as it is, start the target's
   gateway by digest, run the compatibility suite.
5. **`report`.** A check A failure goes to one issue, labelled
   `compat-migrate`, rewritten in place and closed when it passes. The issue
   says what to do: wait for upstream, or start a prep branch.

| | Dashboard | Gateway | Tells you |
|---|---|---|---|
| check A | `main`, on the SDK it ships with | the target | whether a dashboard that is installed today keeps working |
| CI on the bump pull request | the bump branch, on the target's SDK | the target | whether the move passes, and what has to change before it merges |

## What the bot does and never does

It creates a bump branch and opens its pull request. That is all.

- It never rebases, updates or force-pushes a bump branch. Once the branch
  exists it is an ordinary branch, and people push fixes to it.
- It never merges a pull request and never turns on auto-merge.
- It never cuts a release. A person runs `publish.yml`
  ([docs/releasing.md](../../../docs/releasing.md)).
- It never creates `release/X.Y`. A person does, before merging a bump that
  starts a new gateway minor.
- It pushes and opens the pull request with a token of the organization's
  bump-bot GitHub App, asked for with contents and pull requests write and
  nothing else. The App's client ID and private key are the secrets
  `BUMP_BOT_CLIENT_ID` and `BUMP_BOT_PRIVATE_KEY` in the `bump-bot`
  environment, which only `main` can deploy to.

## Working on a bump

**CI is red on the bump pull request.** Fix it on the bump branch: check it
out, commit, push. The call sites and the test doubles when `check-backend`
fails, the dashboard itself when `compat` fails. The
[proto-sync skill](../../../.claude/skills/proto-sync/SKILL.md) walks through
it.

**You need something the upcoming release has before upstream ships it** (a
new SDK method, an RPC the released gateway does not serve). Start a prep
branch: run Follow upstream with `version` set to the pre-release and
**Dry run** cleared.

```sh
gh workflow run follow-upstream.yml --ref main -f version=0.2.0-pre.1 -f dry_run=false
```

Do the work on `bump/openshell-0.2.0-pre.1`. Its pull request fails
`pins a stable release` until upstream ships the stable release; that is
intended. When it ships, the daily run opens `bump/openshell-0.2.0`.
Cherry-pick the fixes from the prep branch onto it, then close the prep pull
request.

**The bump pull request conflicts with `main`, or has fallen behind it.** The
bot will not refresh it. Rebase it yourself and push:

```sh
git fetch origin
git switch bump/openshell-X.Y.Z
git rebase origin/main
git push --force-with-lease
```

A conflict in a lock file is resolved by regenerating it: `backend/go.sum`
with `go mod tidy`, `frontend/package-lock.json` with `npm install
--package-lock-only --ignore-scripts`.

**You do not want this release.** Close the pull request. The bot will not make
that branch again, even after the branch is deleted. A newer upstream release
gets a branch of its own.

## A pin move

`pinmove.py`, and nothing else, changes:

| File | What moves |
|---|---|
| `deploy/ci/gateway-pins.json` | `release`, `gateway_image`, `supervisor_image`, `sdk` |
| `backend/go.mod`, `backend/go.sum` | the SDK, with `go get <module>@<tag's commit>` and `go mod tidy`. Never `@latest` |
| `backend/pkg/models/gateway_release_line.go` | the built-in line, only when the minor changes |
| `README.md` | the generated block, with `scripts/readme-gateway-range.mjs --write` |

The workload image and the config schema are carried over. A release that
needs a new config schema fails the compatibility suite, and a person adds
the template.

## Who may write

| Job | Permissions | Runs |
|---|---|---|
| `plan` | `contents: read` | this directory's code, `git ls-remote`, registry lookups |
| `bump` | `contents: read`, `pull-requests: read`; no secret, no environment | git, `go get`, `go mod tidy`, `go list -m`, the README script. No build, no test, no container |
| `publish-bump` | `contents: read`, plus the bump-bot App's token (contents and pull requests write), in the `bump-bot` environment | git, and `git push` and `gh` through `follow.py publish`. No toolchain, no module download |
| `check-a` | `contents: read` | upstream's gateway image, the BFF, the compat suite |
| `report` | `contents: read`, `issues: write` | this directory's code and `gh issue` |

The App's token exists only in `publish-bump`, made after the forward check
and after the commit is taken from the bundle, and is handed only to
`follow.py publish`, which runs `git push` and `gh` with the arguments the
`bump` job chose and refuses anything else. The scripts it runs come from the commit the workflow runs at,
never from the tree it is changing. `tests/test_workflows.py` and
`tests/test_publish.py` enforce this.

## Settings it depends on

| | Without it |
|---|---|
| The `bump-bot` environment, limited to `main`, with the secrets `BUMP_BOT_CLIENT_ID` and `BUMP_BOT_PRIVATE_KEY` | `publish-bump` cannot get a token and fails. Plan, check A and the report still run |
| The bump-bot App installed on this repository with Contents and Pull requests read/write | The token cannot push or open the pull request, and the run fails |

*Allow GitHub Actions to create and approve pull requests* is not needed and stays off.

The secret `UPSTREAM_BOT_TOKEN` and the variable `UPSTREAM_AUTOMERGE` are no
longer read. Delete them.

A manual run is a dry run unless its box is cleared, and a run from any branch
but `main` is always one: it prints what it would push, open and edit.

## Files

| File | What it decides |
|---|---|
| `versions.py` | Which tags are releases, how they order, which one is the target. |
| `pins.py` | Is `gateway-pins.json` well formed and in canonical form? Does it name a stable release? The move to another release. |
| `upstream.py` | What is learned from the network: tags and image digests. Tests replace it. |
| `plan.py` | The target, whether to bump to it, and what moving to it would pin, as data for the other jobs. |
| `pinmove.py` | The pin move, in a working tree. Tests replace the commands it runs. |
| `bump.py` | The bump branch's one commit: `main` plus the pin move, in a scratch worktree. |
| `publish.py` | Whether the bump branch or a pull request from it exists already, and every step to take on GitHub (no token), and running those steps (token). |
| `report.py` | The commit message, the pull request, check A's issue, the run summaries. |
| `follow.py` | The command line the workflows call. |
| `tests/` | `support.py` has the stand-in tools and a throwaway git repository; `fixtures/upstream.json` is a subset of upstream's real tags with the real ghcr.io digests of 2026-10-09. |

## Not done

- **A bump branch is never refreshed.** It is `main` as it was when the bot
  made it, plus whatever people pushed. A person rebases it.
- **A silent drop is still invisible.** The compatibility suite proves what it
  varies. A release that silently drops a request field the suite does not
  vary would pass both checks; the field-by-field diff of the request messages
  that would show it is not run automatically (ADR 0005, Consequences).
