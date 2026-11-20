import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import SandboxListPage from '../SandboxListPage';
import type { Sandbox } from '../../types';

const mockSandbox: Sandbox = {
  metadata: {
    id: 'uuid-1',
    name: 'test-sandbox',
    workspace: 'default',
    createdAtMs: Date.now() - 60_000,
    resourceVersion: 1,
    labels: { team: 'ml' },
  },
  spec: {
    image: 'ghcr.io/nvidia/openshell-community/sandboxes/python:latest',
    providers: ['claude'],
    policy: { version: 1, networkPolicies: {} },
  },
  status: {
    phase: 'READY',
    currentPolicyVersion: 1,
  },
};

jest.mock('../../api/sandboxes', () => ({
  useSandboxes: jest.fn(),
  useSandboxPage: jest.fn(),
  deleteSandbox: jest.fn(),
  useStopSandbox: jest.fn(() => ({ mutate: jest.fn(), isPending: false })),
  useStartSandbox: jest.fn(() => ({ mutate: jest.fn(), isPending: false })),
  useSuspendSandbox: jest.fn(() => ({ mutate: jest.fn(), isPending: false })),
  useResumeSandbox: jest.fn(() => ({ mutate: jest.fn(), isPending: false })),
  useCreateSandbox: jest.fn(() => ({
    mutate: jest.fn(),
    reset: jest.fn(),
    isPending: false,
    isError: false,
    error: null,
  })),
}));

jest.mock('../../api/auth', () => ({
  useFeatureFlags: jest.fn(() => ({
    terminal: true,
    fileTransfer: true,
    settings: true,
    globalPolicy: true,
    credentialRefresh: true,
    services: true,
    draftPolicy: true,
  })),
}));

jest.mock('../../api/policy', () => ({
  useSandboxPolicies: jest.fn(() => ({})),
}));

// The list, its filters and its badges are covered in
// SandboxListPage.list.spec.tsx.
jest.mock('../../api/draftSummary', () => ({
  useWorkspaceDraftSummary: jest.fn(() => ({
    items: [],
    bySandbox: {},
    totalPending: 0,
  })),
}));

jest.mock('../../api/providers', () => ({
  useProviderExpiry: jest.fn(() => ({ expiring: [], expired: [] })),
  useProviders: jest.fn(() => ({ data: [] })),
}));

jest.mock('../../app/AlertContext', () => ({
  useAlerts: jest.fn(() => ({
    addAlert: jest.fn(),
    addSuccess: jest.fn(),
    addDanger: jest.fn(),
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

jest.mock('../../hooks/useJsonValidation', () => ({
  useJsonValidation: jest.fn((text: string) => {
    try {
      return { error: null, parsed: JSON.parse(text) };
    } catch {
      return { error: 'invalid', parsed: null };
    }
  }),
}));

import { useSandboxes, useSandboxPage } from '../../api/sandboxes';
import { pagedResult } from './pagedHookMock';
const mockUseSandboxes = useSandboxes as jest.Mock;
const mockUseSandboxPage = useSandboxPage as jest.Mock;

beforeEach(() => {
  mockUseSandboxPage.mockImplementation((workspace, request) =>
    pagedResult(mockUseSandboxes(workspace, request.labelSelector), request),
  );
});

const renderPage = () =>
  render(
    <MemoryRouter>
      <SandboxListPage workspace="default" />
    </MemoryRouter>,
  );

describe('SandboxListPage', () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  it('shows spinner during loading', () => {
    mockUseSandboxes.mockReturnValue({
      isLoading: true,
      isError: false,
      data: undefined,
    });
    renderPage();
    expect(screen.getByLabelText('Loading sandboxes')).toBeInTheDocument();
  });

  it('shows error alert on fetch failure', () => {
    mockUseSandboxes.mockReturnValue({
      isLoading: false,
      isError: true,
      error: new Error('Gateway unreachable'),
      refetch: jest.fn(),
    });
    renderPage();
    expect(screen.getByText('Failed to load sandboxes')).toBeInTheDocument();
    expect(screen.getByText('Gateway unreachable')).toBeInTheDocument();
    expect(screen.getByText('Retry')).toBeInTheDocument();
  });

  it('shows empty state when no sandboxes exist', () => {
    mockUseSandboxes.mockReturnValue({
      isLoading: false,
      isError: false,
      data: [],
    });
    renderPage();
    expect(screen.getByText('No sandboxes')).toBeInTheDocument();
    expect(screen.getByTestId('create-sandbox-empty')).toBeInTheDocument();
  });

  it('renders sandbox table with view toggle when sandboxes exist', () => {
    mockUseSandboxes.mockReturnValue({
      isLoading: false,
      isError: false,
      data: [mockSandbox],
    });
    renderPage();
    expect(screen.getByTestId('sandbox-table')).toBeInTheDocument();
    expect(screen.getByTestId('view-toggle')).toBeInTheDocument();
    expect(screen.getByTestId('create-sandbox')).toBeInTheDocument();
    expect(screen.getByText('test-sandbox')).toBeInTheDocument();
  });

  it('renders toolbarStart content and filters by sandbox name', () => {
    mockUseSandboxes.mockReturnValue({
      isLoading: false,
      isError: false,
      data: [
        mockSandbox,
        {
          ...mockSandbox,
          metadata: {
            ...mockSandbox.metadata,
            id: 'uuid-2',
            name: 'research-runner',
          },
        },
      ],
    });
    render(
      <MemoryRouter>
        <SandboxListPage
          workspace="default"
          toolbarStart={<span>Workspace selector</span>}
        />
      </MemoryRouter>,
    );

    expect(screen.getByText('Workspace selector')).toBeInTheDocument();
    fireEvent.change(
      screen.getByPlaceholderText('Filter current page by name'),
      {
        target: { value: 'research' },
      },
    );
    expect(screen.getByText('research-runner')).toBeInTheDocument();
    expect(screen.queryByText('test-sandbox')).not.toBeInTheDocument();
  });
});
