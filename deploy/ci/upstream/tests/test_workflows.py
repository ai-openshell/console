"""Invariants of the workflow files themselves.

The rules are stated in comments at the top of follow-upstream.yml. A comment
cannot fail a build, so the ones that can be read off the YAML are checked
here: every PR runs this file from ci.yml's compat-pins job.

There is no YAML parser in the standard library, so this reads the files by
indentation. That is enough for the handful of facts it needs, and it only
looks at what this directory owns: all of follow-upstream.yml, the triggers
and the compat jobs of ci.yml, and the triggers of publish.yml.
"""

import os
import re
import unittest

import pinmove
import report
from tests import support

WORKFLOWS = os.path.join(support.REPO_ROOT, ".github", "workflows")


def read(*parts):
    with open(os.path.join(support.REPO_ROOT, *parts), encoding="utf-8") as fh:
        return fh.read()


def jobs(text):
    """{job id: its lines} for a workflow file."""
    lines = text.splitlines()
    out, current = {}, None
    for line in lines[lines.index("jobs:") + 1 :]:
        m = re.match(r"^  ([A-Za-z0-9_-]+):\s*$", line)
        if m:
            current = m.group(1)
            out[current] = []
        elif current is not None:
            out[current].append(line)
    return out


def indent(line):
    return len(line) - len(line.lstrip())


def run_scripts(lines):
    """The shell text of every `run:` among some lines."""
    scripts, i = [], 0
    while i < len(lines):
        m = re.match(r"^(\s*)(- )?run:\s*(.*)$", lines[i])
        i += 1
        if not m:
            continue
        key_column = len(m.group(1)) + (2 if m.group(2) else 0)
        if m.group(3)[:1] in ("|", ">"):
            body = []
            while i < len(lines) and (not lines[i].strip() or indent(lines[i]) > key_column):
                body.append(lines[i])
                i += 1
            scripts.append("\n".join(body))
        else:
            scripts.append(m.group(3))
    return scripts


def steps(lines):
    """A job's steps, each as a dict of its name, text, scripts and own env.

    `env` is the text of the step's own env: block. A value handed to one step
    there is not visible to any other step, which is what the token tests
    below rely on.
    """
    start = lines.index("    steps:")
    found, current = [], None
    for line in lines[start + 1 :]:
        if line.strip() and not line.lstrip().startswith("#") and indent(line) < 6:
            break
        if re.match(r"^      - ", line):
            current = []
            found.append(current)
        if current is not None:
            current.append(line)
    out = []
    for body in found:
        text = "\n".join(body)
        name = re.search(r"^\s+(?:- )?name:\s*(.+)$", text, re.M)
        uses = re.search(r"^\s+(?:- )?uses:\s*(\S+)", text, re.M)
        env, inside = [], False
        for line in body:
            if re.match(r"^        env:\s*$", line):
                inside = True
            elif inside and line.strip() and indent(line) <= 8:
                inside = False
            elif inside:
                env.append(line)
        out.append(
            {
                "name": name.group(1).strip() if name else (uses.group(1) if uses else ""),
                "uses": uses.group(1) if uses else None,
                "lines": body,
                "text": text,
                "scripts": run_scripts(body),
                "env": "\n".join(env),
            }
        )
    return out


def job_env(lines):
    """The text of a job's own env: block (not its steps')."""
    start = lines.index("    steps:")
    env, inside = [], False
    for line in lines[:start]:
        if re.match(r"^    env:\s*$", line):
            inside = True
        elif inside and line.strip() and indent(line) <= 4:
            inside = False
        elif inside:
            env.append(line)
    return "\n".join(env)


def permissions(lines):
    """A job's permissions block as a dict; None when it declares none."""
    for i, line in enumerate(lines):
        m = re.match(r"^    permissions:\s*(.*)$", line)
        if not m:
            continue
        if m.group(1).strip() == "{}":
            return {}
        found = {}
        for entry in lines[i + 1 :]:
            if indent(entry) != 6:
                break
            key, _, value = entry.strip().partition(":")
            found[key] = value.split("#")[0].strip()
        return found
    return None


def commands(script):
    """The command lines of a script: continuation lines folded in, comments and blanks dropped."""
    folded = re.sub(r"\\\n\s*", "", script)
    return [text.strip() for text in folded.splitlines() if text.strip() and not text.strip().startswith("#")]


