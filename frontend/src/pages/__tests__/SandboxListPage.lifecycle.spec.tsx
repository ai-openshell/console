import React from 'react';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';

import SandboxListPage from '../SandboxListPage';
import type { Sandbox, SandboxPhase } from '../../types';

// The lifecycle actions of a row: which ones a sandbox is offered in which
// phase, what each one calls, and what is said when the gateway refuses.

jest.mock('../../api/auth', () => ({
  useFeatureFlags: jest.fn(() => ({ terminal: true, draftPolicy: true })),
}));

type MutateOptions = {
  onSuccess?: () => void;
  onError?: (err: Error) => void;
};
const mockMutate = {
  stop: jest.fn(),
  start: jest.fn(),
  suspend: jest.fn(),
  resume: jest.fn(),
};

jest.mock('../../api/sandboxes', () => ({
  useSandboxes: jest.fn(),
  useSandboxPage: jest.fn(),
  deleteSandbox: jest.fn(),
  useStopSandbox: jest.fn(() => ({ mutate: mockMutate.stop })),
  useStartSandbox: jest.fn(() => ({ mutate: mockMutate.start })),
  useSuspendSandbox: jest.fn(() => ({ mutate: mockMutate.suspend })),
  useResumeSandbox: jest.fn(() => ({ mutate: mockMutate.resume })),
}));

jest.mock('../../api/policy', () => ({
  useSandboxPolicies: jest.fn(() => ({})),
}));

jest.mock('../../api/draftSummary', () => ({
  useWorkspaceDraftSummary: jest.fn(() => ({
    items: [],
    bySandbox: {},
    totalPending: 0,
  })),
}));

jest.mock('../../api/providers', () => ({
  useProviderExpiry: jest.fn(() => ({})),
}));

const mockAddSuccess = jest.fn();
const mockAddDanger = jest.fn();
jest.mock('../../app/AlertContext', () => ({
  useAlerts: jest.fn(() => ({
    addAlert: jest.fn(),
    addSuccess: mockAddSuccess,
    addDanger: mockAddDanger,
  })),
}));

jest.mock('../../hooks/useBulkDelete', () => ({
  useBulkDelete: jest.fn(() => ({
    run: jest.fn(),
    isDeleting: false,
    error: undefined,
    clearError: jest.fn(),
  })),
}));

jest.mock('../../components/CreateSandboxModal', () => ({
  __esModule: true,
  default: () => null,
}));

jest.mock('../../slots', () => ({ useSlots: () => ({}) }));

import { useSandboxPage } from '../../api/sandboxes';

const mockUseSandboxPage = useSandboxPage as jest.Mock;

const sandboxIn = (phase: SandboxPhase): Sandbox => ({
  metadata: {
    id: 'id-agent-1',
    name: 'agent-1',
    workspace: 'team-a',
    createdAtMs: Date.UTC(2026, 9, 6, 14, 30, 0),
    resourceVersion: 1,
  },
  spec: { image: 'base', policy: { version: 1, networkPolicies: {} } },
  status: { phase, currentPolicyVersion: 1 },
});

// Opens the actions of the one row and returns the lifecycle actions it
// offers, in order.
const openActions = (phase: SandboxPhase): string[] => {
  mockUseSandboxPage.mockReturnValue({
    isLoading: false,
    isError: false,
    data: { items: [sandboxIn(phase)], nextPageToken: '' },
  });
  render(
    <MemoryRouter>
      <SandboxListPage workspace="team-a" />
    </MemoryRouter>,
  );
  const row = screen.getByTestId('sandbox-link-agent-1').closest('tr');
  fireEvent.click(
    within(row as HTMLElement).getByRole('button', { name: /kebab toggle/i }),
  );
  return screen
    .getAllByRole('menuitem')
    .map((item) => item.textContent ?? '')
    .filter((title) => ['Stop', 'Start', 'Suspend', 'Resume'].includes(title));
};

const choose = (title: string) =>
  fireEvent.click(screen.getByRole('menuitem', { name: title }));

describe('SandboxListPage lifecycle actions', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    localStorage.clear();
  });

  it.each<[SandboxPhase, string[]]>([
    ['READY', ['Suspend', 'Stop']],
    ['SUSPENDED', ['Resume', 'Stop']],
    ['SUSPENDING', ['Stop']],
    ['STOPPED', ['Start']],
    ['COMPLETED', ['Stop']],
    ['ERROR', ['Stop']],
    ['PROVISIONING', []],
    ['STARTING', []],
    ['STOPPING', []],
    ['DELETING', []],
  ])('offers a sandbox that is %s: %j', (phase, actions) => {
    expect(openActions(phase)).toEqual(actions);
    // Whatever its phase, a sandbox can be deleted.
    expect(
      screen.getByRole('menuitem', { name: 'Delete' }),
    ).toBeInTheDocument();
  });

  it.each<[SandboxPhase, string, keyof typeof mockMutate, string]>([
    ['READY', 'Suspend', 'suspend', 'Suspending sandbox agent-1'],
    ['SUSPENDED', 'Resume', 'resume', 'Resuming sandbox agent-1'],
    ['ERROR', 'Stop', 'stop', 'Stopping sandbox agent-1'],
    ['STOPPED', 'Start', 'start', 'Starting sandbox agent-1'],
  ])('%s: %s calls the gateway and says so', (phase, title, verb, said) => {
    mockMutate[verb].mockImplementationOnce(
      (_name: string, options: MutateOptions) => options.onSuccess?.(),
    );
    openActions(phase);
    choose(title);

    expect(mockMutate[verb]).toHaveBeenCalledWith('agent-1', expect.anything());
    for (const other of Object.keys(
      mockMutate,
    ) as (keyof typeof mockMutate)[]) {
      if (other !== verb) expect(mockMutate[other]).not.toHaveBeenCalled();
    }
    expect(mockAddSuccess).toHaveBeenCalledWith(said);
  });

  // What every compute driver upstream ships at 0.1.9 answers a suspend.
  it("shows the gateway's refusal of a suspend, in its words", () => {
    mockMutate.suspend.mockImplementationOnce(
      (_name: string, options: MutateOptions) =>
        options.onError?.(
          new Error('compute driver does not support sandbox suspend'),
        ),
    );
    openActions('READY');
    choose('Suspend');

    expect(mockAddDanger).toHaveBeenCalledWith(
      'Failed to suspend sandbox agent-1: compute driver does not support sandbox suspend',
    );
    expect(mockAddSuccess).not.toHaveBeenCalled();
  });

  it("shows the gateway's refusal of a resume, in its words", () => {
    mockMutate.resume.mockImplementationOnce(
      (_name: string, options: MutateOptions) =>
        options.onError?.(new Error('restore failed')),
    );
    openActions('SUSPENDED');
    choose('Resume');

    expect(mockAddDanger).toHaveBeenCalledWith(
      'Failed to resume sandbox agent-1: restore failed',
    );
    expect(mockAddSuccess).not.toHaveBeenCalled();
  });
});
