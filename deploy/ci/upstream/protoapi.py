"""The gateway's public API, read from upstream's .proto files.

This repository holds no copy of the protos (ADR 0003), so nothing here is
generated and nothing is checked in: the files are read from an upstream tag
when the question is asked. `parse` turns one file into plain dicts,
`load` follows the imports of `openshell.proto` (the closure the SDKs are
generated from), and `surface` keeps what a person signed in to the gateway
can reach: the RPCs that take a user's bearer, and every message and enum one
of those RPCs sends or returns.

The parser covers the proto3 the upstream files use: messages, nested
messages and enums, oneofs, maps, `reserved`, field and method options. It is
not a general protobuf compiler and does not try to be: a construct it does
not know raises ProtoError with the file and line, so that a new syntax
upstream stops the report instead of quietly leaving something out.

Standard library only.
"""

import re
import subprocess

ROOT = "openshell.proto"

SCALARS = frozenset(
    "double float int32 int64 uint32 uint64 sint32 sint64 fixed32 fixed64 "
    "sfixed32 sfixed64 bool string bytes".split()
)

# Who may call an RPC is written on it, as the `authorization` method option
# (options.proto; every RPC has carried one since OpenShell 0.1.0). These
# modes admit a person or an API client: "dual" is a user or a sandbox. The
# others are a sandbox ("sandbox"), another gateway ("peer") or an operator's
# mTLS certificate ("operator"), none of which the dashboard is.
USER_AUTH_MODES = frozenset(["bearer", "dual", "unauthenticated"])

_TOKEN = re.compile(
    r"""
    (?P<space>\s+)
  | (?P<line>//[^\n]*)
  | (?P<block>/\*.*?\*/)
  | (?P<str>"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*')
  | (?P<num>[-+]?(?:0[xX][0-9a-fA-F]+|\d+(?:\.\d+)?(?:[eE][-+]?\d+)?))
  | (?P<ident>\.?[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)
  | (?P<sym>[{}\[\]()<>;=,:])
    """,
    re.X | re.S,
)


class ProtoError(Exception):
    """A .proto file this parser cannot read."""


def _tokens(text, name):
    """(kind, value, line, trailing) for every token; comments are kept.

    `trailing` is true for a comment that follows code on its own line: it
    documents what is before it, not what comes next.
    """
    out = []
    pos, line, code_line = 0, 1, 0
    while pos < len(text):
        match = _TOKEN.match(text, pos)
        if not match:
            raise ProtoError("%s:%d: cannot read %r" % (name, line, text[pos : pos + 20]))
        kind, value = match.lastgroup, match.group()
        if kind in ("line", "block"):
            body = value[2:] if kind == "line" else value[2:-2]
            out.append(("comment", body.strip(), line, line == code_line))
        elif kind != "space":
            out.append((kind, value, line, False))
            code_line = line + value.count("\n")
        line += value.count("\n")
        pos = match.end()
    return out


def _sentence(lines):
    """The first sentence of a comment block, on one line."""
    text = " ".join(part for part in (line.strip() for line in lines) if part)
    text = text.split("  ")[0]
    match = re.search(r"(?<=[a-z0-9`)])\.(?:\s|$)", text)
    return (text[: match.start() + 1] if match else text).strip()


