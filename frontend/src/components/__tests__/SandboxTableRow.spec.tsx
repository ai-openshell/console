import React from 'react';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { Table, Tbody } from '@patternfly/react-table';

import SandboxTableRow from '../sandbox/SandboxTableRow';
import type {
  DraftSandboxSummary,
  Sandbox,
  SandboxSpec,
  SandboxStatus,
} from '../../types';

const CREATED_AT_MS = Date.UTC(2026, 9, 6, 14, 30, 0);

const makeSandbox = (
  status: Partial<SandboxStatus> = {},
  spec: Partial<SandboxSpec> = {},
): Sandbox => ({
  metadata: {
    id: 'sb-1',
    name: 'agent-1',
    workspace: 'team-a',
    createdAtMs: CREATED_AT_MS,
    resourceVersion: 1,
  },
  spec: {
    image: 'ghcr.io/nvidia/openshell-community/sandboxes/python:latest',
    policy: { version: 1, networkPolicies: {} },
    ...spec,
  },
  status: { phase: 'READY', currentPolicyVersion: 1, ...status },
});

type RowProps = Partial<React.ComponentProps<typeof SandboxTableRow>>;

const renderRow = (sandbox: Sandbox, props: RowProps = {}) =>
  render(
    <Table aria-label="Sandboxes">
      <Tbody>
        <SandboxTableRow
          sandbox={sandbox}
          rowIndex={0}
          isSelected={false}
          onSelect={jest.fn()}
          onDelete={jest.fn()}
          onStop={jest.fn()}
          onStart={jest.fn()}
          onSuspend={jest.fn()}
          onResume={jest.fn()}
          onViewLogs={jest.fn()}
          {...props}
        />
      </Tbody>
    </Table>,
  );

// The titles of the row's actions menu, opened the way a user opens it.
const actionTitles = (): string[] => {
  fireEvent.click(screen.getByRole('button', { name: /kebab toggle/i }));
  return screen.getAllByRole('menuitem').map((item) => item.textContent ?? '');
};

const summary = (
  overrides: Partial<DraftSandboxSummary> = {},
): DraftSandboxSummary => ({
  workspace: 'team-a',
  sandboxName: 'agent-1',
  pendingCount: 3,
  hasSecurityFlags: false,
  latestDraftMs: CREATED_AT_MS,
  ...overrides,
});

