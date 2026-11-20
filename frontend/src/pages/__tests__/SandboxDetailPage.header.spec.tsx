import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { AlertVariant } from '@patternfly/react-core';
import { MemoryRouter, Route, Routes } from 'react-router-dom';

import SandboxDetailPage from '../SandboxDetailPage';
import type { DeleteResult, Sandbox, SandboxStatus } from '../../types';

const CREATED_AT_MS = Date.UTC(2026, 9, 6, 14, 30, 0);

jest.mock('../../api/auth', () => ({
  useFeatureFlags: jest.fn(() => ({})),
}));

// What the delete mutation is given, and what the gateway then answers:
// a result, or an error.
type MutateOptions = { onSuccess?: (result: DeleteResult) => void };
let mockDeleteAnswer: DeleteResult | Error = {
  outcome: 'completed',
  deleted: true,
};
let mockDeleteState: { isPending: boolean; isError: boolean; error?: Error } = {
  isPending: false,
  isError: false,
};
const mockDeleteMutate = jest.fn((_name: string, options?: MutateOptions) => {
  if (!(mockDeleteAnswer instanceof Error)) {
    options?.onSuccess?.(mockDeleteAnswer);
  }
});
const mockDeleteReset = jest.fn();
const mockStartMutate = jest.fn();
const mockStopMutate = jest.fn();
const mockSuspendMutate = jest.fn();
const mockResumeMutate = jest.fn();

jest.mock('../../api/sandboxes', () => ({
  useSandbox: jest.fn(),
  useStartSandbox: jest.fn(() => ({
    mutate: mockStartMutate,
    isPending: false,
  })),
  useStopSandbox: jest.fn(() => ({ mutate: mockStopMutate, isPending: false })),
  useSuspendSandbox: jest.fn(() => ({
    mutate: mockSuspendMutate,
    isPending: false,
  })),
  useResumeSandbox: jest.fn(() => ({
    mutate: mockResumeMutate,
    isPending: false,
  })),
  useDeleteSandbox: jest.fn(() => ({
    mutate: mockDeleteMutate,
    reset: mockDeleteReset,
    ...mockDeleteState,
  })),
}));

jest.mock('../../api/policy', () => ({
  useDraftPolicy: jest.fn(() => ({ data: undefined })),
  useSandboxPolicy: jest.fn(() => ({ data: undefined })),
}));

jest.mock('../../api/providers', () => ({
  useProviderExpiry: jest.fn(() => ({})),
}));

const mockAddAlert = jest.fn();
const mockAddDanger = jest.fn();
jest.mock('../../app/AlertContext', () => ({
  useAlerts: jest.fn(() => ({
    addAlert: mockAddAlert,
    addSuccess: jest.fn(),
    addDanger: mockAddDanger,
  })),
}));

// Everything under the header has specs of its own.
jest.mock('../../components/sandbox/SandboxAttention', () => ({
  __esModule: true,
  default: () => null,
}));
jest.mock('../../components/sandbox/SandboxLogsTab', () => ({
  __esModule: true,
  default: () => null,
}));
jest.mock('../../components/sandbox/SandboxProvidersTab', () => ({
  __esModule: true,
  default: () => null,
}));
jest.mock('../../components/PolicyRuleEditor', () => ({
  __esModule: true,
  default: () => null,
}));
jest.mock('../../components/ConnectCard', () => ({
  __esModule: true,
  default: () => null,
}));

import { useDeleteSandbox, useSandbox } from '../../api/sandboxes';
const mockUseSandbox = useSandbox as jest.Mock;
const mockUseDeleteSandbox = useDeleteSandbox as jest.Mock;

const makeSandbox = (status: Partial<SandboxStatus> = {}): Sandbox => ({
  metadata: {
    id: 'sb-1',
    name: 'agent-1',
    workspace: 'team-a',
    createdAtMs: CREATED_AT_MS,
    resourceVersion: 2,
  },
  spec: { image: 'base' },
  status: { phase: 'READY', currentPolicyVersion: 1, ...status },
});

