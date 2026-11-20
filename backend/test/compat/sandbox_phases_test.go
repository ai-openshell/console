//go:build compat

package compat

import (
	"bytes"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// suspendResumeCapability is what a compute driver advertises when it can
// keep a sandbox's memory state across a suspend
// (openshell_core::extension_protocol::COMPUTE_SUSPEND_RESUME). The gateway
// refuses SuspendSandbox and ResumeSandbox outright when its driver does not.
const suspendResumeCapability = "openshell.compute.suspend-resume"

// computeDriverSuspends reports whether the gateway's compute driver
// advertises suspend and resume, read from the extensions GetGatewayInfo
// lists.
func computeDriverSuspends(t *testing.T) bool {
	t.Helper()
	var info struct {
		Extensions []struct {
			Kind                  string   `json:"kind"`
			SupportedCapabilities []string `json:"supportedCapabilities"`
		} `json:"extensions"`
	}
	mustJSON(t, http.MethodGet, "/api/v1/gateway", nil, &info, http.StatusOK)
	for _, extension := range info.Extensions {
		if extension.Kind == "COMPUTE_DRIVER" && slices.Contains(extension.SupportedCapabilities, suspendResumeCapability) {
			return true
		}
	}
	return false
}

// refused reports a failure unless a lifecycle call was refused as a conflict
// whose message contains want, in the gateway's own words.
func refused(t *testing.T, what, path string, body any, want string) {
	t.Helper()
	status, raw, err := do(http.MethodPost, path, body)
	if err != nil {
		t.Fatalf("POST %s [gateway %s]: %v", path, gatewayVersion, err)
	}
	var env apiError
	_ = json.Unmarshal(raw, &env)
	if status != http.StatusConflict || env.Code != "conflict" || !strings.Contains(env.Message, want) {
		t.Errorf("%s [gateway %s]: status = %d code = %q message = %q; want 409 conflict saying %q",
			what, gatewayVersion, status, env.Code, env.Message, want)
	}
}

// TestSandboxSuspendResume covers SuspendSandbox and ResumeSandbox, which
// arrived in gateway 0.1.6 with the SUSPENDING and SUSPENDED phases, in a
// workspace of its own.
//
// Both need a compute driver that advertises suspend and resume, and none of
// the drivers upstream ships at 0.1.9 does: the compat stack's Docker driver
// lists openshell.compute.contract and nothing else. So on this stack the
// test asserts the refusal, which is what the dashboard shows a person who
// asks for a suspend: a 409 in the gateway's words, and a sandbox that is
// still READY. The refusal proves that both calls reach the gateway and that
// the BFF relays its answer. It does not prove that the workspace arrives:
// the gateway checks the driver before it looks the sandbox up (measured on
// 0.1.9, 2026-11-20: a suspend of a sandbox in PROVISIONING gets the same
// answer), so the workspace is not what this run varies.
//
// The round trip below it runs only against a gateway whose driver advertises
// the capability. It is written from the gateway's source at v0.1.9
// (crates/openshell-server/src/compute/mod.rs) and has never run: there was
// no such gateway to run it against.
func TestSandboxSuspendResume(t *testing.T) {
	requireSandboxes(t)
	ws := newWorkspace(t)
	name := randName("su")
	createSandbox(t, ws, name, nil)
	waitForPhase(t, ws, name, "READY")

	if !computeDriverSuspends(t) {
		t.Run("the driver refuses a suspend", func(t *testing.T) {
			refused(t, "suspend on a driver without suspend", sandboxPath(ws, name)+"/suspend", nil,
				"does not support sandbox suspend")
		})
		t.Run("the driver refuses a resume", func(t *testing.T) {
			refused(t, "resume on a driver without suspend", sandboxPath(ws, name)+"/resume", nil,
				"does not support sandbox resume")
			// With the one option a resume has, to show that a body is taken.
			refused(t, "resume with restartIfRestoreFails on a driver without suspend", sandboxPath(ws, name)+"/resume",
				map[string]any{"restartIfRestoreFails": true}, "does not support sandbox resume")
		})
		t.Run("the sandbox is left as it was", func(t *testing.T) {
			if phase := getSandbox(t, ws, name).Status.Phase; phase != "READY" {
				t.Errorf("phase after a refused suspend and resume = %q, want READY [gateway %s]", phase, gatewayVersion)
			}
		})
		return
	}

	// A file written before the suspend: what a resume gives back is the
	// sandbox as it was.
	marker := []byte("written before suspend by " + name + "\n")
	mustUpload(t, ws, name, "", "marker.txt", marker)

	t.Run("suspend", func(t *testing.T) {
		var sb sandbox
		mustJSON(t, http.MethodPost, sandboxPath(ws, name)+"/suspend", nil, &sb, http.StatusOK)
		switch sb.Status.Phase {
		case "SUSPENDING", "SUSPENDED":
		default:
			t.Errorf("phase after suspend = %q, want SUSPENDING or SUSPENDED", sb.Status.Phase)
		}
		waitForPhase(t, ws, name, "SUSPENDED")
	})

	t.Run("a suspended sandbox is suspended in its own workspace only", func(t *testing.T) {
		wantError(t, http.MethodPost, sandboxPath("default", name)+"/resume", nil, http.StatusNotFound, "not_found")
		if phase := getSandbox(t, ws, name).Status.Phase; phase != "SUSPENDED" {
			t.Errorf("phase after a resume sent to another workspace = %q, want SUSPENDED", phase)
		}
	})

	t.Run("resume", func(t *testing.T) {
		var sb sandbox
		mustJSON(t, http.MethodPost, sandboxPath(ws, name)+"/resume",
			map[string]any{"restartIfRestoreFails": false}, &sb, http.StatusOK)
		switch sb.Status.Phase {
		case "STARTING", "READY":
		default:
			t.Errorf("phase after resume = %q, want STARTING or READY", sb.Status.Phase)
		}
		waitForPhase(t, ws, name, "READY")
	})

	t.Run("state survives the suspend", func(t *testing.T) {
		if got, _ := mustDownload(t, ws, name, "/sandbox/marker.txt"); !bytes.Equal(got, marker) {
			t.Errorf("marker after resume = %q, want %q", got, marker)
		}
	})
}

// TestSandboxStopAfterMainProcessExit covers the stop the dashboard offers a
// sandbox whose main process has exited: COMPLETED after status 0, ERROR
// after any other. The gateway takes a stop in both (QuiesceOperation::admit
// at v0.1.9), the sandbox goes to STOPPED and keeps its exit code, and a
// start from there runs the command again. That is the dashboard's way out of
// ERROR, which before had none but a delete.
//
// The start is sent from STOPPED and never straight from COMPLETED or ERROR,
// because that is what the dashboard sends. The gateway accepts a start in
// both, but against 0.1.9 with the Docker driver it then failed (measured
// 2026-11-20: UNAVAILABLE, "Docker supervisor exited before becoming ready",
// and the sandbox in ERROR with no exit code), so the dashboard does not
// offer it.
//
// It runs in a workspace of its own.
func TestSandboxStopAfterMainProcessExit(t *testing.T) {
	requireSandboxes(t)
	ws := newWorkspace(t)

	cases := []struct {
		name string
		// The phase the main process leaves the sandbox in, and its status.
		phase string
		exit  int32
	}{
		{name: "completed", phase: "COMPLETED", exit: 0},
		{name: "failed", phase: "ERROR", exit: 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			name := randName("ex")
			// Long enough for the sandbox to become READY first, as a
			// command a person gives one would.
			createSandbox(t, ws, name, map[string]any{
				"command": []string{"sh", "-c", "sleep 5; exit " + strconv.Itoa(int(tc.exit))},
				"tty":     false,
			})
			waitForExit(t, ws, name, tc.phase, tc.exit)

			var sb sandbox
			mustJSON(t, http.MethodPost, sandboxPath(ws, name)+"/stop", nil, &sb, http.StatusOK)
			switch sb.Status.Phase {
			case "STOPPING", "STOPPED":
			default:
				t.Errorf("phase after stopping a sandbox that was %s = %q, want STOPPING or STOPPED", tc.phase, sb.Status.Phase)
			}
			stopped := waitForPhase(t, ws, name, "STOPPED")
			// The list and the page show a stopped sandbox with an exit code
			// as the result of a run, not as one somebody stopped.
			if stopped.Status.ExitCode == nil || *stopped.Status.ExitCode != tc.exit {
				t.Errorf("exitCode of the stopped sandbox = %v, want %d: the stop no longer keeps the main process's status",
					exitCodeOf(stopped), tc.exit)
			}

			mustJSON(t, http.MethodPost, sandboxPath(ws, name)+"/start", nil, &sb, http.StatusOK)
			switch sb.Status.Phase {
			case "STARTING", "PROVISIONING", "READY":
			default:
				t.Errorf("phase after start = %q, want STARTING, PROVISIONING or READY", sb.Status.Phase)
			}
			// The command runs again and ends the way it did before.
			waitForExit(t, ws, name, tc.phase, tc.exit)
		})
	}
}

// waitForExit waits for a sandbox to reach the phase its main process leaves
// it in, and reports a failure unless it then carries that process's status.
func waitForExit(t *testing.T, workspace, name, phase string, exit int32) {
	t.Helper()
	sb, err := awaitPhase(workspace, name, phase, phaseLimit)
	if err != nil {
		t.Fatalf("[gateway %s] %v", gatewayVersion, err)
	}
	if sb.Status.ExitCode == nil || *sb.Status.ExitCode != exit {
		t.Fatalf("[gateway %s] sandbox %s/%s is %s with exitCode %v, want %d",
			gatewayVersion, workspace, name, phase, exitCodeOf(sb), exit)
	}
}

// exitCodeOf prints a sandbox's exit code, which it may not report.
func exitCodeOf(sb sandbox) any {
	if sb.Status.ExitCode == nil {
		return "none"
	}
	return *sb.Status.ExitCode
}