describe('SandboxTableRow', () => {
  describe('creation time', () => {
    beforeEach(() => {
      jest.useFakeTimers().setSystemTime(CREATED_AT_MS + 3 * 60 * 60 * 1000);
    });
    afterEach(() => {
      jest.useRealTimers();
    });

    it('shows when the sandbox was created, and how long ago', () => {
      renderRow(makeSandbox());
      const created = screen.getByTestId('sandbox-created-agent-1');
      // The text is in the viewer's locale and time zone. The instant it
      // stands for is not.
      expect(created.querySelector('time')).toHaveAttribute(
        'datetime',
        new Date(CREATED_AT_MS).toISOString(),
      );
      expect(created).toHaveTextContent(/2026/);
      expect(screen.getByText('3h 0m ago')).toBeInTheDocument();
    });

    it('shows a dash for a sandbox without a creation time', () => {
      const sandbox = makeSandbox();
      sandbox.metadata.createdAtMs = 0;
      renderRow(sandbox);
      expect(
        screen.queryByTestId('sandbox-created-agent-1'),
      ).not.toBeInTheDocument();
      expect(screen.getByRole('row')).toHaveTextContent(/-$/);
    });
  });

  describe('image', () => {
    it('shows a short image reference whole', () => {
      renderRow(makeSandbox());
      expect(screen.getByTestId('sandbox-image-agent-1')).toHaveTextContent(
        'python:latest',
      );
    });

    // An image pinned by digest is one unbroken word of some eighty
    // characters, which used to push the table wider than the page.
    it('cuts an image pinned by digest down to a length the table has room for', () => {
      const digest =
        'sha256:aeef1c63f00e2913ea002ccb3aaf925f338b5c5d70e63576f0d95c16a138044e';
      renderRow(
        makeSandbox(
          {},
          {
            image: `ghcr.io/nvidia/openshell-community/sandboxes/base:latest@${digest}`,
          },
        ),
      );
      const cell = screen.getByTestId('sandbox-image-agent-1');
      const shown = cell.querySelector('.pf-v6-c-truncate');
      expect(shown).not.toBeNull();
      // What is on screen: the start and the end of the reference, with
      // the middle left out.
      const visible = Array.from(
        cell.querySelectorAll(
          '.pf-v6-c-truncate__start, .pf-v6-c-truncate__end, .pf-v6-c-truncate__text',
        ),
      )
        .map((part) => part.textContent ?? '')
        .join('');
      expect(visible.length).toBeGreaterThan(0);
      expect(visible.length).toBeLessThan(`base:latest@${digest}`.length);
      expect(visible).toMatch(/^base:latest@/);
      expect(visible).toMatch(/138044e$/);
    });

    it('shows a dash for a sandbox without an image', () => {
      renderRow(makeSandbox({}, { image: '' }));
      expect(screen.getByTestId('sandbox-image-agent-1')).toHaveTextContent(
        '-',
      );
    });
  });

  describe('pending proposals', () => {
    it('shows no badge for a sandbox with none pending', () => {
      renderRow(makeSandbox());
      expect(
        screen.queryByTestId('sandbox-pending-agent-1'),
      ).not.toBeInTheDocument();
      expect(
        screen.queryByTestId('sandbox-pending-unavailable-agent-1'),
      ).not.toBeInTheDocument();
    });

    it('shows how many rules are pending beside the name', () => {
      renderRow(makeSandbox(), { draftSummary: summary() });
      expect(screen.getByTestId('sandbox-pending-agent-1')).toHaveTextContent(
        /^3 pending rules$/,
      );
    });

    it('says "rule" for one', () => {
      renderRow(makeSandbox(), {
        draftSummary: summary({ pendingCount: 1 }),
      });
      expect(screen.getByTestId('sandbox-pending-agent-1')).toHaveTextContent(
        /^1 pending rule$/,
      );
    });

    it('says when the pending rules carry findings', () => {
      renderRow(makeSandbox(), {
        draftSummary: summary({ pendingCount: 2, hasSecurityFlags: true }),
      });
      expect(screen.getByTestId('sandbox-pending-agent-1')).toHaveTextContent(
        '2 pending rules, with findings',
      );
    });

    it('opens the proposals when the badge is clicked', () => {
      const onReviewDrafts = jest.fn();
      renderRow(makeSandbox(), { draftSummary: summary(), onReviewDrafts });
      fireEvent.click(
        within(screen.getByTestId('sandbox-pending-agent-1')).getByRole(
          'button',
        ),
      );
      expect(onReviewDrafts).toHaveBeenCalledTimes(1);
    });

    it('says the proposals are unavailable instead of showing none', () => {
      renderRow(makeSandbox(), {
        draftSummary: summary({ pendingCount: 0, unavailable: true }),
      });
      expect(
        screen.getByTestId('sandbox-pending-unavailable-agent-1'),
      ).toHaveTextContent('Proposals unavailable');
      expect(
        screen.queryByTestId('sandbox-pending-agent-1'),
      ).not.toBeInTheDocument();
    });

    it('shows no badge for an entry that counts none', () => {
      renderRow(makeSandbox(), {
        draftSummary: summary({ pendingCount: 0 }),
      });
      expect(
        screen.queryByTestId('sandbox-pending-agent-1'),
      ).not.toBeInTheDocument();
    });
  });

  describe('rejected configuration', () => {
    const rejected: Partial<SandboxStatus> = {
      phase: 'PROVISIONING',
      currentPolicyVersion: 0,
      conditions: [
        {
          type: 'ConfigurationReady',
          status: 'False',
          reason: 'ConfigurationInvalid',
          message: 'Effective configuration could not be activated',
        },
      ],
    };

    it('notes "Invalid config" on a sandbox that is not in ERROR', () => {
      renderRow(makeSandbox(rejected));
      expect(
        screen.getByTestId('sandbox-invalid-config-agent-1'),
      ).toHaveTextContent('Invalid config');
      // The phase is still what the gateway says it is.
      expect(screen.getByTestId('sandbox-status-agent-1')).toHaveTextContent(
        'PROVISIONING',
      );
    });

    it('has no such note on a sandbox that accepted its configuration', () => {
      renderRow(makeSandbox());
      expect(
        screen.queryByTestId('sandbox-invalid-config-agent-1'),
      ).not.toBeInTheDocument();
    });

    it('does not take a pending configuration for a rejected one', () => {
      renderRow(
        makeSandbox({
          phase: 'PROVISIONING',
          currentPolicyVersion: 0,
          conditions: [
            {
              type: 'ConfigurationReady',
              status: 'False',
              reason: 'ConfigurationPending',
            },
          ],
        }),
      );
      expect(
        screen.queryByTestId('sandbox-invalid-config-agent-1'),
      ).not.toBeInTheDocument();
    });
  });

  describe('status', () => {
    const status = () => screen.getByTestId('sandbox-status-agent-1');
    const DANGER = 'pf-v6-u-text-color-status-danger';

    it('shows a completed sandbox as completed, not as a failure', () => {
      renderRow(makeSandbox({ phase: 'COMPLETED', exitCode: 0 }));
      expect(status()).toHaveTextContent(/^Completed$/);
      expect(status()).not.toHaveClass(DANGER);
    });

    it('shows a stopped sandbox with an exit code as a failure', () => {
      renderRow(makeSandbox({ phase: 'STOPPED', exitCode: 143 }));
      expect(status()).toHaveTextContent('STOPPED (exit 143)');
      expect(status()).toHaveClass(DANGER);
    });

    it('shows a sandbox somebody stopped as stopped', () => {
      renderRow(makeSandbox({ phase: 'STOPPED' }));
      expect(status()).toHaveTextContent(/^STOPPED$/);
      expect(status()).not.toHaveClass(DANGER);
    });

    it('shows the reason of an error', () => {
      renderRow(
        makeSandbox({
          phase: 'ERROR',
          conditions: [
            { type: 'Ready', status: 'False', reason: 'MainProcessFailed' },
          ],
        }),
      );
      expect(status()).toHaveTextContent('MainProcessFailed');
      expect(status()).toHaveClass(DANGER);
    });
  });

  describe('actions', () => {
    // A start of a completed sandbox is accepted by gateway 0.1.9 and, with
    // the Docker driver, then fails and leaves the sandbox in ERROR. A stop,
    // then a start, runs it again.
    it('offers a stop and no start on a completed sandbox', () => {
      renderRow(makeSandbox({ phase: 'COMPLETED', exitCode: 0 }));
      expect(actionTitles()).toEqual(['Logs', 'Stop', 'Delete']);
    });

    it('offers a stop on a sandbox in ERROR', () => {
      renderRow(makeSandbox({ phase: 'ERROR', exitCode: 3 }));
      expect(actionTitles()).toEqual(['Logs', 'Stop', 'Delete']);
    });

    it('offers a suspend and a stop on a ready sandbox', () => {
      renderRow(makeSandbox());
      expect(actionTitles()).toEqual(['Logs', 'Suspend', 'Stop', 'Delete']);
    });

    it('offers a resume and a stop, and no start, on a suspended sandbox', () => {
      renderRow(makeSandbox({ phase: 'SUSPENDED' }));
      expect(actionTitles()).toEqual(['Logs', 'Resume', 'Stop', 'Delete']);
    });

    it('offers a start on a stopped sandbox', () => {
      renderRow(makeSandbox({ phase: 'STOPPED' }));
      expect(actionTitles()).toEqual(['Logs', 'Start', 'Delete']);
    });

    it('offers none of them while the sandbox is provisioning', () => {
      renderRow(makeSandbox({ phase: 'PROVISIONING' }));
      expect(actionTitles()).toEqual(['Logs', 'Delete']);
    });

    it('offers a suspend and a resume only where the page handles them', () => {
      renderRow(makeSandbox(), { onSuspend: undefined });
      expect(actionTitles()).toEqual(['Logs', 'Stop', 'Delete']);
    });

    it.each([
      ['Start', 'STOPPED', 'onStart'],
      ['Stop', 'COMPLETED', 'onStop'],
      ['Suspend', 'READY', 'onSuspend'],
      ['Resume', 'SUSPENDED', 'onResume'],
    ] as const)('%s on a sandbox that is %s calls %s', (title, phase, prop) => {
      const handler = jest.fn();
      renderRow(makeSandbox({ phase }), { [prop]: handler });
      fireEvent.click(screen.getByRole('button', { name: /kebab toggle/i }));
      fireEvent.click(screen.getByRole('menuitem', { name: title }));
      expect(handler).toHaveBeenCalledTimes(1);
    });
  });
});
