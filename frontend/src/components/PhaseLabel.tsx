import { Label } from '@patternfly/react-core';
import {
  CheckCircleIcon,
  ExclamationCircleIcon,
  InProgressIcon,
  PauseCircleIcon,
  PowerOffIcon,
} from '@patternfly/react-icons';

import type { SandboxPhase, WorkspacePhase } from '../types';

type PhaseLabelProps = {
  phase: SandboxPhase | WorkspacePhase;
  // The main process exit code of a sandbox, when it reports one.
  exitCode?: number;
};

// Renders a sandbox or workspace lifecycle phase. Sandbox phases come from
// the SandboxPhase enum: PROVISIONING → READY | ERROR → DELETING, plus the
// stop/start cycle READY → STOPPING → STOPPED → STARTING → READY, the
// suspend/resume cycle READY → SUSPENDING → SUSPENDED → STARTING → READY, and
// COMPLETED for a sandbox whose main command exited with status 0.
//
// The colors follow `openshell sandbox list`: COMPLETED is a success like
// READY, a STOPPED sandbox that reports an exit code is shown as the failure
// it is, not as one somebody stopped, SUSPENDING is in progress and SUSPENDED
// is at rest.
const PhaseLabel: React.FC<PhaseLabelProps> = ({ phase, exitCode }) => {
  switch (phase) {
    case 'READY':
    case 'ACTIVE':
    case 'COMPLETED':
      return (
        <Label
          color="green"
          icon={<CheckCircleIcon />}
          data-testid="phase-label"
        >
          {phase}
        </Label>
      );
    case 'ERROR':
      return (
        <Label
          color="red"
          icon={<ExclamationCircleIcon />}
          data-testid="phase-label"
        >
          {phase}
        </Label>
      );
    case 'PROVISIONING':
    case 'STOPPING':
    case 'STARTING':
    case 'SUSPENDING':
      return (
        <Label color="blue" icon={<InProgressIcon />} data-testid="phase-label">
          {phase}
        </Label>
      );
    case 'STOPPED':
      if (exitCode !== undefined) {
        return (
          <Label
            color="red"
            icon={<ExclamationCircleIcon />}
            data-testid="phase-label"
          >
            {phase} (exit {exitCode})
          </Label>
        );
      }
      return (
        <Label color="grey" icon={<PowerOffIcon />} data-testid="phase-label">
          {phase}
        </Label>
      );
    case 'SUSPENDED':
      return (
        <Label
          color="grey"
          icon={<PauseCircleIcon />}
          data-testid="phase-label"
        >
          {phase}
        </Label>
      );
    case 'DELETING':
    case 'TERMINATING':
      return (
        <Label
          color="orange"
          icon={<InProgressIcon />}
          data-testid="phase-label"
        >
          {phase}
        </Label>
      );
    default:
      return (
        <Label color="grey" data-testid="phase-label">
          {phase}
        </Label>
      );
  }
};

export default PhaseLabel;
