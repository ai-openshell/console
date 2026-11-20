import {
  canResumeSandbox,
  canStartSandbox,
  canStopSandbox,
  canSuspendSandbox,
  getConfigurationRejection,
  hasPolicyRevision,
  sandboxPhaseTone,
  type SandboxPhaseTone,
} from '../sandboxLifecycle';
import type { Sandbox, SandboxPhase, SandboxStatus } from '../../types';

const ALL_PHASES: SandboxPhase[] = [
  'PROVISIONING',
  'READY',
  'ERROR',
  'DELETING',
  'STOPPING',
  'STOPPED',
  'STARTING',
  'COMPLETED',
  'SUSPENDING',
  'SUSPENDED',
  'UNKNOWN',
  'UNSPECIFIED',
];

const sandboxWith = (status: Partial<SandboxStatus>): Sandbox => ({
  metadata: {
    id: 'sb-1',
    name: 'agent-1',
    workspace: 'team-a',
    createdAtMs: 1_700_000_000_000,
    resourceVersion: 1,
  },
  spec: { image: 'base', policy: { version: 1, networkPolicies: {} } },
  status: { phase: 'READY', currentPolicyVersion: 1, ...status },
});

describe('lifecycle actions', () => {
  // What gateway 0.1.9 takes, as far as it was seen to work with the Docker
  // driver: a stop from Ready, Completed and Error (and, read from its source,
  // from Suspending and Suspended), a start from Stopped. It accepts a start
  // of a completed sandbox too, and that start then fails and leaves the
  // sandbox in Error, so it is not offered.
  it.each<[SandboxPhase, boolean, boolean]>([
    ['READY', false, true],
    ['STOPPED', true, false],
    ['COMPLETED', false, true],
    ['ERROR', false, true],
    ['SUSPENDING', false, true],
    ['SUSPENDED', false, true],
    ['PROVISIONING', false, false],
    ['STARTING', false, false],
    ['STOPPING', false, false],
    ['DELETING', false, false],
    ['UNKNOWN', false, false],
    ['UNSPECIFIED', false, false],
  ])('%s: start %s, stop %s', (phase, canStart, canStop) => {
    expect(canStartSandbox(phase)).toBe(canStart);
    expect(canStopSandbox(phase)).toBe(canStop);
  });

  it('never offers both for one phase', () => {
    for (const phase of ALL_PHASES) {
      expect(canStartSandbox(phase) && canStopSandbox(phase)).toBe(false);
    }
  });

  it('offers a suspend for a ready sandbox only', () => {
    expect(ALL_PHASES.filter(canSuspendSandbox)).toEqual(['READY']);
  });

  it('offers a resume for a suspended sandbox only', () => {
    expect(ALL_PHASES.filter(canResumeSandbox)).toEqual(['SUSPENDED']);
  });

  // A start boots a suspended sandbox afresh and discards the memory state a
  // resume would restore.
  it('does not offer a start beside the resume', () => {
    expect(canStartSandbox('SUSPENDED')).toBe(false);
  });

  it('leaves every phase but the transitional ones a way on', () => {
    const stuck = ALL_PHASES.filter(
      (phase) =>
        !canStartSandbox(phase) &&
        !canStopSandbox(phase) &&
        !canSuspendSandbox(phase) &&
        !canResumeSandbox(phase),
    );
    expect(stuck).toEqual([
      'PROVISIONING',
      'DELETING',
      'STOPPING',
      'STARTING',
      'UNKNOWN',
      'UNSPECIFIED',
    ]);
  });
});

describe('sandboxPhaseTone', () => {
  it.each<[SandboxPhase, SandboxPhaseTone]>([
    ['READY', 'success'],
    ['COMPLETED', 'success'],
    ['ERROR', 'danger'],
    ['PROVISIONING', 'progress'],
    ['STARTING', 'progress'],
    ['STOPPING', 'progress'],
    ['SUSPENDING', 'progress'],
    ['DELETING', 'deleting'],
    ['STOPPED', 'stopped'],
    ['SUSPENDED', 'stopped'],
    ['UNKNOWN', 'unknown'],
    ['UNSPECIFIED', 'unknown'],
  ])('%s is %s', (phase, tone) => {
    expect(sandboxPhaseTone(phase)).toBe(tone);
  });

  it('reads a stopped sandbox that reports an exit code as a failure', () => {
    expect(sandboxPhaseTone('STOPPED', 143)).toBe('danger');
    // The CLI's rule is that an exit code is there, not what it is.
    expect(sandboxPhaseTone('STOPPED', 0)).toBe('danger');
  });

  it('does not let an exit code change any other phase', () => {
    expect(sandboxPhaseTone('COMPLETED', 0)).toBe('success');
    expect(sandboxPhaseTone('READY', 1)).toBe('success');
    expect(sandboxPhaseTone('ERROR', 0)).toBe('danger');
  });
});

