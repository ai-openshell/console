"""What changed in the gateway's public API between two upstream releases.

The dashboard is meant to offer what the gateway's user-facing API can do, and
nobody here reads upstream's commits. So every time the Follow upstream
workflow finds a release ahead of the pinned one, it compares the two
releases' protos and says what a signed-in person can now reach that they
could not before: RPCs, fields and enum values, added, removed or changed.
Each new thing is looked up in the coverage ledger (apicoverage.py), and what
the ledger does not know yet is looked for in the dashboard's source, so the
report also says whether the dashboard has it.

`diff` compares two `protoapi.surface` documents. `render` writes the report
as Markdown: for the bump pull request, the run summary and check A's issue.

Standard library only.
"""

import os
import re

# Tables in a pull request body are cut at this many rows, with the rest
# counted: GitHub refuses a body over 65,536 characters. The run summary and
# the command line print everything.
PR_ROWS = 40

_SIDE_WORDS = {"request": "sent", "response": "returned"}


def _users(surface):
    """Which user-facing RPCs reach each message and each enum, by name."""
    users = {}
    for key in sorted(surface["rpcs"]):
        rpc = surface["rpcs"][key]
        seen, queue = set(), [rpc["request"], rpc["response"]]
        while queue:
            name = queue.pop()
            if name in seen:
                continue
            seen.add(name)
            message = surface["messages"].get(name)
            if message:
                queue.extend(field["type"] for field in message["fields"].values())
        for name in seen:
            users.setdefault(name, []).append(key.split(".", 1)[1])
    return users


def _field_shape(field):
    label = field["label"]
    if label == "map":
        return "map<%s, %s>" % (field["key"], field["type"])
    return ("%s %s" % (label, field["type"])).strip()


def _rpc_shape(rpc):
    return "(%s%s) returns (%s%s)" % (
        "stream " if rpc["client_stream"] else "",
        rpc["request"],
        "stream " if rpc["server_stream"] else "",
        rpc["response"],
    )


def _who(auth):
    """Who may call an RPC, in the words of its authorization rule."""
    mode = auth.get("auth_mode", "")
    if not mode:
        return "no authorization rule"
    if mode == "unauthenticated":
        return "anyone"
    role = auth.get("global_role") or auth.get("workspace_role")
    who = {"platform_admin": "platform admin", "admin": "workspace admin", "user": "workspace user"}.get(
        role, role or "any signed-in user"
    )
    if mode == "dual":
        who += ", or a sandbox"
    if auth.get("scope"):
        who += " (`%s`)" % auth["scope"]
    return who