FOLLOW = read(".github", "workflows", "follow-upstream.yml")
CI = read(".github", "workflows", "ci.yml")
PUBLISH = read(".github", "workflows", "publish.yml")
STACK = read("deploy", "ci", "e2e-stack.sh")
FOLLOW_JOBS = jobs(FOLLOW)
CI_ALL_JOBS = jobs(CI)
CI_JOBS = {name: lines for name, lines in CI_ALL_JOBS.items() if name in ("compat-pins", "stable-release", "compat")}

TOKEN = "GH_TOKEN: ${{ steps.app-token.outputs.token }}"
APP_TOKEN_STEP = "Get a token for the bump-bot App"
PINNED = re.compile(r"^[A-Za-z0-9_.-]+/[A-Za-z0-9_./-]+@[0-9a-f]{40}$")


def action(uses):
    """The action a `uses:` names, without the ref it is pinned to."""
    return uses.split("@", 1)[0] if uses else None
LIST = "List the pull requests from the bump branch"
BUILD = "Make the bump commit"
PUBLISH_STEP = "Push the branch and open the pull request"
FORWARD = "Refuse a bump main has caught up with"
UNBUNDLE = "Take the bump commit from the bundle"


class ScannerSanity(unittest.TestCase):
    """If the scanner found nothing, every test below would pass for no reason."""

    def test_finds_the_jobs(self):
        self.assertEqual(sorted(FOLLOW_JOBS), ["bump", "check-a", "plan", "publish-bump", "report"])
        self.assertEqual(sorted(CI_JOBS), ["compat", "compat-pins", "stable-release"])

    def test_finds_the_scripts(self):
        self.assertGreaterEqual(sum(len(run_scripts(lines)) for lines in FOLLOW_JOBS.values()), 14)
        self.assertGreaterEqual(sum(len(run_scripts(lines)) for lines in CI_JOBS.values()), 8)

    def test_finds_the_steps(self):
        found = steps(FOLLOW_JOBS["bump"])
        self.assertEqual(action(found[0]["uses"]), "actions/checkout")
        self.assertIn(LIST, [step["name"] for step in found])
        self.assertIn(BUILD, [step["name"] for step in found])
        self.assertIn("BRANCH:", job_env(FOLLOW_JOBS["bump"]))
        published = steps(FOLLOW_JOBS["publish-bump"])
        for name in (FORWARD, UNBUNDLE, APP_TOKEN_STEP, PUBLISH_STEP):
            self.assertIn(name, [step["name"] for step in published])
        self.assertIn("GH_TOKEN", [s for s in published if s["name"] == PUBLISH_STEP][0]["env"])

    def test_reads_block_and_inline_scripts(self):
        lines = [
            "      - run: echo one",
            "      - name: x",
            "        run: |",
            "          echo two",
            "",
            "          echo three",
            "      - uses: y",
        ]
        self.assertEqual(run_scripts(lines), ["echo one", "          echo two\n\n          echo three"])

    def test_folds_continuation_lines(self):
        self.assertEqual(commands("  # why\n  a \\\n    b\n\n  c\n"), ["a b", "c"])


class NoExpressionInsideAScript(unittest.TestCase):
    """Values reach scripts through env:, never by text substitution.

    `${{ }}` is expanded before the shell sees the script, so a value that
    contains a quote or a `$(...)` becomes code. An env var cannot.
    """

    def test_follow_upstream(self):
        for name, lines in FOLLOW_JOBS.items():
            for script in run_scripts(lines):
                with self.subTest(job=name):
                    self.assertNotIn("${{", script)

    def test_ci_compat_jobs(self):
        for name, lines in CI_JOBS.items():
            for script in run_scripts(lines):
                with self.subTest(job=name):
                    self.assertNotIn("${{", script)


class EveryCompatRunIsUncached(unittest.TestCase):
    """-count=1: Go's test cache cannot see the gateway behind the BFF.

    Without it a pass against one gateway is replayed as "ok (cached)" for the
    next one, without a single request being sent.
    """

    def compat_runs(self, text):
        return [line for line in text.splitlines() if "go test" in line and "-tags compat" in line and not line.lstrip().startswith("#")]

    def test_every_invocation_passes_count_1(self):
        for name, text in (("follow-upstream.yml", FOLLOW), ("ci.yml", CI), ("e2e-stack.sh", STACK)):
            runs = self.compat_runs(text)
            with self.subTest(file=name):
                self.assertEqual(len(runs), 1, "the compat suite is run from here exactly once")
                self.assertIn("-count=1", runs[0])