// The page on its own route, beside a stand-in for the workspace page it
// goes back to after a delete.
const renderPage = (
  sandbox: Sandbox = makeSandbox(),
  props: { onDeleted?: () => void } = {},
) => {
  mockUseSandbox.mockReturnValue({
    isLoading: false,
    isError: false,
    data: sandbox,
  });
  return render(
    <MemoryRouter initialEntries={['/workspaces/team-a/sandboxes/agent-1']}>
      <Routes>
        <Route
          path="/workspaces/:workspace/sandboxes/:sandbox"
          element={
            <SandboxDetailPage
              workspace="team-a"
              sandboxName="agent-1"
              {...props}
            />
          }
        />
        <Route
          path="/workspaces/:workspace"
          element={<div data-testid="workspace-page">the sandbox list</div>}
        />
      </Routes>
    </MemoryRouter>,
  );
};

const confirmDelete = (typed = 'agent-1') => {
  fireEvent.click(screen.getByTestId('sandbox-delete-button'));
  fireEvent.change(screen.getByTestId('confirm-delete-name-input'), {
    target: { value: typed },
  });
  fireEvent.click(screen.getByTestId('confirm-delete'));
};

describe('SandboxDetailPage header', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockDeleteAnswer = { outcome: 'completed', deleted: true };
    mockDeleteState = { isPending: false, isError: false };
  });

  describe('age', () => {
    beforeEach(() => {
      jest.useFakeTimers().setSystemTime(CREATED_AT_MS + 26 * 60 * 60 * 1000);
    });
    afterEach(() => {
      jest.useRealTimers();
    });

    it('shows the age of the sandbox beside when it was created', () => {
      renderPage();
      expect(screen.getByTestId('sandbox-age')).toHaveTextContent(/^1d 2h$/);
      const details = screen.getByTestId('sandbox-details-card');
      const text = details.textContent ?? '';
      expect(text.indexOf('Created')).toBeGreaterThan(-1);
      // Age comes right after Created, as in the TUI.
      expect(text.indexOf('Age')).toBeGreaterThan(text.indexOf('Created'));
      expect(text.indexOf('Age')).toBeLessThan(
        text.indexOf('Active policy version'),
      );
    });
  });

  describe('phase', () => {
    it('shows a completed sandbox as completed', () => {
      renderPage(makeSandbox({ phase: 'COMPLETED', exitCode: 0 }));
      expect(screen.getByTestId('phase-label')).toHaveTextContent('COMPLETED');
    });

    it('shows the exit code of a stopped sandbox that reports one', () => {
      renderPage(makeSandbox({ phase: 'STOPPED', exitCode: 143 }));
      expect(screen.getByTestId('phase-label')).toHaveTextContent(
        'STOPPED (exit 143)',
      );
    });
  });

  describe('start and stop', () => {
    it('offers a stop for a ready sandbox', () => {
      renderPage();
      expect(screen.getByTestId('sandbox-stop-button')).toBeEnabled();
      expect(
        screen.queryByTestId('sandbox-start-button'),
      ).not.toBeInTheDocument();
    });

    // Gateway 0.1.9 accepts a start of a completed sandbox, and with the
    // Docker driver that start fails and leaves the sandbox in ERROR. A stop,
    // then a start, runs it again.
    it('offers a stop, and no start, for a completed sandbox', () => {
      renderPage(makeSandbox({ phase: 'COMPLETED', exitCode: 0 }));
      expect(
        screen.queryByTestId('sandbox-start-button'),
      ).not.toBeInTheDocument();
      fireEvent.click(screen.getByTestId('sandbox-stop-button'));
      expect(mockStopMutate).toHaveBeenCalledWith('agent-1', expect.anything());
    });

    it('offers a stop for a sandbox in ERROR, which a start can follow', () => {
      renderPage(makeSandbox({ phase: 'ERROR', exitCode: 3 }));
      expect(
        screen.queryByTestId('sandbox-start-button'),
      ).not.toBeInTheDocument();
      fireEvent.click(screen.getByTestId('sandbox-stop-button'));
      expect(mockStopMutate).toHaveBeenCalledWith('agent-1', expect.anything());
    });

    it('offers a start for a stopped sandbox', () => {
      renderPage(makeSandbox({ phase: 'STOPPED' }));
      expect(
        screen.queryByTestId('sandbox-stop-button'),
      ).not.toBeInTheDocument();
      fireEvent.click(screen.getByTestId('sandbox-start-button'));
      expect(mockStartMutate).toHaveBeenCalledWith(
        'agent-1',
        expect.anything(),
      );
    });

    it('offers neither while the sandbox is provisioning', () => {
      renderPage(makeSandbox({ phase: 'PROVISIONING' }));
      expect(
        screen.queryByTestId('sandbox-start-button'),
      ).not.toBeInTheDocument();
      expect(screen.getByTestId('sandbox-stop-button')).toBeDisabled();
    });
  });

  describe('suspend and resume', () => {
    it('offers a suspend beside the stop of a ready sandbox', () => {
      renderPage();
      expect(screen.getByTestId('sandbox-stop-button')).toBeEnabled();
      expect(
        screen.queryByTestId('sandbox-resume-button'),
      ).not.toBeInTheDocument();
      const suspend = screen.getByTestId('sandbox-suspend-button');
      expect(suspend).toHaveTextContent('Suspend');
      fireEvent.click(suspend);
      expect(mockSuspendMutate).toHaveBeenCalledWith(
        'agent-1',
        expect.anything(),
      );
    });

    it('shows a suspended sandbox as suspended and offers a resume', () => {
      renderPage(makeSandbox({ phase: 'SUSPENDED' }));
      expect(screen.getByTestId('phase-label')).toHaveTextContent('SUSPENDED');
      expect(
        screen.queryByTestId('sandbox-suspend-button'),
      ).not.toBeInTheDocument();
      const resume = screen.getByTestId('sandbox-resume-button');
      expect(resume).toHaveTextContent('Resume');
      fireEvent.click(resume);
      expect(mockResumeMutate).toHaveBeenCalledWith(
        'agent-1',
        expect.anything(),
      );
    });

    // A start would boot it afresh and discard what the resume restores; a
    // stop says that it ends the sandbox's run.
    it('offers a stop, and no start, for a suspended sandbox', () => {
      renderPage(makeSandbox({ phase: 'SUSPENDED' }));
      expect(
        screen.queryByTestId('sandbox-start-button'),
      ).not.toBeInTheDocument();
      expect(screen.getByTestId('sandbox-stop-button')).toBeEnabled();
    });

    it('offers a stop, and nothing else, while a suspend is under way', () => {
      renderPage(makeSandbox({ phase: 'SUSPENDING' }));
      expect(screen.getByTestId('sandbox-stop-button')).toBeEnabled();
      for (const action of ['suspend', 'resume', 'start']) {
        expect(
          screen.queryByTestId(`sandbox-${action}-button`),
        ).not.toBeInTheDocument();
      }
    });

    it.each(['STOPPED', 'COMPLETED', 'ERROR', 'PROVISIONING'] as const)(
      'offers neither for a sandbox that is %s',
      (phase) => {
        renderPage(makeSandbox({ phase }));
        expect(
          screen.queryByTestId('sandbox-suspend-button'),
        ).not.toBeInTheDocument();
        expect(
          screen.queryByTestId('sandbox-resume-button'),
        ).not.toBeInTheDocument();
      },
    );

    // What every compute driver upstream ships at 0.1.9 answers.
    it("shows the gateway's refusal of a suspend", () => {
      mockSuspendMutate.mockImplementationOnce(
        (_name: string, options: { onError: (err: Error) => void }) =>
          options.onError(
            new Error('compute driver does not support sandbox suspend'),
          ),
      );
      renderPage();
      fireEvent.click(screen.getByTestId('sandbox-suspend-button'));
      expect(mockAddDanger).toHaveBeenCalledWith(
        'Failed to suspend sandbox agent-1: compute driver does not support sandbox suspend',
      );
    });

    it("shows the gateway's refusal of a resume", () => {
      mockResumeMutate.mockImplementationOnce(
        (_name: string, options: { onError: (err: Error) => void }) =>
          options.onError(new Error('restore failed')),
      );
      renderPage(makeSandbox({ phase: 'SUSPENDED' }));
      fireEvent.click(screen.getByTestId('sandbox-resume-button'));
      expect(mockAddDanger).toHaveBeenCalledWith(
        'Failed to resume sandbox agent-1: restore failed',
      );
    });
  });

  describe('delete', () => {
    it('deletes nothing until the name has been typed and confirmed', () => {
      renderPage();
      expect(
        screen.queryByTestId('confirm-delete-name-input'),
      ).not.toBeInTheDocument();

      fireEvent.click(screen.getByTestId('sandbox-delete-button'));
      expect(screen.getByText('Delete sandbox?')).toBeInTheDocument();
      expect(screen.getByTestId('confirm-delete')).toBeDisabled();

      fireEvent.change(screen.getByTestId('confirm-delete-name-input'), {
        target: { value: 'agent-2' },
      });
      expect(screen.getByTestId('confirm-delete')).toBeDisabled();
      expect(mockDeleteMutate).not.toHaveBeenCalled();
    });

    it('deletes this sandbox of this workspace', () => {
      renderPage();
      confirmDelete();
      expect(mockUseDeleteSandbox).toHaveBeenCalledWith('team-a');
      expect(mockDeleteMutate).toHaveBeenCalledTimes(1);
      expect(mockDeleteMutate).toHaveBeenCalledWith(
        'agent-1',
        expect.anything(),
      );
    });

    it("goes back to the workspace's sandbox list afterwards", () => {
      renderPage();
      expect(screen.queryByTestId('workspace-page')).not.toBeInTheDocument();
      confirmDelete();
      expect(screen.getByTestId('workspace-page')).toBeInTheDocument();
    });

    it('calls onDeleted instead of navigating, when one is passed', () => {
      const onDeleted = jest.fn();
      renderPage(makeSandbox(), { onDeleted });
      confirmDelete();
      expect(onDeleted).toHaveBeenCalledTimes(1);
      expect(screen.queryByTestId('workspace-page')).not.toBeInTheDocument();
    });

    it.each<[DeleteResult, AlertVariant, string]>([
      [
        { outcome: 'completed', deleted: true },
        AlertVariant.success,
        'Sandbox "agent-1" deleted',
      ],
      [
        { outcome: 'accepted', deleted: false },
        AlertVariant.info,
        'Sandbox "agent-1" deletion accepted; cleanup is pending',
      ],
      [
        { outcome: 'already_absent', deleted: true },
        AlertVariant.success,
        'Sandbox "agent-1" already deleted',
      ],
      [
        { outcome: 'unspecified', deleted: false },
        AlertVariant.danger,
        'Unsupported deletion outcome for sandbox "agent-1"',
      ],
    ])('says what the gateway did: $outcome', (answer, variant, title) => {
      mockDeleteAnswer = answer;
      renderPage();
      confirmDelete();
      expect(mockAddAlert).toHaveBeenCalledTimes(1);
      expect(mockAddAlert).toHaveBeenCalledWith(title, variant);
    });

    it('stays on the page and shows the error when the delete fails', () => {
      mockDeleteAnswer = new Error('sandbox is being provisioned');
      mockDeleteState = {
        isPending: false,
        isError: true,
        error: new Error('sandbox is being provisioned'),
      };
      renderPage();
      confirmDelete();
      expect(
        screen.getByText('sandbox is being provisioned'),
      ).toBeInTheDocument();
      expect(screen.queryByTestId('workspace-page')).not.toBeInTheDocument();
      expect(mockAddAlert).not.toHaveBeenCalled();
    });

    it('forgets a failed delete when the dialog is cancelled', () => {
      renderPage();
      fireEvent.click(screen.getByTestId('sandbox-delete-button'));
      fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
      expect(mockDeleteReset).toHaveBeenCalled();
      expect(mockDeleteMutate).not.toHaveBeenCalled();
      expect(
        screen.queryByTestId('confirm-delete-name-input'),
      ).not.toBeInTheDocument();
    });
  });
});
