"""Two small releases of an API shaped like upstream's, for the API tests.

OLD is "0.9.0" and NEW is "0.9.1". Between them NEW adds what OpenShell 0.1.6
added to 0.1.5 (suspend and resume, and two phases) and one of every other
kind of change the comparison has to name.
"""

OPTIONS = """
syntax = "proto3";
package openshell.options.v1;
import "google/protobuf/descriptor.proto";
message AuthorizationRule {
  string auth_mode = 1;
  string workspace_role = 2;
  string global_role = 3;
  string scope = 4;
}
extend google.protobuf.MethodOptions {
  AuthorizationRule authorization = 50000;
}
extend google.protobuf.FieldOptions {
  bool secret = 50001;
}
"""

DATAMODEL = """
syntax = "proto3";
package openshell.datamodel.v1;
// Which workspace a request is about.
message WorkspaceSelector {
  oneof scope {
    string workspace = 1;
    bool all_workspaces = 2;
  }
}
"""

OLD = """
// A licence header.
syntax = "proto3";

package openshell.v1;

import "datamodel.proto";
import "google/protobuf/timestamp.proto";
import "options.proto";

option go_package = "example.com/openshell/v1";

// The service.
service OpenShell {
  // Check the health of the service.
  rpc Health(HealthRequest) returns (HealthResponse) {
    option (openshell.options.v1.authorization) = {
      auth_mode: "unauthenticated"
    };
  }

  // Create a new sandbox.
  //
  // A second paragraph that is not the summary.
  rpc CreateSandbox(CreateSandboxRequest) returns (SandboxResponse) {
    option (openshell.options.v1.authorization) = {
      auth_mode: "bearer"
      scope: "sandbox:write"
      workspace_role: "user"
    };
  }

  // Stop a sandbox.
  rpc StopSandbox(StopSandboxRequest) returns (SandboxResponse) {
    option (openshell.options.v1.authorization) = {
      auth_mode: "bearer"
      scope: "sandbox:write"
      workspace_role: "user"
    };
  }

  // Follow a sandbox.
  rpc WatchSandbox(GetSandboxRequest) returns (stream SandboxResponse) {
    option (openshell.options.v1.authorization) = { auth_mode: "bearer", scope: "sandbox:read", workspace_role: "user" };
  }

  // Read or write a sandbox's settings.
  rpc GetSandboxConfig(GetSandboxRequest) returns (SandboxResponse) {
    option (openshell.options.v1.authorization) = { auth_mode: "dual" scope: "config:read" };
  }

  // Sandbox-only: report in.
  rpc ReportStatus(ReportStatusRequest) returns (HealthResponse) {
    option (openshell.options.v1.authorization) = { auth_mode: "sandbox" };
  }

  // Removed in the next release.
  rpc LegacyThing(HealthRequest) returns (HealthResponse) {
    option (openshell.options.v1.authorization) = { auth_mode: "bearer" };
  }
}

message HealthRequest {}
message HealthResponse { string status = 1; }

// Only a sandbox sends this.
message ReportStatusRequest {
  string internal_detail = 1;
}

message CreateSandboxRequest {
  openshell.datamodel.v1.WorkspaceSelector workspace_scope = 3;
  string name = 1;
  SandboxSpec spec = 2;
  reserved 4, 5;
  reserved "old_name";
}

message GetSandboxRequest {
  openshell.datamodel.v1.WorkspaceSelector workspace_scope = 2;
  string name = 1;
}

message StopSandboxRequest {
  openshell.datamodel.v1.WorkspaceSelector workspace_scope = 2;
  string name = 1;
}

// What a person asks for.
message SandboxSpec {
  // The image to run.
  string image = 1;
  map<string, string> environment = 2;
  repeated string command = 3;
  optional bool tty = 4;
  string token = 5 [(openshell.options.v1.secret) = true];
  string log_level = 6; // Trailing words about the level.
  int32 replicas = 7 [deprecated = false];

  // How it restarts.
  enum RestartPolicy {
    RESTART_POLICY_UNSPECIFIED = 0;
    RESTART_POLICY_NEVER = 1;
  }
  RestartPolicy restart_policy = 8;

  message Resources {
    string cpu = 1;
  }
  Resources resources = 9;
}

message SandboxResponse {
  Sandbox sandbox = 1;
}

message Sandbox {
  string name = 1;
  SandboxSpec spec = 2;
  SandboxPhase phase = 3;
  google.protobuf.Timestamp created = 4;
}

// Where a sandbox is in its life.
enum SandboxPhase {
  SANDBOX_PHASE_UNSPECIFIED = 0;
  // The sandbox answers.
  SANDBOX_PHASE_READY = 1;
  SANDBOX_PHASE_STOPPED = 2;
  SANDBOX_PHASE_GONE = 3;
}
"""