class LeastPrivilege(unittest.TestCase):
    def test_the_workflow_grants_nothing_by_default(self):
        self.assertIn("\npermissions: {}\n", FOLLOW)

    def test_each_job_holds_exactly_what_it_uses(self):
        # Changing this table is the point at which a reviewer should ask why.
        expected = {
            "plan": {"contents": "read"},
            # Makes the commit; reads which pull requests came from the branch.
            "bump": {"contents": "read", "pull-requests": "read"},
            # Writes with the bump-bot App's token, not with this one.
            "publish-bump": {"contents": "read"},
            "check-a": {"contents": "read"},
            "report": {"contents": "read", "issues": "write"},
        }
        self.assertEqual({name: permissions(lines) for name, lines in FOLLOW_JOBS.items()}, expected)

    def test_the_job_that_runs_upstreams_image_can_write_nothing_and_sees_no_secret(self):
        self.assertNotIn("write", permissions(FOLLOW_JOBS["check-a"]).values())
        for name in ("check-a", "plan"):
            text = "\n".join(FOLLOW_JOBS[name])
            self.assertNotIn("secrets.", text, name)
            self.assertNotIn("github.token", text, name)

    def test_only_the_app_token_step_is_handed_a_secret(self):
        holders = []
        for name, lines in FOLLOW_JOBS.items():
            for step in steps(lines):
                if "secrets." in step["text"]:
                    holders.append((name, step["name"]))
        self.assertEqual(holders, [("publish-bump", APP_TOKEN_STEP)])
        self.assertNotIn("UPSTREAM_BOT_TOKEN", FOLLOW)

    def test_the_app_secrets_come_from_an_environment_only_main_can_use(self):
        self.assertIn("    environment: bump-bot", FOLLOW_JOBS["publish-bump"])
        for name, lines in FOLLOW_JOBS.items():
            if name != "publish-bump":
                self.assertNotIn("environment:", "\n".join(lines), name)
        step = [s for s in steps(FOLLOW_JOBS["publish-bump"]) if s["name"] == APP_TOKEN_STEP][0]
        self.assertIn("client-id: ${{ secrets.BUMP_BOT_CLIENT_ID }}", step["text"])
        self.assertIn("private-key: ${{ secrets.BUMP_BOT_PRIVATE_KEY }}", step["text"])
        # Asked for with what the job does and nothing more.
        perms = sorted(re.findall(r"permission-([a-z-]+): (\w+)", step["text"]))
        self.assertEqual(perms, [("contents", "write"), ("pull-requests", "write")])


class ActionsArePinned(unittest.TestCase):
    """The bump job holds a token that can push, so every action is a commit, not a tag."""

    def test_every_action_in_follow_upstream_is_pinned_by_sha(self):
        for name, lines in FOLLOW_JOBS.items():
            for step in steps(lines):
                if step["uses"]:
                    self.assertRegex(step["uses"], PINNED, "%s: %s" % (name, step["uses"]))

    def test_ci_compat_jobs_are_read_only(self):
        for name, lines in CI_JOBS.items():
            self.assertEqual(permissions(lines), {"contents": "read"}, name)


