"""The coverage ledger: what the dashboard does with each piece of the gateway's API.

`api-coverage.json` names every RPC a signed-in person may call at the pinned
release and every field of every message a client can send, and says for each
whether the SDK carries it, whether the BFF has a route for it, and how the
UI offers it. It is written by people: a thing is covered when someone has
read the code and said so, with the place.

Three things keep it honest:

- `problems` checks its shape, offline, in the unit tests.
- `against` compares it with upstream's protos at the release it names: an
  RPC or a field the ledger does not list is unclassified, and an entry for
  something upstream no longer has is stale.
- `summary` and `matrix` write it out as the counts and the tables of
  docs/api-coverage.md, and the unit tests fail when that file was not
  regenerated after an edit.

Standard library only.
"""

import json

UI_VALUES = ("form", "action", "document", "read-only", "fixed", "plumbing", "none")
UI_WORDS = {
    "form": "form field",
    "action": "action",
    "document": "document only",
    "read-only": "read-only",
    "fixed": "fixed value",
    "plumbing": "plumbing",
    "none": "**not offered**",
}
# A person can do it in the UI, one way or another.
OFFERED = frozenset(["form", "action", "document"])
FIELD_SDK = ("yes", "no", "n/a")
FIELD_BFF = ("yes", "no", "fixed")

# docs/api-coverage.md has two generated blocks, each between a pair of
# markers: the counts near the top and the tables at the end.
BLOCKS = ("summary", "matrix")


def begin(block):
    return "<!-- api-coverage:%s:begin (generated from deploy/ci/upstream/api-coverage.json; do not edit) -->" % block


def end(block):
    return "<!-- api-coverage:%s:end -->" % block

def load(path):
    with open(path, encoding="utf-8") as fh:
        return json.load(fh)


def problems(ledger):
    """What is wrong with the ledger's own shape. Empty when it is well formed."""
    out = []
    for key in ("release", "groups", "messages", "rpcs", "fields"):
        if key not in ledger:
            out.append("the ledger has no %r" % key)
    if out:
        return out
    group_ids = [group.get("id") for group in ledger["groups"]]
    listed = [rpc for group in ledger["groups"] for rpc in group.get("rpcs", [])]
    for rpc in sorted(set(ledger["rpcs"]) - set(listed)):
        out.append("%s is in no group" % rpc)
    for rpc in sorted(set(listed) - set(ledger["rpcs"])):
        out.append("%s is in a group and has no entry" % rpc)
    for rpc in sorted(set(r for r in listed if listed.count(r) > 1)):
        out.append("%s is in more than one group" % rpc)
    for message, group in sorted(ledger["messages"].items()):
        if group not in group_ids:
            out.append("%s is in group %r, which does not exist" % (message, group))
    for kind in ("rpcs", "fields"):
        for key, entry in sorted(ledger[kind].items()):
            for need in ("sdk", "bff", "ui", "where", "note"):
                if not isinstance(entry.get(need), str):
                    out.append("%s: %r is missing or not text" % (key, need))
            if entry.get("ui") not in UI_VALUES:
                out.append("%s: ui is %r, not one of %s" % (key, entry.get("ui"), ", ".join(UI_VALUES)))
            if kind == "fields":
                if entry.get("sdk") not in FIELD_SDK:
                    out.append("%s: sdk is %r, not one of %s" % (key, entry.get("sdk"), ", ".join(FIELD_SDK)))
                if entry.get("bff") not in FIELD_BFF:
                    out.append("%s: bff is %r, not one of %s" % (key, entry.get("bff"), ", ".join(FIELD_BFF)))
                if key.rsplit(".", 1)[0] not in ledger["messages"]:
                    out.append("%s: its message is in no group" % key)
    return out


def request_fields(surface):
    """Every field of every message a client can send: `message.field` -> field."""
    out = {}
    for name, message in surface["messages"].items():
        if "request" in message["sides"]:
            for field_name, field in message["fields"].items():
                out["%s.%s" % (name, field_name)] = field
    return out


def against(ledger, surface):
    """How the ledger differs from the API at a release.

    unclassified: in the API, not in the ledger. Somebody has to look.
    stale:        in the ledger, no longer in the API.
    """
    wanted = request_fields(surface)
    return {
        "unclassified_rpcs": sorted(set(surface["rpcs"]) - set(ledger["rpcs"])),
        "unclassified_fields": sorted(set(wanted) - set(ledger["fields"])),
        "stale_rpcs": sorted(set(ledger["rpcs"]) - set(surface["rpcs"])),
        "stale_fields": sorted(set(ledger["fields"]) - set(wanted)),
    }


def counts(ledger):
    """How many RPCs and fields the UI offers each way."""
    out = {"rpcs": dict.fromkeys(UI_VALUES, 0), "fields": dict.fromkeys(UI_VALUES, 0)}
    for kind in ("rpcs", "fields"):
        for entry in ledger[kind].values():
            out[kind][entry["ui"]] += 1
    return out


def _short(name):
    for prefix in ("openshell.v1.", "openshell.sandbox.v1.", "openshell.datamodel.v1."):
        if name.startswith(prefix):
            return name[len(prefix) :]
    return name


