import React from 'react';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { AlertVariant } from '@patternfly/react-core';
import { MemoryRouter } from 'react-router-dom';

import SandboxListPage from '../SandboxListPage';
import type {
  DeletionOutcome,
  DraftSandboxSummary,
  Sandbox,
  SandboxStatus,
} from '../../types';

const CREATED_AT_MS = Date.UTC(2026, 9, 6, 14, 30, 0);
const REJECTED =
  'Effective configuration could not be activated; replace the policy or repair attached providers';

const makeSandbox = (
  name: string,
  status: Partial<SandboxStatus> = {},
): Sandbox => ({
  metadata: {
    id: `id-${name}`,
    name,
    workspace: 'team-a',
    createdAtMs: CREATED_AT_MS,
    resourceVersion: 1,
    labels: { team: 'ml' },
  },
  spec: {
    image: 'ghcr.io/nvidia/openshell-community/sandboxes/python:latest',
    policy: { version: 1, networkPolicies: {} },
  },
  status: { phase: 'READY', currentPolicyVersion: 1, ...status },
});

// A sandbox as gateway 0.1.2 reports it while its configuration is rejected.
const rejectedSandbox = makeSandbox('stuck', {
  phase: 'PROVISIONING',
  currentPolicyVersion: 0,
  conditions: [
    {
      type: 'ConfigurationReady',
      status: 'False',
      reason: 'ConfigurationInvalid',
      message: REJECTED,
    },
    {
      type: 'Ready',
      status: 'False',
      reason: 'ConfigurationInvalid',
      message: REJECTED,
    },
  ],
  configurationAdmission: {
    state: 'REJECTED',
    policyVersion: 0,
    configRevision: '1',
    providerEnvRevision: '2',
    error: REJECTED,
  },
});

let mockFlags = { terminal: true, draftPolicy: true };
jest.mock('../../api/auth', () => ({
  useFeatureFlags: jest.fn(() => mockFlags),
}));

jest.mock('../../api/sandboxes', () => ({
  useSandboxes: jest.fn(),
  useSandboxPage: jest.fn(),
  deleteSandbox: jest.fn(),
  useStopSandbox: jest.fn(() => ({ mutate: jest.fn(), isPending: false })),
  useStartSandbox: jest.fn(() => ({ mutate: jest.fn(), isPending: false })),
  useSuspendSandbox: jest.fn(() => ({ mutate: jest.fn(), isPending: false })),
  useResumeSandbox: jest.fn(() => ({ mutate: jest.fn(), isPending: false })),
}));

jest.mock('../../api/policy', () => ({
  useSandboxPolicies: jest.fn(() => ({})),
}));

let mockDraftItems: DraftSandboxSummary[] = [];
jest.mock('../../api/draftSummary', () => ({
  useWorkspaceDraftSummary: jest.fn(() => ({
    items: mockDraftItems,
    bySandbox: Object.fromEntries(
      mockDraftItems.map((item) => [item.sandboxName, item]),
    ),
    totalPending: 0,
  })),
}));

jest.mock('../../api/providers', () => ({
  useProviderExpiry: jest.fn(() => ({})),
}));

const mockAddAlert = jest.fn();
const mockAddSuccess = jest.fn();
jest.mock('../../app/AlertContext', () => ({
  useAlerts: jest.fn(() => ({
    addAlert: mockAddAlert,
    addSuccess: mockAddSuccess,
    addDanger: jest.fn(),
  })),
}));