class TheJobThatMakesTheCommit(unittest.TestCase):
    """`bump` runs `go get` and the README script, so it holds no write token and no environment.

    That is also what lets a dry run from any ref preview the bump in full.
    """

    def setUp(self):
        self.lines = FOLLOW_JOBS["bump"]
        self.steps = steps(self.lines)
        self.text = "\n".join(self.lines)

    def step(self, name):
        return [s for s in self.steps if s["name"] == name][0]

    def test_it_holds_no_secret_no_environment_and_writes_nothing(self):
        self.assertNotIn("secrets.", self.text)
        self.assertNotIn("environment:", self.text)
        self.assertNotIn("write", (permissions(self.lines) or {}).values())

    def test_it_runs_whenever_the_plan_says_bump_dry_run_or_not(self):
        self.assertIn("    if: needs.plan.outputs.bump == 'true'", self.lines)

    def test_it_sets_up_two_toolchains_and_no_container(self):
        used = [action(step["uses"]) for step in self.steps if step["uses"]]
        self.assertEqual(
            used,
            ["actions/checkout", "actions/setup-go", "actions/setup-node", "actions/download-artifact", "actions/upload-artifact"],
        )
        for script in run_scripts(self.lines):
            for word in ("docker", "npm ", "npx ", "e2e-stack", "curl "):
                self.assertNotIn(word, script)

    def test_the_checkout_keeps_no_credentials_and_fetches_everything(self):
        checkout = self.steps[0]
        self.assertIn("persist-credentials: false", checkout["text"])
        self.assertIn("fetch-depth: 0", checkout["text"])

    def test_no_cache_is_restored_into_it(self):
        self.assertIn("cache: false", [s for s in self.steps if action(s["uses"]) == "actions/setup-go"][0]["text"])

    def test_the_listing_step_runs_gh_pr_list_and_nothing_else(self):
        lines = commands(self.step(LIST)["scripts"][0])
        self.assertEqual(len(lines), 2)
        line = lines[1]
        # Every state: a closed or merged pull request stops the bot as well.
        self.assertTrue(line.startswith('gh pr list --head "$BRANCH" --state all '), line)
        self.assertIn("isCrossRepository", line)
        self.assertRegex(line, r"> /tmp/upstream/pulls\.json$")
        self.assertIn("GH_TOKEN: ${{ github.token }}", self.step(LIST)["env"])

    def test_what_it_is_told_reaches_the_command(self):
        script = re.sub(r"\\\n\s*", "", self.step(BUILD)["scripts"][0])
        for flag in ("--plan /tmp/upstream/plan.json", "--pulls /tmp/upstream/pulls.json", '--repo-url "$REPO_URL"', '--dry-run "$DRY_RUN"'):
            self.assertIn(flag, script)
        self.assertEqual([l.strip() for l in self.step(BUILD)["env"].splitlines() if not l.strip().startswith("#")], ["GOTOOLCHAIN: auto"])

    def test_the_commit_is_handed_on_as_a_bundle_only_when_one_was_made(self):
        step = self.step("Bundle the bump commit for publish-bump")
        self.assertIn("if: steps.bump.outputs.made == 'true'", step["text"])
        self.assertEqual(commands(step["scripts"][0]), ["git bundle create /tmp/upstream/bump/bump.bundle refs/upstream/bump"])
        self.assertIn("      made: ${{ steps.bump.outputs.made }}", self.lines)


class TheJobThatCanWrite(unittest.TestCase):
    """`publish-bump` holds the App token, so it builds, tests and fetches nothing."""

    def setUp(self):
        self.lines = FOLLOW_JOBS["publish-bump"]
        self.steps = steps(self.lines)
        self.text = "\n".join(self.lines)

    def step(self, name):
        return [s for s in self.steps if s["name"] == name][0]

    def test_it_runs_only_on_main_for_a_real_run_with_a_commit_to_push(self):
        condition = [line for line in self.lines if line.startswith("    if:")][0]
        for part in (
            "needs.bump.outputs.made == 'true'",
            "github.ref == 'refs/heads/main'",
            "!(github.event_name == 'workflow_dispatch' && inputs.dry_run)",
        ):
            self.assertIn(part, condition)
        self.assertIn("    needs: [plan, bump]", self.lines)

    def test_it_uses_no_toolchain_and_no_container(self):
        used = [action(step["uses"]) for step in self.steps if step["uses"]]
        self.assertEqual(
            used,
            ["actions/checkout", "actions/download-artifact", "actions/download-artifact", "actions/create-github-app-token"],
        )
        for script in run_scripts(self.lines):
            for word in ("docker", "npm ", "node ", "go ", "curl ", "make "):
                self.assertNotIn(word, script)

    def test_it_checks_out_main_as_it_is_now_without_credentials(self):
        checkout = self.steps[0]
        self.assertIn("ref: main", checkout["text"])
        self.assertIn("persist-credentials: false", checkout["text"])

    def test_main_is_checked_and_the_commit_taken_before_the_token_exists(self):
        names = [s["name"] for s in self.steps]
        self.assertLess(names.index(FORWARD), names.index(APP_TOKEN_STEP))
        self.assertLess(names.index(UNBUNDLE), names.index(APP_TOKEN_STEP))
        self.assertLess(names.index(APP_TOKEN_STEP), names.index(PUBLISH_STEP))
        self.assertEqual(
            commands(self.step(FORWARD)["scripts"][0]),
            ["$FOLLOW check-forward --plan /tmp/upstream/plan.json --pins deploy/ci/gateway-pins.json"],
        )

    def test_only_the_publishing_step_is_handed_the_token(self):
        holders = [s["name"] for s in self.steps if "app-token.outputs.token" in s["text"] or "github.token" in s["text"]]
        self.assertEqual(holders, [PUBLISH_STEP])
        self.assertIn(TOKEN, self.step(PUBLISH_STEP)["env"])
        self.assertNotIn("GH_TOKEN", job_env(self.lines))

    def test_the_publishing_step_runs_publish_and_nothing_else(self):
        script = self.step(PUBLISH_STEP)["scripts"][0]
        self.assertIn('$FOLLOW publish --plan /tmp/upstream/bump/publish.json --dry-run "$DRY_RUN"', re.sub(r"\\\n\s*", "", script))
        for word in ("$FOLLOW bump", "git ", "gh ", "go ", "node", "python"):
            self.assertNotIn(word, script)

    def test_a_failed_publish_still_writes_its_summary_and_still_fails(self):
        script = self.step(PUBLISH_STEP)["scripts"][0]
        self.assertIn("|| status=$?", script)
        self.assertLess(script.index("|| status=$?"), script.index("GITHUB_STEP_SUMMARY"))
        self.assertEqual(commands(script)[-1], 'exit "$status"')

    def test_nothing_merges_and_nothing_turns_auto_merge_on(self):
        self.assertNotIn("UPSTREAM_AUTOMERGE", FOLLOW)
        import publish
        source = open(publish.__file__, encoding="utf-8").read()
        for word in ("pr merge", "enablePullRequestAutoMerge", "pr close", '"--force"'):
            self.assertNotIn(word, source)