def _cell(text):
    return (text or "").replace("|", "\\|").replace("\n", " ")


def _where(entry):
    return "`%s`" % entry["where"] if entry["where"] else ""


def summary_lines(ledger):
    """The counts, as the sentences at the top of the generated block."""
    made = counts(ledger)
    rpcs, fields = made["rpcs"], made["fields"]
    total_rpcs = sum(rpcs.values())
    offered = sum(rpcs[ui] for ui in OFFERED)
    authored = sum(fields.values()) - fields["plumbing"]
    return [
        "At OpenShell %s the gateway has **%d** RPCs a signed-in person may call. The dashboard offers **%d**"
        % (ledger["release"], total_rpcs, offered),
        "of them as something a person can do, shows %d more read-only, and has nothing for **%d**."
        % (rpcs["read-only"] + rpcs["plumbing"] + rpcs["fixed"], rpcs["none"]),
        "",
        "The messages a client can send have **%d** fields. %d are plumbing the dashboard fills in"
        % (sum(fields.values()), fields["plumbing"]),
        "(the workspace of the route, page tokens, concurrency tokens). Of the other **%d**:" % authored,
        "",
        "| A person sets it | Fields | Share |",
        "|---|---|---|",
    ] + [
        "| %s | %d | %d%% |" % (label, fields[ui], round(100.0 * fields[ui] / authored) if authored else 0)
        for ui, label in (
            ("form", "with an input in a form"),
            ("action", "by pressing a button"),
            ("document", "only by writing YAML or JSON, or choosing a file"),
            ("fixed", "never: the dashboard always sends one value"),
            ("read-only", "never: it is shown only"),
            ("none", "never: **the dashboard cannot send it**"),
        )
    ]


def summary(ledger):
    return "\n".join(summary_lines(ledger)) + "\n"


def matrix(ledger):
    """The whole ledger as Markdown tables, grouped as the ledger groups it."""
    lines = []
    by_group = {}
    for message, group in ledger["messages"].items():
        by_group.setdefault(group, []).append(message)
    by_message = {}
    for key, entry in ledger["fields"].items():
        message, field = key.rsplit(".", 1)
        by_message.setdefault(message, []).append((field, entry))

    for group in ledger["groups"]:
        lines += ["## %s" % group["title"], ""]
        if group.get("about"):
            lines += [group["about"], ""]
        lines += ["| RPC | SDK | BFF | UI | Where | Note |", "|---|---|---|---|---|---|"]
        for rpc in group["rpcs"]:
            entry = ledger["rpcs"][rpc]
            name = rpc.split(".", 1)[1] if rpc.startswith("OpenShell.") else rpc
            lines.append(
                "| `%s` | %s | %s | %s | %s | %s |"
                % (name, _cell(entry["sdk"]), _cell(entry["bff"]), UI_WORDS[entry["ui"]], _where(entry), _cell(entry["note"]))
            )
        lines.append("")
        quiet = []
        for message in sorted(by_group.get(group["id"], []), key=_short):
            rows = sorted(by_message.get(message, []))
            if not rows:
                continue
            if all(entry["ui"] in ("plumbing", "action") and not entry["note"] for _, entry in rows):
                quiet.append("`%s` (%s)" % (_short(message), ", ".join("`%s`" % field for field, _ in rows)))
                continue
            lines += ["### `%s`" % _short(message), "", "| Field | SDK | BFF | UI | Where | Note |", "|---|---|---|---|---|---|"]
            for field, entry in rows:
                lines.append(
                    "| `%s` | %s | %s | %s | %s | %s |"
                    % (field, entry["sdk"], entry["bff"], UI_WORDS[entry["ui"]], _where(entry), _cell(entry["note"]))
                )
            lines.append("")
        if quiet:
            lines += ["Requests with nothing for a person to author: %s." % "; ".join(quiet), ""]
    return "\n".join(lines).rstrip() + "\n"


def splice(document, block, text):
    """`document` with what stands between the markers of `block` replaced by `text`."""
    first, last = begin(block), end(block)
    start, stop = document.find(first), document.find(last)
    if start < 0 or stop < start:
        raise ValueError("the document has no %s ... %s block" % (first, last))
    return document[: start + len(first)] + "\n\n" + text.rstrip() + "\n\n" + document[stop:]


def write_into(document, ledger):
    """`document` with both generated blocks made from `ledger`."""
    document = splice(document, "summary", summary(ledger))
    return splice(document, "matrix", matrix(ledger))


def unclassified_report(found, release):
    """What `against` found, as lines for a person."""
    lines = []
    for key, what in (
        ("unclassified_rpcs", "RPCs the ledger does not list"),
        ("unclassified_fields", "request fields the ledger does not list"),
        ("stale_rpcs", "RPCs the ledger lists that OpenShell %s does not have" % release),
        ("stale_fields", "fields the ledger lists that OpenShell %s does not have" % release),
    ):
        if found[key]:
            lines.append("%d %s:" % (len(found[key]), what))
            lines.extend("  %s" % name for name in found[key])
    return lines