class _Parser(object):
    def __init__(self, text, name):
        self.name = name
        self.tokens = _tokens(text, name)
        self.pos = 0
        self.file = {"name": name, "package": "", "imports": [], "services": {}, "messages": {}, "enums": {}}

    # -- tokens ------------------------------------------------------------

    def _skip_comments(self):
        """Step over comments; return the block that ends right above the next token."""
        block, last = [], None
        while self.pos < len(self.tokens) and self.tokens[self.pos][0] == "comment":
            _, body, line, trailing = self.tokens[self.pos]
            self.pos += 1
            if trailing:
                continue
            if last is not None and line > last + 1:
                block = []
            block.extend(body.split("\n"))
            last = line + body.count("\n")
        if self.pos < len(self.tokens) and last is not None and self.tokens[self.pos][2] > last + 1:
            block = []
        return block

    def _peek(self):
        self._pending = self._skip_comments() or getattr(self, "_pending", [])
        if self.pos >= len(self.tokens):
            return ("eof", "", self.tokens[-1][2] if self.tokens else 0, False)
        return self.tokens[self.pos]

    def _next(self):
        token = self._peek()
        self.pos += 1
        return token

    def _doc(self):
        """The comment above the declaration about to be read."""
        self._peek()
        doc, self._pending = self._pending, []
        return _sentence(doc)

    def _trailing_doc(self, line):
        """A comment after the declaration that just ended, on its line."""
        if self.pos < len(self.tokens):
            kind, body, at, trailing = self.tokens[self.pos]
            if kind == "comment" and trailing and at == line:
                return _sentence(body.split("\n"))
        return ""

    def _fail(self, what, token):
        raise ProtoError("%s:%d: expected %s, found %r" % (self.name, token[2], what, token[1]))

    def _expect(self, value):
        token = self._next()
        if token[1] != value:
            self._fail(repr(value), token)
        return token

    def _ident(self):
        token = self._next()
        if token[0] != "ident":
            self._fail("a name", token)
        return token[1]

    def _int(self):
        token = self._next()
        if token[0] != "num":
            self._fail("a number", token)
        return int(token[1], 0)

    def _skip_statement(self):
        """Skip to the `;` that ends a statement, over any nested braces."""
        depth = 0
        while True:
            token = self._next()
            if token[0] == "eof":
                self._fail("';'", token)
            if token[1] in "{[(":
                depth += 1
            elif token[1] in "}])":
                depth -= 1
            elif token[1] == ";" and depth == 0:
                return

    # -- option values -----------------------------------------------------

    def _value(self):
        """A constant: a scalar, or a `{ key: value ... }` message literal."""
        token = self._next()
        if token[1] == "{":
            out = {}
            while self._peek()[1] != "}":
                key = self._ident()
                if self._peek()[1] == ":":
                    self._next()
                out[key] = self._value()
                if self._peek()[1] in (",", ";"):
                    self._next()
            self._next()
            return out
        if token[1] == "[":
            out = []
            while self._peek()[1] != "]":
                out.append(self._value())
                if self._peek()[1] == ",":
                    self._next()
            self._next()
            return out
        if token[0] == "str":
            return token[1][1:-1]
        if token[0] == "num":
            return token[1]
        if token[0] == "ident":
            return token[1]
        self._fail("a value", token)

    def _option_name(self):
        """`deprecated`, or `(openshell.options.v1.secret)`, with any `.field` suffix."""
        parts = []
        while True:
            token = self._next()
            if token[1] == "(":
                parts.append("(" + self._ident() + ")")
                self._expect(")")
            elif token[0] == "ident":
                parts.append(token[1])
            else:
                self._fail("an option name", token)
            if self._peek()[1] != "=":
                continue
            return "".join(parts)

    def _bracket_options(self):
        """The `[a = 1, (b) = true]` after a field or an enum value."""
        options = {}
        if self._peek()[1] != "[":
            return options
        self._next()
        while True:
            name = self._option_name()
            self._expect("=")
            options[name] = self._value()
            token = self._next()
            if token[1] == "]":
                return options
            if token[1] != ",":
                self._fail("',' or ']'", token)

    # -- declarations ------------------------------------------------------

    def parse(self):
        while True:
            doc = self._doc()
            token = self._next()
            if token[0] == "eof":
                return self.file
            word = token[1]
            if word == "syntax" or word == "option":
                self._skip_statement()
            elif word == "package":
                self.file["package"] = self._ident()
                self._expect(";")
            elif word == "import":
                if self._peek()[1] in ("public", "weak"):
                    self._next()
                self.file["imports"].append(self._next()[1][1:-1])
                self._expect(";")
            elif word == "message":
                self._message(self.file["package"], doc)
            elif word == "enum":
                self._enum(self.file["package"], doc)
            elif word == "service":
                self._service(doc)
            elif word == "extend":
                self._ident()
                self._block_skip()
            elif word == ";":
                continue
            else:
                self._fail("a declaration", token)

    def _block_skip(self):
        self._expect("{")
        depth = 1
        while depth:
            token = self._next()
            if token[0] == "eof":
                self._fail("'}'", token)
            depth += {"{": 1, "}": -1}.get(token[1], 0)

    def _service(self, doc):
        name = self._ident()
        rpcs = {}
        self.file["services"][name] = {"doc": doc, "rpcs": rpcs}
        self._expect("{")
        while True:
            doc = self._doc()
            token = self._next()
            if token[1] == "}":
                return
            if token[1] == "option":
                self._skip_statement()
            elif token[1] == "rpc":
                rpc_name = self._ident()
                rpcs[rpc_name] = self._rpc(doc)
            elif token[1] != ";":
                self._fail("an rpc", token)

    def _rpc_side(self):
        self._expect("(")
        stream = self._peek()[1] == "stream"
        if stream:
            self._next()
        name = self._ident()
        self._expect(")")
        return name, stream

    def _rpc(self, doc):
        request, client_stream = self._rpc_side()
        self._expect("returns")
        response, server_stream = self._rpc_side()
        options = {}
        token = self._next()
        if token[1] == "{":
            while True:
                token = self._next()
                if token[1] == "}":
                    break
                if token[1] == "option":
                    name = self._option_name()
                    self._expect("=")
                    options[name] = self._value()
                    self._expect(";")
                elif token[1] != ";":
                    self._fail("an option", token)
        elif token[1] != ";":
            self._fail("';' or '{'", token)
        auth = {}
        for name, value in options.items():
            if name.endswith(".authorization)") and isinstance(value, dict):
                auth = value
        return {
            "doc": doc,
            "request": request,
            "response": response,
            "client_stream": client_stream,
            "server_stream": server_stream,
            "auth": auth,
        }

    def _enum(self, scope, doc):
        name = self._ident()
        full = "%s.%s" % (scope, name) if scope else name
        values = {}
        self.file["enums"][full] = {"doc": doc, "values": values}
        self._expect("{")
        while True:
            doc = self._doc()
            token = self._next()
            if token[1] == "}":
                return
            if token[1] in ("option", "reserved"):
                self._skip_statement()
            elif token[0] == "ident":
                self._expect("=")
                number = self._int()
                self._bracket_options()
                end = self._expect(";")
                values[token[1]] = {"number": number, "doc": doc or self._trailing_doc(end[2])}
            elif token[1] != ";":
                self._fail("an enum value", token)

    def _message(self, scope, doc):
        name = self._ident()
        full = "%s.%s" % (scope, name) if scope else name
        fields = {}
        self.file["messages"][full] = {"doc": doc, "fields": fields}
        self._expect("{")
        self._message_body(full, fields, None)

    def _message_body(self, full, fields, oneof):
        while True:
            doc = self._doc()
            token = self._next()
            word = token[1]
            if word == "}":
                return
            if word == ";":
                continue
            if oneof is None and word == "message" and self._peek()[0] == "ident":
                self._message(full, doc)
            elif oneof is None and word == "enum" and self._peek()[0] == "ident":
                self._enum(full, doc)
            elif oneof is None and word == "oneof":
                group = self._ident()
                self._expect("{")
                self._message_body(full, fields, group)
            elif word in ("option", "reserved", "extensions") and self._peek()[1] != "=":
                self._skip_statement()
            elif token[0] == "ident":
                self._field(token, fields, oneof, doc)
            else:
                self._fail("a field", token)

    def _field(self, token, fields, oneof, doc):
        label, key = "", ""
        word = token[1]
        if word in ("repeated", "optional", "required") and self._peek()[0] == "ident":
            label, word = word, self._ident()
        if word == "map" and self._peek()[1] == "<":
            self._next()
            key = self._ident()
            self._expect(",")
            word = self._ident()
            self._expect(">")
            label = "map"
        name = self._ident()
        self._expect("=")
        number = self._int()
        options = self._bracket_options()
        end = self._expect(";")
        fields[name] = {
            "doc": doc or self._trailing_doc(end[2]),
            "number": number,
            "type": word,
            "label": label,
            "key": key,
            "oneof": oneof or "",
            "secret": any(k.endswith(".secret)") and v == "true" for k, v in options.items()),
            "deprecated": options.get("deprecated") == "true",
        }


