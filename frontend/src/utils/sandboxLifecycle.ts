import type { Sandbox, SandboxPhase } from '../types';

// Which lifecycle action is offered in which phase. Shared by the list row
// actions and the detail page header so the two surfaces never disagree.
//
// The rules are the gateway's own (QuiesceOperation::admit and
// start_sandbox_with_intent in crates/openshell-server/src/compute/mod.rs at
// v0.1.9), as far as a run against gateway 0.1.9 with the Docker driver bore
// them out on 2026-11-20. Where the two differ, the comment says so.

// Stop. The gateway takes it from READY, COMPLETED, ERROR, SUSPENDING and
// SUSPENDED, and refuses it in PROVISIONING. Measured: a completed sandbox
// and one in ERROR both go to STOPPED and keep their exit code, and a start
// from there runs them again, so a stop is the way out of ERROR. A stop of a
// suspended sandbox, or of a suspend that has not finished, gives up the
// memory state it kept (read, not measured: the Docker driver cannot
// suspend). The gateway also takes a stop while it is restarting a main
// process by itself; that is a STARTING the phase alone does not tell from
// any other, so it is not offered.
//
// One stop was measured to remove the sandbox: of a sandbox in ERROR because
// its image could not be pulled, which never had a container. The gateway
// answers 404 and the sandbox is gone.
export const canStopSandbox = (phase: SandboxPhase): boolean =>
  phase === 'READY' ||
  phase === 'COMPLETED' ||
  phase === 'ERROR' ||
  phase === 'SUSPENDING' ||
  phase === 'SUSPENDED';

// Start. Offered from STOPPED only, which is less than the gateway takes:
//
// - COMPLETED, and an ERROR whose main process exited with a failure. The
//   gateway accepts both, but on 0.1.9 with the Docker driver the start then
//   fails ("Docker supervisor exited before becoming ready", a 502 here) and
//   leaves the sandbox in ERROR. A stop first, then a start, works, and that
//   is what both phases are offered.
// - SUSPENDED. The gateway boots the sandbox afresh and discards its memory
//   state. Beside Resume that reads as the same thing and is not; a stop, then
//   a start, says what it does.
//
// Any other ERROR the gateway refuses, with "stop the sandbox before retrying
// start".
export const canStartSandbox = (phase: SandboxPhase): boolean =>
  phase === 'STOPPED';

// Suspend: from READY only. The gateway refuses it outright when its compute
// driver does not advertise openshell.compute.suspend-resume, which none of
// the drivers upstream ships at 0.1.9 does. Only a platform admin can read a
// driver's capabilities (GET /gateway), so the action is offered to everyone
// and the gateway's refusal is shown.
export const canSuspendSandbox = (phase: SandboxPhase): boolean =>
  phase === 'READY';

// Resume: from SUSPENDED only. Such a sandbox exists only on a gateway whose
// driver can suspend.
export const canResumeSandbox = (phase: SandboxPhase): boolean =>
  phase === 'SUSPENDED';

// How a sandbox's phase reads at a glance, following `openshell sandbox list`:
// READY and COMPLETED are both good news, ERROR is not, and neither is a
// STOPPED sandbox that reports an exit code. A sandbox somebody stopped
// reports none. SUSPENDING is work in progress, as PROVISIONING is, and a
// SUSPENDED sandbox is at rest, as a stopped one is.
export type SandboxPhaseTone =
  'success' | 'danger' | 'progress' | 'deleting' | 'stopped' | 'unknown';

export const sandboxPhaseTone = (
  phase: SandboxPhase,
  exitCode?: number,
): SandboxPhaseTone => {
  switch (phase) {
    case 'READY':
    case 'COMPLETED':
      return 'success';
    case 'ERROR':
      return 'danger';
    case 'STOPPED':
      return exitCode === undefined ? 'stopped' : 'danger';
    case 'PROVISIONING':
    case 'STOPPING':
    case 'STARTING':
    case 'SUSPENDING':
      return 'progress';
    case 'SUSPENDED':
      return 'stopped';
    case 'DELETING':
      return 'deleting';
    default:
      return 'unknown';
  }
};

// A sandbox whose supervisor refused the configuration it was given.
export type ConfigurationRejection = {
  // Why, in the gateway's words, when it gave any.
  message?: string;
};

const collapseWhitespace = (text?: string): string | undefined =>
  text?.split(/\s+/).filter(Boolean).join(' ') || undefined;

// Whether the sandbox rejected its configuration, which the TUI notes as
// "Invalid config": the condition ConfigurationReady, or Ready, is False with
// the reason ConfigurationInvalid. The gateway derives both from the
// configuration admission the sandbox reported, so a REJECTED admission says
// the same and is read too.
//
// The phase does not say it: the gateway keeps a sandbox whose configuration
// is not accepted in PROVISIONING, not ERROR. The admission's error is the
// reason, and the conditions repeat it.
export const getConfigurationRejection = (
  sandbox: Sandbox,
): ConfigurationRejection | undefined => {
  const { conditions, configurationAdmission } = sandbox.status;
  const condition = conditions?.find(
    (candidate) =>
      (candidate.type === 'ConfigurationReady' || candidate.type === 'Ready') &&
      candidate.status === 'False' &&
      candidate.reason === 'ConfigurationInvalid',
  );
  const rejected = configurationAdmission?.state === 'REJECTED';
  if (!condition && !rejected) {
    return undefined;
  }
  return {
    message:
      (rejected
        ? collapseWhitespace(configurationAdmission?.error)
        : undefined) ?? collapseWhitespace(condition?.message),
  };
};

// Whether the gateway has a policy revision to show for the sandbox. It
// writes the first one when the sandbox's supervisor first asks for its
// configuration, and until then answers a request for the sandbox's policy
// with "no policy revision found" (a 404). A sandbox that reports a policy
// version has one. The list asks only for those, so that a sandbox that was
// just created is not asked for a policy it does not have yet; the sandbox
// list is polled, and the policy is read once the version is there.
export const hasPolicyRevision = (sandbox: Sandbox): boolean =>
  sandbox.status.currentPolicyVersion > 0;