class CheckA(unittest.TestCase):
    def setUp(self):
        self.lines = FOLLOW_JOBS["check-a"]
        self.text = "\n".join(self.lines)
        self.by_name = {step["name"]: step for step in steps(self.lines)}

    def test_it_runs_only_when_there_is_a_target_with_published_images(self):
        self.assertIn("    if: needs.plan.outputs.ready == 'true'", self.lines)

    def test_the_bff_is_the_one_main_builds_and_go_mod_cannot_move(self):
        self.assertIn("GOFLAGS: -mod=readonly", self.text)
        build = self.by_name["Build the BFF from the checked-in go.mod"]["scripts"][0]
        self.assertIn("go build -o ../bin/server ./cmd/server", build)
        self.assertIn("git diff --exit-code -- backend/go.mod backend/go.sum", build)
        for script in run_scripts(self.lines):
            self.assertNotIn("go get", script)
            self.assertNotIn("go mod tidy", script)

    def test_the_gateway_is_the_one_the_plan_resolved_by_digest(self):
        self.assertIn("OPENSHELL_GATEWAY_IMAGE: ${{ needs.plan.outputs.gateway_image }}", self.text)
        self.assertIn("OPENSHELL_SUPERVISOR_IMAGE: ${{ needs.plan.outputs.supervisor_image }}", self.text)
        self.assertIn("COMPAT_SANDBOX_IMAGE: ${{ needs.plan.outputs.sandbox_image }}", self.text)
        self.assertIn("OPENSHELL_CONFIG_SCHEMA: ${{ needs.plan.outputs.config_schema }}", self.text)

    def test_the_verdict_is_asserted_only_for_a_target_on_mains_own_line(self):
        self.assertIn(
            "COMPAT_EXPECT_COMPATIBILITY: ${{ needs.plan.outputs.same_line == 'true' && 'supported' || '' }}", self.text
        )

    def test_every_setup_step_is_a_result_and_gates_the_next(self):
        chain = [
            ("Pull images", "pull", None),
            ("Start gateway stack", "stack", "pull"),
            ("Start the BFF", "bff", "stack"),
            ("Run compat suite", "compat", "bff"),
        ]
        for name, step_id, needs in chain:
            text = self.by_name[name]["text"]
            with self.subTest(step=name):
                self.assertIn("id: %s" % step_id, text)
                self.assertIn("continue-on-error: true", text)
                if needs:
                    self.assertIn("if: steps.%s.outcome == 'success'" % needs, text)
        record = self.by_name["Record result"]
        for flag in ('--pull "$PULL"', '--stack "$STACK"', '--bff "$BFF"', '--compat "$COMPAT"'):
            self.assertIn(flag, record["scripts"][0])
        for variable in ("PULL: ${{ steps.pull.outcome }}", "COMPAT: ${{ steps.compat.outcome }}"):
            self.assertIn(variable, record["env"])

    def test_it_does_not_ask_a_stack_that_never_started_for_its_logs(self):
        self.assertIn(
            "if: always() && (steps.bff.outcome == 'failure' || steps.compat.outcome == 'failure')",
            self.by_name["Gateway logs"]["text"],
        )