def diff(old, new):
    """Everything that differs between two surfaces, as lists of plain dicts."""
    out = {
        "rpcs_added": [],
        "rpcs_removed": [],
        "rpcs_changed": [],
        "messages_added": [],
        "messages_removed": [],
        "fields_added": [],
        "fields_removed": [],
        "fields_changed": [],
        "enums_added": [],
        "enum_values_added": [],
        "enum_values_removed": [],
        "internal_added": sorted(set(new["internal"]) - set(old["internal"]) - set(old["rpcs"])),
        "internal_removed": sorted(set(old["internal"]) - set(new["internal"]) - set(new["rpcs"])),
    }
    users = _users(new)
    old_users = _users(old)

    for key in sorted(set(new["rpcs"]) - set(old["rpcs"])):
        rpc = new["rpcs"][key]
        out["rpcs_added"].append(
            {
                "rpc": key,
                "doc": rpc["doc"],
                "who": _who(rpc["auth"]),
                "shape": _rpc_shape(rpc),
                "was_internal": key in old["internal"],
            }
        )
    for key in sorted(set(old["rpcs"]) - set(new["rpcs"])):
        out["rpcs_removed"].append({"rpc": key, "now_internal": new["internal"].get(key, "")})
    for key in sorted(set(old["rpcs"]) & set(new["rpcs"])):
        before, after = old["rpcs"][key], new["rpcs"][key]
        changes = []
        if _rpc_shape(before) != _rpc_shape(after):
            changes.append("`%s` is now `%s`" % (_rpc_shape(before), _rpc_shape(after)))
        if before["auth"] != after["auth"]:
            changes.append("may be called by %s (was %s)" % (_who(after["auth"]), _who(before["auth"])))
        if changes:
            out["rpcs_changed"].append({"rpc": key, "changes": changes})

    for name in sorted(set(new["messages"]) - set(old["messages"])):
        message = new["messages"][name]
        out["messages_added"].append(
            {
                "message": name,
                "doc": message["doc"],
                "sides": message["sides"],
                "fields": sorted(message["fields"]),
                "used_by": users.get(name, []),
            }
        )
    for name in sorted(set(old["messages"]) - set(new["messages"])):
        out["messages_removed"].append({"message": name, "used_by": old_users.get(name, [])})

    for name in sorted(set(old["messages"]) & set(new["messages"])):
        before, after = old["messages"][name]["fields"], new["messages"][name]["fields"]
        sides = new["messages"][name]["sides"]
        by_number = dict((field["number"], field_name) for field_name, field in before.items())
        renamed = {}
        for field_name in sorted(set(after) - set(before)):
            field = after[field_name]
            was = by_number.get(field["number"])
            if was is not None and was not in after and _field_shape(before[was]) == _field_shape(field):
                renamed[was] = field_name
                out["fields_changed"].append(
                    {
                        "field": "%s.%s" % (name, field_name),
                        "sides": sides,
                        "changes": ["renamed from `%s` (same number, same type)" % was],
                    }
                )
                continue
            out["fields_added"].append(
                {
                    "field": "%s.%s" % (name, field_name),
                    "doc": field["doc"],
                    "shape": _field_shape(field),
                    "sides": sides,
                    "secret": field["secret"],
                    "used_by": users.get(name, []),
                }
            )
        for field_name in sorted(set(before) - set(after)):
            if field_name not in renamed:
                out["fields_removed"].append(
                    {"field": "%s.%s" % (name, field_name), "sides": sides, "used_by": users.get(name, [])}
                )
        for field_name in sorted(set(before) & set(after)):
            was, now = before[field_name], after[field_name]
            changes = []
            if _field_shape(was) != _field_shape(now):
                changes.append("type `%s` is now `%s`" % (_field_shape(was), _field_shape(now)))
            if was["number"] != now["number"]:
                changes.append("field number %d is now %d" % (was["number"], now["number"]))
            if was["oneof"] != now["oneof"]:
                changes.append("oneof `%s` is now `%s`" % (was["oneof"] or "none", now["oneof"] or "none"))
            if was["secret"] != now["secret"]:
                changes.append("is now secret" if now["secret"] else "is no longer secret")
            if now["deprecated"] and not was["deprecated"]:
                changes.append("is now deprecated")
            if changes:
                out["fields_changed"].append({"field": "%s.%s" % (name, field_name), "sides": sides, "changes": changes})

    uses = {}
    for name, message in new["messages"].items():
        for field_name, field in message["fields"].items():
            uses.setdefault(field["type"], []).append("%s.%s" % (name.rsplit(".", 1)[1], field_name))
    for name in sorted(set(new["enums"]) - set(old["enums"])):
        out["enums_added"].append(
            {"enum": name, "values": sorted(new["enums"][name]["values"]), "used_in": sorted(uses.get(name, []))}
        )
    for name in sorted(set(old["enums"]) & set(new["enums"])):
        before, after = old["enums"][name]["values"], new["enums"][name]["values"]
        for value in sorted(set(after) - set(before), key=lambda v: after[v]["number"]):
            out["enum_values_added"].append(
                {
                    "enum": name,
                    "value": value,
                    "number": after[value]["number"],
                    "doc": after[value]["doc"],
                    "used_in": sorted(uses.get(name, [])),
                }
            )
        for value in sorted(set(before) - set(after)):
            out["enum_values_removed"].append({"enum": name, "value": value})
    return out


def counts(changes):
    return dict((key, len(value)) for key, value in changes.items())


def is_empty(changes):
    return not any(changes.values())