// The outcomes the gateway gave the deletes of one confirm, in order.
let mockDeleteOutcomes: DeletionOutcome[] = [];
const mockRunDelete = jest.fn(
  (_names: string[], onDone: (outcomes: DeletionOutcome[]) => void) =>
    onDone(mockDeleteOutcomes),
);
jest.mock('../../hooks/useBulkDelete', () => ({
  useBulkDelete: jest.fn(() => ({
    run: mockRunDelete,
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

import { useWorkspaceDraftSummary } from '../../api/draftSummary';
import { useSandboxPolicies } from '../../api/policy';
import { useSandboxes, useSandboxPage } from '../../api/sandboxes';
import { pagedResult } from './pagedHookMock';

const mockUseSandboxes = useSandboxes as jest.Mock;
const mockUseSandboxPage = useSandboxPage as jest.Mock;

const bottomPagination = () =>
  within(screen.getByRole('navigation', { name: 'Bottom pagination' }));

beforeEach(() => {
  mockUseSandboxPage.mockImplementation((workspace, request) =>
    pagedResult(mockUseSandboxes(workspace, request.labelSelector), request),
  );
});
const mockUseSandboxPolicies = useSandboxPolicies as jest.Mock;
const mockUseWorkspaceDraftSummary = useWorkspaceDraftSummary as jest.Mock;

const listOf = (...sandboxes: Sandbox[]) => ({
  isLoading: false,
  isError: false,
  data: sandboxes,
});

const onViewSandbox = jest.fn();

const renderPage = () =>
  render(
    <MemoryRouter>
      <SandboxListPage workspace="team-a" onViewSandbox={onViewSandbox} />
    </MemoryRouter>,
  );

const showCards = () =>
  fireEvent.click(screen.getByRole('button', { name: 'Card view' }));

const selectorInput = () =>
  within(screen.getByTestId('sandbox-label-selector')).getByRole('textbox');

const applySelector = (selector: string) => {
  fireEvent.change(selectorInput(), { target: { value: selector } });
  fireEvent.click(screen.getByRole('button', { name: 'Apply label selector' }));
};

// The label selector every call of useSandboxes asked for, in order.
const selectorsAskedFor = (): (string | undefined)[] =>
  mockUseSandboxes.mock.calls.map(([, selector]) => selector);

describe('SandboxListPage list', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    localStorage.clear();
    mockFlags = { terminal: true, draftPolicy: true };
    mockDraftItems = [];
    mockDeleteOutcomes = [];
    mockUseSandboxes.mockReturnValue(listOf(makeSandbox('agent-1')));
  });

  describe('creation time', () => {
    it('has a Created column, with the time itself and not only the age', () => {
      renderPage();
      expect(
        screen.getByRole('columnheader', { name: 'Created' }),
      ).toBeInTheDocument();
      expect(
        screen.getByTestId('sandbox-created-agent-1').querySelector('time'),
      ).toHaveAttribute('datetime', new Date(CREATED_AT_MS).toISOString());
    });

    it('keeps the creation time within reach in the card view', () => {
      renderPage();
      showCards();
      expect(
        screen
          .getByTestId('sandbox-card-created-agent-1')
          .querySelector('time'),
      ).toHaveAttribute('datetime', new Date(CREATED_AT_MS).toISOString());
    });
  });

  describe('label selector', () => {
    it('combines an applied type label with the generic selector', () => {
      renderPage();
      applySelector('team=ml');
      const type = within(screen.getByTestId('sandbox-type-label')).getByRole(
        'textbox',
      );
      fireEvent.change(type, { target: { value: 'type=tool' } });
      fireEvent.click(screen.getByRole('button', { name: 'Apply type label' }));
      expect(mockUseSandboxPage).toHaveBeenLastCalledWith('team-a', {
        pageSize: 10,
        pageToken: '',
        labelSelector: 'team=ml,type=tool',
      });
    });

    it('rejects a type key already used in the generic selector', () => {
      renderPage();
      applySelector('type=agent');
      const type = within(screen.getByTestId('sandbox-type-label')).getByRole(
        'textbox',
      );
      fireEvent.change(type, { target: { value: 'type=tool' } });
      fireEvent.click(screen.getByRole('button', { name: 'Apply type label' }));
      expect(screen.getByTestId('sandbox-type-label-error')).toHaveTextContent(
        'already uses type',
      );
      expect(mockUseSandboxPage).toHaveBeenLastCalledWith('team-a', {
        pageSize: 10,
        pageToken: '',
        labelSelector: 'type=agent',
      });
    });

    it('requests only the next page and retains earlier selections', () => {
      mockUseSandboxes.mockReturnValue(
        listOf(
          ...Array.from({ length: 11 }, (_, i) =>
            makeSandbox(`agent-${i + 1}`),
          ),
        ),
      );
      renderPage();
      const firstRow = screen
        .getByTestId('sandbox-table')
        .querySelector('tbody tr');
      fireEvent.click(within(firstRow as HTMLElement).getByRole('checkbox'));
      fireEvent.click(
        bottomPagination().getByRole('button', { name: 'Go to next page' }),
      );
      expect(mockUseSandboxPage).toHaveBeenLastCalledWith('team-a', {
        pageSize: 10,
        pageToken: '10',
        labelSelector: undefined,
      });
      expect(mockUseWorkspaceDraftSummary).toHaveBeenLastCalledWith(
        'team-a',
        true,
        ['agent-11'],
      );
      const secondRow = screen
        .getByTestId('sandbox-table')
        .querySelector('tbody tr');
      fireEvent.click(within(secondRow as HTMLElement).getByRole('checkbox'));
      fireEvent.click(screen.getByTestId('sandbox-actions-kebab'));
      expect(screen.getByTestId('delete-selected-sandboxes')).toHaveTextContent(
        'Delete selected (2)',
      );
      expect(
        bottomPagination().getByRole('button', { name: 'Go to next page' }),
      ).toBeDisabled();
      fireEvent.click(
        bottomPagination().getByRole('button', { name: 'Go to previous page' }),
      );
      expect(mockUseSandboxPage).toHaveBeenLastCalledWith('team-a', {
        pageSize: 10,
        pageToken: '',
        labelSelector: undefined,
      });
    });

    it('offers a first-page reset when a continuation token is rejected', () => {
      mockUseSandboxPage.mockImplementation((_workspace, request) =>
        request.pageToken
          ? {
              isLoading: false,
              isError: true,
              error: new Error('token expired'),
              data: undefined,
            }
          : {
              isLoading: false,
              isError: false,
              data: { items: [makeSandbox('agent-1')], nextPageToken: 'stale' },
            },
      );
      renderPage();
      fireEvent.click(
        bottomPagination().getByRole('button', { name: 'Go to next page' }),
      );
      expect(screen.getByText('token expired')).toBeInTheDocument();
      fireEvent.click(
        screen.getByRole('button', { name: 'Start at first page' }),
      );
      expect(mockUseSandboxPage).toHaveBeenLastCalledWith('team-a', {
        pageSize: 10,
        pageToken: '',
        labelSelector: undefined,
      });
      expect(bottomPagination().getByText('Page 1')).toBeInTheDocument();
    });
    it('lists every sandbox of the workspace until a selector is applied', () => {
      renderPage();
      expect(mockUseSandboxes).toHaveBeenCalledWith('team-a', undefined);
      expect(selectorsAskedFor().every((s) => s === undefined)).toBe(true);
    });

    it('asks the gateway for the selector when the search is submitted', () => {
      renderPage();
      fireEvent.change(selectorInput(), { target: { value: 'team=ml' } });
      // Typing sends nothing: a selector that is half typed is one the
      // gateway refuses.
      expect(selectorsAskedFor()).not.toContain('team=ml');

      fireEvent.click(
        screen.getByRole('button', { name: 'Apply label selector' }),
      );
      expect(mockUseSandboxes).toHaveBeenLastCalledWith('team-a', 'team=ml');
    });

    it('trims the selector it sends', () => {
      renderPage();
      applySelector('  team=ml,tier=gpu ');
      expect(mockUseSandboxes).toHaveBeenLastCalledWith(
        'team-a',
        'team=ml,tier=gpu',
      );
    });

    it('goes back to the whole list when the selector is cleared', () => {
      renderPage();
      applySelector('team=ml');
      fireEvent.click(
        screen.getByRole('button', { name: 'Clear label selector' }),
      );
      expect(mockUseSandboxes).toHaveBeenLastCalledWith('team-a', undefined);
      expect(selectorInput()).toHaveValue('');
    });

    it('shows the refusal of a malformed selector beside the filter', () => {
      mockUseSandboxes.mockImplementation((_workspace, selector) =>
        selector
          ? {
              isLoading: false,
              isError: true,
              error: new Error("invalid label selector 'team'"),
              data: undefined,
            }
          : listOf(makeSandbox('agent-1')),
      );
      renderPage();
      applySelector('team');

      expect(
        screen.getByTestId('sandbox-label-selector-error'),
      ).toHaveTextContent("invalid label selector 'team'");
      // The filter is still there to be corrected, with what was typed.
      expect(selectorInput()).toHaveValue('team');
      expect(
        screen.queryByText('Failed to load sandboxes'),
      ).not.toBeInTheDocument();
      expect(
        screen.queryByText('No sandboxes match this filter.'),
      ).not.toBeInTheDocument();
    });

    it('says a selector matches nothing instead of inviting a first sandbox', () => {
      mockUseSandboxes.mockImplementation((_workspace, selector) =>
        selector ? listOf() : listOf(makeSandbox('agent-1')),
      );
      renderPage();
      applySelector('team=nobody');

      expect(
        screen.getByText('No sandboxes match this filter.'),
      ).toBeInTheDocument();
      expect(
        screen.queryByTestId('create-sandbox-empty'),
      ).not.toBeInTheDocument();
      expect(selectorInput()).toHaveValue('team=nobody');
    });

    it('says the same in the card view', () => {
      mockUseSandboxes.mockImplementation((_workspace, selector) =>
        selector ? listOf() : listOf(makeSandbox('agent-1')),
      );
      renderPage();
      showCards();
      applySelector('team=nobody');

      expect(screen.getByTestId('sandbox-gallery-no-match')).toHaveTextContent(
        'No sandboxes match this filter',
      );
      expect(
        screen.queryByTestId('create-sandbox-empty-gallery'),
      ).not.toBeInTheDocument();
    });

    it('still shows the first-sandbox empty state for an empty workspace', () => {
      mockUseSandboxes.mockReturnValue(listOf());
      renderPage();
      expect(screen.getByTestId('create-sandbox-empty')).toBeInTheDocument();
    });
  });

  describe('rejected configuration', () => {
    beforeEach(() => {
      mockUseSandboxes.mockReturnValue(
        listOf(makeSandbox('agent-1'), rejectedSandbox),
      );
    });

    it('is noted in the table on a sandbox that is not in ERROR', () => {
      renderPage();
      expect(
        screen.getByTestId('sandbox-invalid-config-stuck'),
      ).toHaveTextContent('Invalid config');
      expect(
        screen.queryByTestId('sandbox-invalid-config-agent-1'),
      ).not.toBeInTheDocument();
    });

    it('is noted in the card view, with the reason', () => {
      renderPage();
      showCards();
      const card = screen.getByTestId('sandbox-card-stuck');
      expect(
        within(card).getByTestId('attention-invalid-config'),
      ).toHaveTextContent('Invalid config');
      expect(within(card).getByText(REJECTED)).toBeInTheDocument();
      expect(
        within(screen.getByTestId('sandbox-card-agent-1')).queryByTestId(
          'attention-invalid-config',
        ),
      ).not.toBeInTheDocument();
    });
  });

  describe('pending proposals', () => {
    const pending = (
      sandboxName: string,
      overrides: Partial<DraftSandboxSummary> = {},
    ): DraftSandboxSummary => ({
      workspace: 'team-a',
      sandboxName,
      pendingCount: 2,
      hasSecurityFlags: false,
      latestDraftMs: CREATED_AT_MS,
      ...overrides,
    });

    beforeEach(() => {
      mockUseSandboxes.mockReturnValue(
        listOf(makeSandbox('agent-1'), makeSandbox('agent-2')),
      );
    });

    it('reads the summary of this workspace', () => {
      renderPage();
      expect(mockUseWorkspaceDraftSummary).toHaveBeenCalledWith(
        'team-a',
        true,
        ['agent-1', 'agent-2'],
      );
    });

    it('badges the sandboxes that have rules pending, in the table', () => {
      mockDraftItems = [pending('agent-2')];
      renderPage();
      expect(screen.getByTestId('sandbox-pending-agent-2')).toHaveTextContent(
        '2 pending rules',
      );
      expect(
        screen.queryByTestId('sandbox-pending-agent-1'),
      ).not.toBeInTheDocument();
    });

    it('opens the proposals of the sandbox whose badge is clicked', () => {
      mockDraftItems = [pending('agent-2')];
      renderPage();
      fireEvent.click(
        within(screen.getByTestId('sandbox-pending-agent-2')).getByRole(
          'button',
        ),
      );
      expect(onViewSandbox).toHaveBeenCalledWith('agent-2', 'proposals');
    });

    it('says which sandboxes could not be read, in both views', () => {
      mockDraftItems = [
        pending('agent-1', { pendingCount: 0, unavailable: true }),
      ];
      renderPage();
      expect(
        screen.getByTestId('sandbox-pending-unavailable-agent-1'),
      ).toBeInTheDocument();

      showCards();
      expect(
        within(screen.getByTestId('sandbox-card-agent-1')).getByTestId(
          'attention-drafts-unavailable',
        ),
      ).toBeInTheDocument();
      expect(
        within(screen.getByTestId('sandbox-card-agent-2')).queryByTestId(
          'attention-drafts-unavailable',
        ),
      ).not.toBeInTheDocument();
    });

    it('shows the same count on the card of the sandbox', () => {
      mockDraftItems = [pending('agent-2')];
      renderPage();
      showCards();
      expect(
        within(screen.getByTestId('sandbox-card-agent-2')).getByTestId(
          'attention-drafts',
        ),
      ).toHaveTextContent('2 rules proposed');
    });

    it('asks for nothing where draft policies are switched off', () => {
      mockFlags = { terminal: true, draftPolicy: false };
      mockDraftItems = [];
      renderPage();
      expect(mockUseWorkspaceDraftSummary).toHaveBeenCalledWith(
        'team-a',
        false,
        ['agent-1', 'agent-2'],
      );
      expect(screen.queryByText(/pending rule/)).not.toBeInTheDocument();
    });
  });

  describe('sandbox policies', () => {
    // The gateway has no policy revision for a sandbox whose supervisor has
    // not started, and answers a request for it with a 404.
    it('does not ask for the policy of a sandbox that has loaded none yet', () => {
      mockUseSandboxes.mockReturnValue(
        listOf(
          makeSandbox('agent-1'),
          makeSandbox('just-created', {
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
      );
      renderPage();
      expect(mockUseSandboxPolicies).toHaveBeenLastCalledWith('team-a', [
        'agent-1',
      ]);
    });

    it('asks for it once the sandbox reports a policy version', () => {
      mockUseSandboxes.mockReturnValue(
        listOf(makeSandbox('agent-1'), makeSandbox('just-created')),
      );
      renderPage();
      expect(mockUseSandboxPolicies).toHaveBeenLastCalledWith('team-a', [
        'agent-1',
        'just-created',
      ]);
    });
  });

  describe('delete', () => {
    const confirmDeleteOf = (name: string) => {
      const row = screen.getByTestId(`sandbox-link-${name}`).closest('tr');
      fireEvent.click(
        within(row as HTMLElement).getByRole('button', {
          name: /kebab toggle/i,
        }),
      );
      fireEvent.click(screen.getByRole('menuitem', { name: 'Delete' }));
      fireEvent.change(screen.getByTestId('confirm-delete-name-input'), {
        target: { value: name },
      });
      fireEvent.click(screen.getByTestId('confirm-delete'));
    };

    it.each<[DeletionOutcome, AlertVariant, string]>([
      ['completed', AlertVariant.success, 'Sandbox "agent-1" deleted'],
      [
        'accepted',
        AlertVariant.info,
        'Sandbox "agent-1" deletion accepted; cleanup is pending',
      ],
      [
        'already_absent',
        AlertVariant.success,
        'Sandbox "agent-1" already deleted',
      ],
      [
        'unspecified',
        AlertVariant.danger,
        'Unsupported deletion outcome for sandbox "agent-1"',
      ],
    ])('reports a delete the gateway %s as that', (outcome, variant, title) => {
      mockDeleteOutcomes = [outcome];
      renderPage();
      confirmDeleteOf('agent-1');

      expect(mockRunDelete).toHaveBeenCalledWith(
        ['agent-1'],
        expect.any(Function),
      );
      expect(mockAddAlert).toHaveBeenCalledTimes(1);
      expect(mockAddAlert).toHaveBeenCalledWith(title, variant);
      // Never the old unconditional "deleted".
      expect(mockAddSuccess).not.toHaveBeenCalled();
    });
  });
});