def parse(text, name="<proto>"):
    """One .proto file as plain dicts; type names are as written, not resolved."""
    return _Parser(text, name).parse()


def load(read, root=ROOT):
    """`root` and every file it imports, transitively.

    `read(name)` returns a file's text, or None when the tag has no such file.
    Imports of google/protobuf are well-known types and are not read.
    """
    files, queue = {}, [root]
    while queue:
        name = queue.pop(0)
        if name in files or name.startswith("google/"):
            continue
        text = read(name)
        if text is None:
            if name == root:
                raise ProtoError("%s: not found" % name)
            continue
        files[name] = parse(text, name)
        queue.extend(files[name]["imports"])
    return files


def git_reader(repo, ref):
    """`read` for `load`, over a clone that has `ref`: proto/<name> as of that ref.

    Reads with `git show`, never from the working tree, so a clone that is
    checked out somewhere else, or has local edits, gives the same answer.
    """

    def read(name):
        done = subprocess.run(
            ["git", "-C", repo, "show", "%s:proto/%s" % (ref, name)],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            universal_newlines=True,
        )
        return done.stdout if done.returncode == 0 else None

    return read


def _resolve(name, scope, known):
    """The full name a type reference means, from the scope it is written in."""
    if name in SCALARS:
        return name
    if name.startswith("."):
        return name[1:]
    parts = scope.split(".") if scope else []
    while True:
        candidate = ".".join(parts + [name])
        if candidate in known:
            return candidate
        if not parts:
            return name
        parts.pop()