# -- does the dashboard have it? -------------------------------------------

_SOURCE_DIRS = ("frontend/src", "backend/pkg")
_SKIP_DIRS = frozenset(["__tests__", "__mocks__", "mocks", "node_modules", "testdata"])
_SOURCE_EXT = (".ts", ".tsx", ".go")


class Source(object):
    """The dashboard's own source, for asking whether a name occurs in it.

    A search is a hint, not a proof: `name` occurs everywhere, and a field the
    types name is not a field a form offers. The ledger is what says a thing
    is covered; this only says whether anyone has typed its name.
    """

    def __init__(self, root):
        self.files = {}
        for top in _SOURCE_DIRS:
            for directory, subdirs, names in os.walk(os.path.join(root, top)):
                subdirs[:] = [d for d in subdirs if d not in _SKIP_DIRS]
                for name in names:
                    if not name.endswith(_SOURCE_EXT) or re.search(r"(_test\.go|\.(spec|test)\.tsx?)$", name):
                        continue
                    path = os.path.join(directory, name)
                    with open(path, encoding="utf-8", errors="replace") as fh:
                        self.files[os.path.relpath(path, root).replace(os.sep, "/")] = fh.read()

    def mentions(self, *words, **options):
        """The files in which any of `words` stands as a whole word.

        calls=True leaves out a word followed by `()`: that is the generated
        getter of a field of the same name (`GetSecret()` reads a field
        called `secret`), not a call of the RPC `GetSecret`.
        """
        tail = r"(?![A-Za-z0-9_])" + (r"(?!\(\))" if options.get("calls") else "")
        pattern = re.compile(r"(?<![A-Za-z0-9_])(?:%s)%s" % ("|".join(re.escape(w) for w in words if w), tail))
        return sorted(path for path, text in self.files.items() if pattern.search(text))


def _camel(snake, upper=False):
    parts = snake.split("_")
    head = parts[0].capitalize() if upper else parts[0]
    return head + "".join(part.capitalize() for part in parts[1:])


def _enum_words(enum, value):
    """The spellings an enum value goes by: the SDK's and the UI's.

    SANDBOX_PHASE_SUSPENDED of SandboxPhase is `SUSPENDED` in JSON the BFF
    writes, `Suspended` in a label or a TypeScript union and SandboxPhaseSuspended in Go.
    """
    short_enum = enum.rsplit(".", 1)[1]
    prefix = re.sub(r"(?<=[a-z0-9])(?=[A-Z])", "_", short_enum).upper() + "_"
    short = value[len(prefix) :] if value.startswith(prefix) else value
    title = _camel(short.lower(), upper=True)
    return [value, short, title, short_enum + title]


def _found(paths):
    if not paths:
        return "not named anywhere in `frontend/src` or `backend/pkg`"
    ui = [path for path in paths if path.startswith("frontend/")]
    where = "UI and BFF" if ui and len(ui) < len(paths) else ("UI" if ui else "BFF only")
    return "named in the %s (%d file%s, first `%s`)" % (where, len(paths), "" if len(paths) == 1 else "s", paths[0])


def _ledger_says(entry):
    ui = entry.get("ui", "")
    text = {
        "none": "**not offered**",
        "plumbing": "filled in by the dashboard",
        "fixed": "always sent with one value",
        "read-only": "shown, cannot be set",
        "document": "document only (YAML or JSON), no input",
        "form": "offered as a form field",
        "action": "offered",
    }.get(ui, ui)
    if entry.get("where") and ui not in ("none",):
        text += " (`%s`)" % entry["where"]
    return text