class Images(unittest.TestCase):
    def test_images_are_never_named_in_a_script(self):
        # They arrive as tag@digest from the plan or the pins; a literal tag would be a moving one.
        for group in (FOLLOW_JOBS, CI_JOBS):
            for name, lines in group.items():
                for script in run_scripts(lines):
                    with self.subTest(job=name):
                        self.assertIsNone(re.search(r"openshell/(gateway|supervisor)[:@]", script))
                        self.assertNotIn("@latest", script)


class SideEffects(unittest.TestCase):
    def test_a_scheduled_run_acts_a_manual_one_only_when_told_and_never_off_main(self):
        self.assertIn(
            "  DRY_RUN: ${{ github.ref != 'refs/heads/main' || (github.event_name == 'workflow_dispatch' && inputs.dry_run) }}\n",
            FOLLOW,
        )

    def test_a_manual_run_is_a_dry_run_unless_the_box_is_cleared(self):
        dispatch = FOLLOW[FOLLOW.index("  workflow_dispatch:") : FOLLOW.index("\npermissions: {}")]
        self.assertIn("      dry_run:", dispatch)
        self.assertIn("        type: boolean", dispatch)
        self.assertIn("        default: true", dispatch)

    def test_it_runs_once_a_day_off_the_top_of_the_hour(self):
        crons = re.findall(r'^    - cron: "([^"]+)"$', FOLLOW, re.M)
        self.assertEqual(len(crons), 1)
        minute, hour, day, month, weekday = crons[0].split()
        self.assertEqual((day, month, weekday), ("*", "*", "*"))
        self.assertTrue(hour.isdigit(), hour)
        self.assertTrue(minute.isdigit() and 0 < int(minute) < 60, minute)

    def test_a_version_can_be_asked_for_and_reaches_the_plan_through_env(self):
        dispatch = FOLLOW[FOLLOW.index("  workflow_dispatch:") : FOLLOW.index("\npermissions: {}")]
        self.assertIn("      version:", dispatch)
        plan = "\n".join(FOLLOW_JOBS["plan"])
        self.assertIn("VERSION: ${{ inputs.version }}", plan)
        self.assertIn('--version "$VERSION"', "\n".join(run_scripts(FOLLOW_JOBS["plan"])))

    def test_every_step_that_writes_is_told_about_the_dry_run(self):
        plan_script = re.sub(r"\\\n\s*", "", "\n".join(run_scripts(FOLLOW_JOBS["plan"])))
        self.assertIn('--dry-run "$DRY_RUN"', plan_script)
        report_job = FOLLOW_JOBS["report"]
        step = report_job.index("      - name: Maintain the issue")
        self.assertEqual(report_job[step + 1], "        if: env.DRY_RUN == 'false'")
        self.assertIn('--dry-run "$DRY_RUN"', "\n".join(run_scripts(report_job)))

    def test_a_dry_run_cannot_take_the_scheduled_runs_place_in_the_queue(self):
        self.assertIn(
            "  group: follow-upstream-${{ github.ref == 'refs/heads/main' && 'main' || github.run_id }}\n", FOLLOW
        )
        self.assertIn("  cancel-in-progress: false\n", FOLLOW)

    def test_cancelling_a_run_stops_it_from_writing(self):
        # always() is true for a cancelled run as well, so Cancel would not
        # have stopped the issue from being edited.
        condition = [line for line in FOLLOW_JOBS["report"] if line.startswith("    if:")][0]
        self.assertIn("!cancelled()", condition)
        self.assertNotIn("always()", condition)

    def test_a_fork_does_not_start_following_upstream_on_a_schedule(self):
        self.assertIn(
            "    if: ${{ github.event_name != 'schedule' || !github.event.repository.fork }}", FOLLOW_JOBS["plan"]
        )
        # Every other job needs the plan, so skipping it skips them all.
        for name in ("bump", "check-a"):
            self.assertIn("    needs: plan", FOLLOW_JOBS[name])
        self.assertIn("    needs: [plan, bump]", FOLLOW_JOBS["publish-bump"])
        self.assertIn("needs.plan.result == 'success'", "\n".join(FOLLOW_JOBS["report"]))

    def test_the_issue_is_found_by_the_label_the_report_names(self):
        script = "\n".join(run_scripts(FOLLOW_JOBS["report"]))
        self.assertEqual(report.ISSUE_LABEL, "compat-migrate")
        self.assertIn("gh issue list --label %s --state open" % report.ISSUE_LABEL, script)
        self.assertIn("gh issue create --label %s " % report.ISSUE_LABEL, script)
        self.assertIn('--description "%s"' % report.ISSUE_LABEL_DESCRIPTION, script)

    def test_check_a_not_being_run_is_what_turns_the_run_red(self):
        report_job = "\n".join(FOLLOW_JOBS["report"])
        self.assertIn("INCOMPLETE: ${{ steps.report.outputs.incomplete }}", report_job)
        last = run_scripts(FOLLOW_JOBS["report"])[-1]
        self.assertIn('if [ "$INCOMPLETE" != "0" ]; then', last)
        self.assertIn("exit 1", last)

    def test_the_header_says_what_each_token_can_and_cannot_do(self):
        header = FOLLOW[: FOLLOW.index("\nname: Follow upstream")]
        flat = " ".join(header.replace("#", " ").split())
        for phrase in ("bump-bot GitHub App", "`bump-bot` environment", "only `main` can deploy to", "starts CI"):
            self.assertIn(phrase, flat)