# What 0.9.1 changes, as (old text, new text) applied to OLD in order.
_CHANGES = [
    # Two RPCs added, as OpenShell 0.1.6 added them.
    (
        "  // Follow a sandbox.",
        """  // Freeze a ready sandbox and retain its memory state for a later resume.
  rpc SuspendSandbox(SuspendSandboxRequest) returns (SandboxResponse) {
    option (openshell.options.v1.authorization) = {
      auth_mode: "bearer"
      scope: "sandbox:write"
      workspace_role: "user"
    };
  }

  // Restore a suspended sandbox in a new runtime generation.
  rpc ResumeSandbox(ResumeSandboxRequest) returns (SandboxResponse) {
    option (openshell.options.v1.authorization) = {
      auth_mode: "bearer"
      scope: "sandbox:write"
      workspace_role: "user"
    };
  }

  // A new call only a peer gateway makes.
  rpc PeerNotify(HealthRequest) returns (HealthResponse) {
    option (openshell.options.v1.authorization) = { auth_mode: "peer" };
  }

  // Follow a sandbox.""",
    ),
    # One RPC removed.
    (
        """  // Removed in the next release.
  rpc LegacyThing(HealthRequest) returns (HealthResponse) {
    option (openshell.options.v1.authorization) = { auth_mode: "bearer" };
  }
""",
        "",
    ),
    # One RPC needs a higher role.
    (
        """  // Stop a sandbox.
  rpc StopSandbox(StopSandboxRequest) returns (SandboxResponse) {
    option (openshell.options.v1.authorization) = {
      auth_mode: "bearer"
      scope: "sandbox:write"
      workspace_role: "user"
    };""",
        """  // Stop a sandbox.
  rpc StopSandbox(StopSandboxRequest) returns (SandboxResponse) {
    option (openshell.options.v1.authorization) = {
      auth_mode: "bearer"
      scope: "sandbox:write"
      workspace_role: "admin"
    };""",
    ),
    # New request messages.
    (
        "message StopSandboxRequest {",
        """message SuspendSandboxRequest {
  openshell.datamodel.v1.WorkspaceSelector workspace_scope = 2;
  string name = 1;
}

message ResumeSandboxRequest {
  openshell.datamodel.v1.WorkspaceSelector workspace_scope = 2;
  string name = 1;
  bool restart_if_restore_fails = 3;
}

message StopSandboxRequest {""",
    ),
    # A field a client can send, a rename, a type change and a removal.
    (
        "  // The image to run.\n  string image = 1;",
        "  // The image to run.\n  string image = 1;\n  // Seconds of idleness after which the sandbox is suspended.\n  uint32 idle_timeout = 10;",
    ),
    ("  string log_level = 6; // Trailing words about the level.", "  string verbosity = 6;"),
    ("  int32 replicas = 7 [deprecated = false];", "  int64 replicas = 7;"),
    ("  repeated string command = 3;\n", ""),
    # A field only the gateway returns.
    ("  google.protobuf.Timestamp created = 4;", "  google.protobuf.Timestamp created = 4;\n  // Why the sandbox is where it is.\n  string reason = 5;"),
    # A field on a message only a sandbox sends: not user-facing.
    ("  string internal_detail = 1;", "  string internal_detail = 1;\n  string more_internal_detail = 2;"),
    # Two phases added, one removed.
    (
        "  SANDBOX_PHASE_STOPPED = 2;\n  SANDBOX_PHASE_GONE = 3;",
        """  SANDBOX_PHASE_STOPPED = 2;
  // The gateway requested suspension and the driver has not confirmed it.
  SANDBOX_PHASE_SUSPENDING = 4;
  // The workload is frozen and its memory state is retained.
  SANDBOX_PHASE_SUSPENDED = 5;""",
    ),
]


def _new():
    text = OLD
    for before, after in _CHANGES:
        if before not in text:
            raise AssertionError("fixture drifted: %r is not in OLD" % before)
        text = text.replace(before, after)
    return text


NEW = _new()

OLD_VERSION, NEW_VERSION = "0.9.0", "0.9.1"


def files(openshell):
    return {"openshell.proto": openshell, "options.proto": OPTIONS, "datamodel.proto": DATAMODEL}


def by_version():
    """What FakeUpstream serves as `protos`."""
    return {OLD_VERSION: files(OLD), NEW_VERSION: files(NEW)}