def surface(files):
    """What a signed-in person can reach, as one flat document.

    rpcs:      "Service.Method" -> request, response, streaming, auth, user
    messages:  full name -> fields, for every message a user RPC reaches;
               each has `sides`, "request", "response" or both
    enums:     full name -> values, for every enum one of those fields uses
    internal:  the RPCs left out, with the mode that excludes each
    """
    messages, enums = {}, {}
    for parsed in files.values():
        messages.update(parsed["messages"])
        enums.update(parsed["enums"])
    known = set(messages) | set(enums)

    resolved = {}
    for full, message in messages.items():
        fields = {}
        for name, field in message["fields"].items():
            field = dict(field)
            field["type"] = _resolve(field["type"], full, known)
            fields[name] = field
        resolved[full] = {"doc": message["doc"], "fields": fields}

    out = {"rpcs": {}, "messages": {}, "enums": {}, "internal": {}}

    def reach(start, side):
        queue = [start]
        while queue:
            full = queue.pop()
            if full in enums:
                out["enums"].setdefault(full, enums[full])
                continue
            if full not in resolved:
                continue
            entry = out["messages"].setdefault(full, dict(resolved[full], sides=[]))
            if side in entry["sides"]:
                continue
            entry["sides"] = sorted(entry["sides"] + [side])
            queue.extend(field["type"] for field in resolved[full]["fields"].values())

    for parsed in files.values():
        package = parsed["package"]
        for service_name, service in parsed["services"].items():
            for rpc_name, rpc in service["rpcs"].items():
                key = "%s.%s" % (service_name, rpc_name)
                # An RPC with no rule is kept: new and unclassified is
                # something a person should see, not something to drop.
                mode = rpc["auth"].get("auth_mode", "")
                user = not mode or mode in USER_AUTH_MODES
                entry = dict(rpc)
                entry["request"] = _resolve(rpc["request"], package, known)
                entry["response"] = _resolve(rpc["response"], package, known)
                if not user:
                    out["internal"][key] = mode
                    continue
                out["rpcs"][key] = entry
                reach(entry["request"], "request")
                reach(entry["response"], "response")
    return out