class CiTestsThePinnedGateway(unittest.TestCase):
    def test_ci_runs_for_main_and_release_branches(self):
        triggers = CI[CI.index("\non:\n") : CI.index("\nconcurrency:")]
        self.assertIn('  push:\n    branches: [main, "release/**"]\n', triggers)
        self.assertIn('  pull_request:\n    branches: [main, "release/**"]\n', triggers)
        self.assertNotIn("next", triggers)

    def test_latest_and_the_dev_chart_come_from_main_only(self):
        for name in ("promote-latest", "publish-dev-chart"):
            self.assertIn("    if: github.event_name == 'push' && github.ref == 'refs/heads/main'", CI_ALL_JOBS[name])

    def test_validates_the_pins_file_against_go_mod_and_runs_these_tests(self):
        scripts = run_scripts(CI_JOBS["compat-pins"])
        self.assertIn(
            "python3 deploy/ci/upstream/follow.py validate-pins deploy/ci/gateway-pins.json --go-mod backend/go.mod", scripts
        )
        self.assertIn("python3 -m unittest discover -s deploy/ci/upstream", scripts)

    def test_compat_runs_one_gateway_the_pinned_one(self):
        text = "\n".join(CI_JOBS["compat"])
        self.assertIn("    needs: compat-pins", CI_JOBS["compat"])
        self.assertNotIn("matrix", text)
        self.assertNotIn("continue-on-error", text)
        for key in ("gateway_image", "supervisor_image", "config_schema", "sandbox_image"):
            self.assertIn("${{ needs.compat-pins.outputs.%s }}" % key, text)
        read_step = "\n".join(run_scripts(CI_JOBS["compat-pins"]))
        self.assertIn("for key in release gateway_image supervisor_image config_schema sandbox_image; do", read_step)

    def test_the_pinned_gateway_has_to_be_judged_supported(self):
        self.assertIn("      COMPAT_EXPECT_COMPATIBILITY: supported", CI_JOBS["compat"])

    def test_the_check_has_one_fixed_name_so_a_rule_can_require_it(self):
        self.assertFalse([line for line in CI_JOBS["compat"] if line.startswith("    name:")])

    def test_a_pre_release_is_refused_by_a_check_of_its_own_not_by_compat(self):
        job = CI_JOBS["stable-release"]
        self.assertIn("    name: pins a stable release", job)
        self.assertIn(
            "python3 deploy/ci/upstream/follow.py require-stable deploy/ci/gateway-pins.json", run_scripts(job)
        )
        self.assertNotIn("require-stable", "\n".join(CI_JOBS["compat"] + CI_JOBS["compat-pins"]))
        self.assertNotIn("needs:", "\n".join(job), "it does not wait for, or hide behind, another job")

    def test_it_runs_on_every_event_with_no_exception(self):
        self.assertFalse([line for line in CI_JOBS["stable-release"] if line.startswith("    if:")])

    def test_latest_is_promoted_only_from_main_and_only_past_both_checks(self):
        promote = "\n".join(CI_ALL_JOBS["promote-latest"])
        self.assertIn("    if: github.event_name == 'push' && github.ref == 'refs/heads/main'", CI_ALL_JOBS["promote-latest"])
        needs = promote[promote.index("    needs:") : promote.index("    steps:")]
        for job in ("stable-release", "compat", "release-tooling", "check-backend"):
            self.assertIn("        %s," % job, needs)

    def test_a_pull_request_from_this_repository_gets_its_images(self):
        # The build and its manifest are not limited by base branch; only by
        # the head being in this repository, which is what has the registry login.
        for name in ("build", "push-manifest"):
            text = "\n".join(CI_ALL_JOBS[name])
            self.assertIn("github.event.pull_request.head.repo.full_name == github.repository", text)
            self.assertNotIn("base_ref", text)
        self.assertIn("type=ref,event=pr,prefix=pr-", "\n".join(CI_ALL_JOBS["push-manifest"]))