describe('getConfigurationRejection', () => {
  const rejectedMessage =
    'Effective configuration could not be activated; replace the policy or repair attached providers';

  it('is nothing for a sandbox that accepted its configuration', () => {
    expect(
      getConfigurationRejection(
        sandboxWith({
          conditions: [
            { type: 'Ready', status: 'True', reason: 'DependenciesReady' },
            {
              type: 'ConfigurationReady',
              status: 'True',
              reason: 'ConfigurationAccepted',
            },
          ],
          configurationAdmission: {
            state: 'ACCEPTED',
            policyVersion: 1,
            configRevision: '1',
            providerEnvRevision: '2',
          },
        }),
      ),
    ).toBeUndefined();
  });

  it('is nothing for a sandbox with no conditions at all', () => {
    expect(getConfigurationRejection(sandboxWith({}))).toBeUndefined();
  });

  // What gateway 0.1.2 reports for a sandbox it holds in PROVISIONING.
  it('finds the rejection of a sandbox that is still PROVISIONING', () => {
    const rejection = getConfigurationRejection(
      sandboxWith({
        phase: 'PROVISIONING',
        currentPolicyVersion: 0,
        conditions: [
          {
            type: 'ConfigurationReady',
            status: 'False',
            reason: 'ConfigurationInvalid',
            message: rejectedMessage,
          },
          {
            type: 'Ready',
            status: 'False',
            reason: 'ConfigurationInvalid',
            message: rejectedMessage,
          },
        ],
        configurationAdmission: {
          state: 'REJECTED',
          policyVersion: 0,
          configRevision: '1',
          providerEnvRevision: '2',
          error: rejectedMessage,
        },
      }),
    );
    expect(rejection).toEqual({ message: rejectedMessage });
  });

  it.each(['ConfigurationReady', 'Ready'])(
    'reads the %s condition whatever the phase',
    (type) => {
      for (const phase of ALL_PHASES) {
        expect(
          getConfigurationRejection(
            sandboxWith({
              phase,
              conditions: [
                {
                  type,
                  status: 'False',
                  reason: 'ConfigurationInvalid',
                  message: 'credentialed endpoint requires L7 inspection',
                },
              ],
            }),
          ),
        ).toEqual({ message: 'credentialed endpoint requires L7 inspection' });
      }
    },
  );

  it('reads a REJECTED admission that no condition repeats', () => {
    expect(
      getConfigurationRejection(
        sandboxWith({
          configurationAdmission: {
            state: 'REJECTED',
            policyVersion: 2,
            configRevision: '1',
            providerEnvRevision: '2',
            error: 'policy v2 names an unknown provider',
          },
        }),
      ),
    ).toEqual({ message: 'policy v2 names an unknown provider' });
  });

  it('prefers the admission error and falls back to the condition', () => {
    const conditions = [
      {
        type: 'ConfigurationReady',
        status: 'False',
        reason: 'ConfigurationInvalid',
        message: 'from the condition',
      },
    ];
    expect(
      getConfigurationRejection(
        sandboxWith({
          conditions,
          configurationAdmission: {
            state: 'REJECTED',
            policyVersion: 0,
            configRevision: '1',
            providerEnvRevision: '2',
            error: 'from the admission',
          },
        }),
      ),
    ).toEqual({ message: 'from the admission' });
    expect(
      getConfigurationRejection(
        sandboxWith({
          conditions,
          configurationAdmission: {
            state: 'REJECTED',
            policyVersion: 0,
            configRevision: '1',
            providerEnvRevision: '2',
          },
        }),
      ),
    ).toEqual({ message: 'from the condition' });
  });

  it('puts a message of several lines on one', () => {
    expect(
      getConfigurationRejection(
        sandboxWith({
          conditions: [
            {
              type: 'Ready',
              status: 'False',
              reason: 'ConfigurationInvalid',
              message: 'endpoint  requires\n  L7 inspection ',
            },
          ],
        }),
      ),
    ).toEqual({ message: 'endpoint requires L7 inspection' });
  });

  it('is a rejection without a message when the gateway gave none', () => {
    expect(
      getConfigurationRejection(
        sandboxWith({
          conditions: [
            { type: 'Ready', status: 'False', reason: 'ConfigurationInvalid' },
          ],
        }),
      ),
    ).toEqual({ message: undefined });
  });

  it.each([
    // Not accepted yet is not rejected.
    {
      type: 'ConfigurationReady',
      status: 'False',
      reason: 'ConfigurationPending',
    },
    // Another reason a sandbox is not ready.
    { type: 'Ready', status: 'False', reason: 'ContainerExited' },
    // The reason on a condition that is not False.
    { type: 'Ready', status: 'True', reason: 'ConfigurationInvalid' },
    // The gateway's condition for a later configuration the sandbox refused
    // while it keeps running the one it accepted: not what the TUI notes.
    {
      type: 'DesiredConfigurationReady',
      status: 'False',
      reason: 'ConfigurationInvalid',
    },
  ])('does not read $type=$status ($reason) as a rejection', (condition) => {
    expect(
      getConfigurationRejection(sandboxWith({ conditions: [condition] })),
    ).toBeUndefined();
  });

  it('does not read a PENDING admission as a rejection', () => {
    expect(
      getConfigurationRejection(
        sandboxWith({
          phase: 'PROVISIONING',
          configurationAdmission: {
            state: 'PENDING',
            policyVersion: 0,
            configRevision: '1',
            providerEnvRevision: '2',
          },
        }),
      ),
    ).toBeUndefined();
  });
});

describe('hasPolicyRevision', () => {
  // A sandbox as gateway 0.1.2 returns it right after the create: its
  // admission is already PENDING, and it has no policy revision yet.
  it('is false for a sandbox that was just created', () => {
    expect(
      hasPolicyRevision(
        sandboxWith({
          phase: 'PROVISIONING',
          currentPolicyVersion: 0,
          configurationAdmission: {
            state: 'PENDING',
            policyVersion: 0,
            configRevision: '1',
            providerEnvRevision: '2',
          },
        }),
      ),
    ).toBe(false);
  });

  it('is true once the sandbox reports a policy version', () => {
    expect(hasPolicyRevision(sandboxWith({ currentPolicyVersion: 1 }))).toBe(
      true,
    );
    expect(
      hasPolicyRevision(
        sandboxWith({ phase: 'STOPPED', currentPolicyVersion: 3 }),
      ),
    ).toBe(true);
  });
});