def annotate(changes, ledger=None, source=None):
    """Add `dashboard` to every added item: what the dashboard does with it."""
    ledger = ledger or {}
    rpcs, fields = ledger.get("rpcs", {}), ledger.get("fields", {})

    def searched(words, calls=False):
        if source is None:
            return "not in the coverage ledger"
        return "not in the coverage ledger; " + _found(source.mentions(*words, calls=calls))

    for item in changes["rpcs_added"]:
        entry = rpcs.get(item["rpc"])
        name = item["rpc"].split(".", 1)[1]
        item["covered"] = bool(entry) and entry.get("ui") not in ("none", None)
        item["dashboard"] = _ledger_says(entry) if entry else "**not covered**: " + searched([name], calls=True)
    for item in changes["fields_added"]:
        entry = fields.get(item["field"])
        name = item["field"].rsplit(".", 1)[1]
        item["covered"] = bool(entry) and entry.get("ui") not in ("none", None)
        if entry:
            item["dashboard"] = _ledger_says(entry)
        else:
            item["dashboard"] = searched([name, _camel(name), _camel(name, upper=True)])
            item["covered"] = None
    for item in changes["enum_values_added"]:
        words = _enum_words(item["enum"], item["value"])
        item["covered"] = None
        item["dashboard"] = _found(source.mentions(*words)) if source is not None else ""
    return changes


# -- the report --------------------------------------------------------------


def _short(name):
    """`openshell.v1.SandboxSpec.log_level` as people say it: SandboxSpec.log_level."""
    return re.sub(r"^openshell\.(?:[a-z]+\.)?v1\.", "", name)


def _cell(text):
    return (text or "").replace("|", "\\|").replace("\n", " ")


def _used(names, limit=3):
    if not names:
        return ""
    shown = ", ".join("`%s`" % name for name in names[:limit])
    return shown + (" and %d more" % (len(names) - limit) if len(names) > limit else "")


def _table(header, rows, limit):
    if not rows:
        return []
    lines = ["| " + " | ".join(header) + " |", "|" + "---|" * len(header)]
    shown = rows if not limit else rows[:limit]
    lines.extend("| " + " | ".join(_cell(cell) for cell in row) + " |" for row in shown)
    if len(shown) < len(rows):
        lines.append("")
        lines.append("…and %d more. The run summary lists every row." % (len(rows) - len(shown)))
    lines.append("")
    return lines


def headline(changes, old_version, new_version):
    """One sentence: how much is new and how much of it the dashboard lacks."""
    if is_empty(changes):
        return "The gateway's user-facing API is the same in OpenShell %s and %s." % (old_version, new_version)
    sent = [item for item in changes["fields_added"] if "request" in item["sides"]]
    parts = []
    for number, one, many in (
        (len(changes["rpcs_added"]), "new RPC", "new RPCs"),
        (len(sent), "new field a client can send", "new fields a client can send"),
        (len(changes["fields_added"]) - len(sent), "new field in what it returns", "new fields in what it returns"),
        (len(changes["enum_values_added"]), "new enum value", "new enum values"),
        (len(changes["messages_added"]), "new message", "new messages"),
    ):
        if number:
            parts.append("%d %s" % (number, one if number == 1 else many))
    breaking = len(changes["rpcs_removed"]) + len(changes["fields_removed"]) + len(changes["enum_values_removed"])
    breaking += len(changes["rpcs_changed"]) + len(changes["fields_changed"]) + len(changes["messages_removed"])
    text = "OpenShell %s → %s: %s." % (old_version, new_version, ", ".join(parts) or "nothing added")
    if breaking:
        text += " %d thing%s removed or changed." % (breaking, "" if breaking == 1 else "s")
    return text


