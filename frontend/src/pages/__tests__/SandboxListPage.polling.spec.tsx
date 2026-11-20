import React from 'react';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';

import SandboxListPage from '../SandboxListPage';
import type { Sandbox } from '../../types';

// What the list does when the data under it changes while somebody is using
// the page: a poll that fails, a list that empties or fills, rows that go.

jest.mock('../../api/auth', () => ({
  useFeatureFlags: jest.fn(() => ({ terminal: true, draftPolicy: false })),
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
jest.mock('../../api/draftSummary', () => ({
  useWorkspaceDraftSummary: jest.fn(() => ({ items: [], bySandbox: {} })),
}));
jest.mock('../../api/providers', () => ({
  useProviderExpiry: jest.fn(() => ({})),
}));
jest.mock('../../app/AlertContext', () => ({
  useAlerts: jest.fn(() => ({
    addAlert: jest.fn(),
    addSuccess: jest.fn(),
    addDanger: jest.fn(),
  })),
}));
const mockRunDelete = jest.fn();
jest.mock('../../hooks/useBulkDelete', () => ({
  useBulkDelete: jest.fn(() => ({
    run: mockRunDelete,
    isDeleting: false,
    error: undefined,
    clearError: jest.fn(),
  })),
}));
jest.mock('../../slots', () => ({ useSlots: () => ({}) }));

// Stands in for the Create sandbox form: a field that holds what was typed
// for as long as the form stays mounted, which is what is under test.
jest.mock('../../components/CreateSandboxModal', () => ({
  __esModule: true,
  default: ({ isOpen }: { isOpen: boolean }) => {
    const [name, setName] = jest
      .requireActual<typeof React>('react')
      .useState('');
    return isOpen ? (
      <input
        data-testid="create-form-name"
        value={name}
        onChange={(event) => setName(event.target.value)}
      />
    ) : null;
  },
}));

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

const sandbox = (index: number): Sandbox => ({
  metadata: {
    id: `id-${index}`,
    name: `sb-${String(index).padStart(2, '0')}`,
    createdAtMs: 1,
    resourceVersion: 1,
  },
  spec: { policy: {} },
  status: { phase: 'READY', currentPolicyVersion: 1 },
});
const sandboxes = (count: number): Sandbox[] =>
  Array.from({ length: count }, (_, index) => sandbox(index + 1));

const loaded = (data: Sandbox[]) => ({
  isLoading: false,
  isError: false,
  data,
});
// What React Query reports after a refetch that failed: the error, and the
// data of the last fetch that worked.
const refreshFailed = (data: Sandbox[]) => ({
  isLoading: false,
  isError: true,
  error: new Error('bad gateway'),
  data,
  refetch: jest.fn(),
});

const page = () => (
  <MemoryRouter>
    <SandboxListPage workspace="team-a" />
  </MemoryRouter>
);

const shownNames = (): string[] =>
  screen
    .queryAllByTestId(/^sandbox-link-/)
    .map((link) => link.textContent ?? '');

const formName = () =>
  screen.getByTestId('create-form-name') as HTMLInputElement;

const selectRow = (name: string) => {
  const row = screen.getByTestId(`sandbox-link-${name}`).closest('tr');
  fireEvent.click(within(row as HTMLElement).getByRole('checkbox'));
};

const openActions = () =>
  fireEvent.click(screen.getByTestId('sandbox-actions-kebab'));

// The "Delete selected" entry of the open actions menu.
const deleteSelected = () =>
  screen.getByRole('menuitem', { name: /^Delete selected/ });

beforeEach(() => {
  jest.clearAllMocks();
  localStorage.clear();
});

describe('SandboxListPage while it is being used', () => {
  describe('a refresh that fails', () => {
    it('leaves the list on screen, with a note that it may be out of date', () => {
      mockUseSandboxes.mockReturnValue(loaded(sandboxes(2)));
      const view = render(page());

      mockUseSandboxes.mockReturnValue(refreshFailed(sandboxes(2)));
      view.rerender(page());

      expect(shownNames()).toEqual(['sb-01', 'sb-02']);
      expect(screen.getByTestId('sandboxes-refresh-error')).toHaveTextContent(
        'bad gateway',
      );
      expect(
        screen.queryByText('Failed to load sandboxes'),
      ).not.toBeInTheDocument();
    });

    it('keeps an open Create sandbox form and what was typed into it', () => {
      mockUseSandboxes.mockReturnValue(loaded(sandboxes(2)));
      const view = render(page());
      fireEvent.click(screen.getByTestId('create-sandbox'));
      fireEvent.change(formName(), { target: { value: 'half-typed' } });

      mockUseSandboxes.mockReturnValue(refreshFailed(sandboxes(2)));
      view.rerender(page());
      expect(formName()).toHaveValue('half-typed');

      // And through the refresh that works again.
      mockUseSandboxes.mockReturnValue(loaded(sandboxes(2)));
      view.rerender(page());
      expect(formName()).toHaveValue('half-typed');
      expect(
        screen.queryByTestId('sandboxes-refresh-error'),
      ).not.toBeInTheDocument();
    });

    it('offers to try again', () => {
      const failed = refreshFailed(sandboxes(1));
      mockUseSandboxes.mockReturnValue(failed);
      render(page());
      fireEvent.click(
        within(screen.getByTestId('sandboxes-refresh-error')).getByRole(
          'button',
          { name: 'Retry' },
        ),
      );
      expect(failed.refetch).toHaveBeenCalledTimes(1);
    });

    it('is not blamed on the label selector when the filtered list had loaded', () => {
      mockUseSandboxes.mockReturnValue(loaded(sandboxes(2)));
      const view = render(page());
      const selector = within(
        screen.getByTestId('sandbox-label-selector'),
      ).getByRole('textbox');
      fireEvent.change(selector, { target: { value: 'team=ml' } });
      fireEvent.click(
        screen.getByRole('button', { name: 'Apply label selector' }),
      );

      mockUseSandboxes.mockReturnValue(refreshFailed(sandboxes(2)));
      view.rerender(page());

      expect(screen.getByTestId('sandboxes-refresh-error')).toBeInTheDocument();
      expect(
        screen.queryByTestId('sandbox-label-selector-error'),
      ).not.toBeInTheDocument();
      expect(shownNames()).toEqual(['sb-01', 'sb-02']);
    });

    it('still takes the page when the list never loaded', () => {
      mockUseSandboxes.mockReturnValue({
        isLoading: false,
        isError: true,
        error: new Error('bad gateway'),
        data: undefined,
        refetch: jest.fn(),
      });
      render(page());
      expect(screen.getByText('Failed to load sandboxes')).toBeInTheDocument();
      expect(
        screen.queryByTestId('sandboxes-refresh-error'),
      ).not.toBeInTheDocument();
    });
  });

  // The empty state and the list are different trees. The Create sandbox
  // form has to sit outside both, or a poll that crosses from one to the
  // other mounts a new, empty form in place of the one being filled in.
  describe('a list that empties or fills under an open Create sandbox form', () => {
    it('keeps what was typed when the first sandbox appears', () => {
      mockUseSandboxes.mockReturnValue(loaded([]));
      const view = render(page());
      fireEvent.click(screen.getByTestId('create-sandbox-empty'));
      fireEvent.change(formName(), { target: { value: 'my-first' } });

      // Somebody else created one, and the poll lands.
      mockUseSandboxes.mockReturnValue(loaded(sandboxes(1)));
      view.rerender(page());

      expect(shownNames()).toEqual(['sb-01']);
      expect(formName()).toHaveValue('my-first');
    });

    it('keeps what was typed when the last sandbox goes', () => {
      mockUseSandboxes.mockReturnValue(loaded(sandboxes(1)));
      const view = render(page());
      fireEvent.click(screen.getByTestId('create-sandbox'));
      fireEvent.change(formName(), { target: { value: 'another' } });

      mockUseSandboxes.mockReturnValue(loaded([]));
      view.rerender(page());

      expect(screen.getByTestId('create-sandbox-empty')).toBeInTheDocument();
      expect(formName()).toHaveValue('another');
    });
  });

  describe('a list that gets shorter than the page it is on', () => {
    it('shows the last page that has rows, not an empty table', () => {
      mockUseSandboxes.mockReturnValue(loaded(sandboxes(11)));
      const view = render(page());
      fireEvent.click(
        bottomPagination().getByRole('button', { name: 'Go to next page' }),
      );
      expect(shownNames()).toEqual(['sb-11']);

      // The eleventh sandbox is deleted, here or elsewhere.
      mockUseSandboxes.mockReturnValue(loaded(sandboxes(10)));
      view.rerender(page());

      expect(shownNames()).toHaveLength(10);
      expect(
        screen.queryByText('No sandboxes match this filter.'),
      ).not.toBeInTheDocument();
    });
  });

  describe('the selection', () => {
    const filterByName = (text: string) =>
      fireEvent.change(
        screen.getByRole('textbox', {
          name: 'Filter current page of sandboxes by name',
        }),
        { target: { value: text } },
      );

    it('clears selection when the current-page name filter changes', () => {
      mockUseSandboxes.mockReturnValue(loaded(sandboxes(3)));
      render(page());
      selectRow('sb-01');
      selectRow('sb-03');
      openActions();
      expect(screen.getByTestId('delete-selected-sandboxes')).toHaveTextContent(
        'Delete selected (2)',
      );

      filterByName('sb-01');
      expect(screen.getByTestId('delete-selected-sandboxes')).toHaveTextContent(
        'Delete selected',
      );

      // Clearing the filter does not select it again behind the user's back.
      filterByName('');
      expect(screen.getByTestId('delete-selected-sandboxes')).toHaveTextContent(
        'Delete selected',
      );
    });

    it('clears selection when the label selector changes', () => {
      mockUseSandboxes.mockImplementation((_workspace, selector) =>
        loaded(selector ? [sandbox(2)] : sandboxes(3)),
      );
      render(page());
      selectRow('sb-01');
      selectRow('sb-02');

      fireEvent.change(
        within(screen.getByTestId('sandbox-label-selector')).getByRole(
          'textbox',
        ),
        { target: { value: 'team=ml' } },
      );
      fireEvent.click(
        screen.getByRole('button', { name: 'Apply label selector' }),
      );

      openActions();
      expect(deleteSelected()).toBeDisabled();
    });

    it('lets go of a sandbox that is gone from the list', () => {
      mockUseSandboxes.mockReturnValue(loaded(sandboxes(3)));
      const view = render(page());
      selectRow('sb-02');
      selectRow('sb-03');

      mockUseSandboxes.mockReturnValue(loaded(sandboxes(2)));
      view.rerender(page());

      openActions();
      expect(screen.getByTestId('delete-selected-sandboxes')).toHaveTextContent(
        'Delete selected (1)',
      );
    });

    it('names every sandbox a bulk delete is about to delete, also those on another page', () => {
      mockUseSandboxes.mockReturnValue(loaded(sandboxes(11)));
      render(page());
      selectRow('sb-01');
      fireEvent.click(
        bottomPagination().getByRole('button', { name: 'Go to next page' }),
      );
      selectRow('sb-11');

      openActions();
      fireEvent.click(deleteSelected());

      expect(
        screen.getByText(
          '2 sandboxes will be permanently deleted: sb-01, sb-11. This cannot be undone.',
        ),
      ).toBeInTheDocument();
      fireEvent.click(screen.getByTestId('confirm-delete'));
      expect(mockRunDelete).toHaveBeenCalledWith(
        ['sb-01', 'sb-11'],
        expect.any(Function),
      );
    });

    it('counts the sandboxes it does not spell out in a long bulk delete', () => {
      mockUseSandboxes.mockReturnValue(loaded(sandboxes(12)));
      render(page());
      fireEvent.change(
        screen.getByRole('combobox', { name: 'Items per page' }),
        { target: { value: '20' } },
      );
      fireEvent.click(
        screen.getByRole('checkbox', { name: 'Select all rows' }),
      );

      openActions();
      fireEvent.click(deleteSelected());

      expect(
        screen.getByText(
          '12 sandboxes will be permanently deleted: sb-01, sb-02, sb-03, sb-04, sb-05, sb-06, sb-07, sb-08, sb-09, sb-10 and 2 more. This cannot be undone.',
        ),
      ).toBeInTheDocument();
    });
  });
});