class TheSweepIsRetired(unittest.TestCase):
    def test_its_workflow_and_its_directory_are_gone(self):
        self.assertFalse(os.path.exists(os.path.join(WORKFLOWS, "compat-sweep.yml")))
        self.assertFalse(os.path.exists(os.path.join(support.REPO_ROOT, "deploy", "ci", "sweep")))
        self.assertFalse(os.path.exists(os.path.join(support.REPO_ROOT, "scripts", "release", "sweep-bump.mjs")))

    def test_nothing_that_runs_still_calls_it(self):
        for name, text in (("ci.yml", CI), ("follow-upstream.yml", FOLLOW), ("publish.yml", PUBLISH), ("e2e-stack.sh", STACK)):
            for gone in ("deploy/ci/sweep", "sweep.py", "compat-sweep/", "check-diff", "SWEEP_TOKEN", "sweep-bump"):
                self.assertNotIn(gone, text, "%s still mentions %s" % (name, gone))

    def test_nothing_reads_lanes_or_a_floor_any_more(self):
        for text in (FOLLOW, "\n".join("\n".join(lines) for lines in CI_JOBS.values()), STACK):
            self.assertIsNone(re.search(r"\.lanes\b|\.floor\b|floor_release|matrix\.required", text))


class NoAutomaticRelease(unittest.TestCase):
    def test_publish_is_started_by_a_person_only(self):
        self.assertNotIn("workflow_run", PUBLISH)
        self.assertNotIn("next-merge", PUBLISH)
        self.assertIn("  workflow_dispatch:", PUBLISH)

    def test_the_next_machinery_is_gone(self):
        for path in (
            ("deploy", "ci", "upstream", "nextbranch.py"),
            ("scripts", "release", "next-merge.mjs"),
        ):
            self.assertFalse(os.path.exists(os.path.join(support.REPO_ROOT, *path)), path)
        for name, text in (("ci.yml", CI), ("follow-upstream.yml", FOLLOW), ("publish.yml", PUBLISH)):
            for gone in ("UPSTREAM_BOT_TOKEN", "UPSTREAM_AUTOMERGE", "refs/heads/next", "--head next"):
                self.assertNotIn(gone, text, "%s still mentions %s" % (name, gone))


class StackScript(unittest.TestCase):
    def test_reports_the_config_schema_the_gateway_accepted(self):
        self.assertIn('echo "config_schema=${RESOLVED_SCHEMA}" >> "$GITHUB_OUTPUT"', STACK)

    def test_prints_the_gateway_log_before_it_tears_a_failed_stack_down(self):
        attempt = STACK[STACK.index("try_schema() {") : STACK.index("\ndown() {")]
        self.assertLess(attempt.index("$COMPOSE logs"), attempt.index("$COMPOSE down -v"))
        self.assertLess(attempt.index('tail -n 120 <<<"$logs" >&2'), attempt.index("$COMPOSE down -v"))
        # Nothing asks for logs after try_schema has returned: by then the
        # containers are gone and the answer would be empty.
        up = STACK[STACK.index("\nup() {") : STACK.index("try_schema() {")]
        self.assertNotIn("$COMPOSE logs", up)

    def test_with_nothing_set_it_runs_the_pinned_gateway(self):
        self.assertIn('default_gateway="$(pinned gateway_image)"', STACK)
        self.assertIn('default_supervisor="$(pinned supervisor_image)"', STACK)
        self.assertIn('default_schema="$(pinned config_schema)"', STACK)
        # A version asked for by name wins over the pins.
        asked = STACK.index('if [ -n "${OPENSHELL_VERSION:-}" ]; then')
        self.assertLess(asked, STACK.index('default_gateway="$(pinned gateway_image)"'))


if __name__ == "__main__":
    unittest.main()