def render(changes, old_version, new_version, limit=0, heading="##"):
    """The report as Markdown. `limit` cuts each table (0: every row)."""
    lines = ["%s What is new in the gateway's API" % heading, "", headline(changes, old_version, new_version), ""]
    if is_empty(changes):
        return "\n".join(lines)
    lines += [
        "Read from upstream's protos at `v%s` and `v%s`: the RPCs a signed-in person may call, and"
        % (old_version, new_version),
        "every message one of them sends or returns. \"Dashboard\" is what",
        "the coverage ledger (`deploy/ci/upstream/api-coverage.json`) says, or, for what it does not list,",
        "whether the name occurs in the dashboard's source. A name that occurs is a hint, not a feature.",
        "",
    ]
    sub = heading + "#"

    rows = [
        [
            "`%s`%s" % (item["rpc"].split(".", 1)[1], " (was sandbox-only)" if item["was_internal"] else ""),
            item["who"],
            item["doc"],
            item.get("dashboard", ""),
        ]
        for item in changes["rpcs_added"]
    ]
    if rows:
        lines += ["%s New RPCs" % sub, ""] + _table(["RPC", "Who may call it", "What it does", "Dashboard"], rows, limit)

    for side, title in (("request", "New fields a client can send"), ("response", "New fields in what the gateway returns")):
        rows = []
        for item in changes["fields_added"]:
            if ("request" in item["sides"]) != (side == "request"):
                continue
            rows.append(
                [
                    "`%s`%s" % (_short(item["field"]), " (secret)" if item["secret"] else ""),
                    "`%s`" % _short(item["shape"]),
                    item["doc"],
                    _used(item["used_by"]),
                    item.get("dashboard", ""),
                ]
            )
        if rows:
            lines += ["%s %s" % (sub, title), ""]
            lines += _table(["Field", "Type", "What it is", "Reached from", "Dashboard"], rows, limit)

    rows = [
        [
            "`%s`" % _short(item["enum"]),
            "`%s`" % item["value"],
            item["doc"],
            _used(item["used_in"]),
            item.get("dashboard", ""),
        ]
        for item in changes["enum_values_added"]
    ]
    if rows:
        lines += ["%s New enum values" % sub, ""]
        lines += _table(["Enum", "Value", "What it is", "Used in", "Dashboard"], rows, limit)

    rows = [
        [
            "`%s`" % _short(item["message"]),
            " and ".join(_SIDE_WORDS[side] for side in item["sides"]),
            ", ".join("`%s`" % name for name in item["fields"]) or "(no fields)",
            _used(item["used_by"]),
        ]
        for item in changes["messages_added"]
    ]
    rows += [
        ["`%s` (enum)" % _short(item["enum"]), "", ", ".join("`%s`" % v for v in item["values"]), _used(item["used_in"])]
        for item in changes["enums_added"]
    ]
    if rows:
        lines += ["%s New messages and enums" % sub, ""]
        lines += _table(["Message", "Is", "Fields", "Reached from"], rows, limit)

    rows = []
    for item in changes["rpcs_removed"]:
        why = "now %s-only" % item["now_internal"] if item["now_internal"] else "removed"
        rows.append(["RPC `%s`" % item["rpc"].split(".", 1)[1], why])
    for item in changes["rpcs_changed"]:
        rows.append(["RPC `%s`" % item["rpc"].split(".", 1)[1], "; ".join(item["changes"])])
    for item in changes["messages_removed"]:
        rows.append(["message `%s`" % _short(item["message"]), "no longer reached from a user-facing RPC"])
    for item in changes["fields_removed"]:
        rows.append(["field `%s`" % _short(item["field"]), "removed (%s)" % (_used(item["used_by"]) or "unused")])
    for item in changes["fields_changed"]:
        rows.append(["field `%s`" % _short(item["field"]), "; ".join(item["changes"])])
    for item in changes["enum_values_removed"]:
        rows.append(["enum value `%s`" % item["value"], "removed from `%s`" % _short(item["enum"])])
    if rows:
        lines += [
            "%s Removed or changed" % sub,
            "",
            "These can break what works today. A changed field number or type is the silent kind:",
            "nothing errors, the gateway reads something else (hard fact 18).",
            "",
        ] + _table(["What", "Change"], rows, limit)

    internal = ["`%s`" % name.split(".", 1)[1] for name in changes["internal_added"]]
    gone = ["`%s`" % name.split(".", 1)[1] for name in changes["internal_removed"]]
    if internal or gone:
        lines += ["%s Not user-facing" % sub, ""]
        if internal:
            lines.append("New RPCs for sandboxes, peers or operators, which the dashboard never calls: %s." % ", ".join(internal))
        if gone:
            lines.append("Removed: %s." % ", ".join(gone))
        lines.append("")
    return "\n".join(lines).rstrip() + "\n"
