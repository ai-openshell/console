import React from 'react';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import SandboxDetailPage from '../SandboxDetailPage';
import type { Sandbox } from '../../types';

let mockFlags = {
  terminal: false,
  fileTransfer: false,
  settings: true,
  globalPolicy: true,
  credentialRefresh: true,
  services: false,
  draftPolicy: false,
};

jest.mock('../../api/auth', () => ({
  useFeatureFlags: jest.fn(() => mockFlags),
}));

jest.mock('../../api/sandboxes', () => ({
  useSandbox: jest.fn(),
  useStartSandbox: jest.fn(() => ({ mutate: jest.fn(), isPending: false })),
  useSuspendSandbox: jest.fn(() => ({ mutate: jest.fn(), isPending: false })),
  useResumeSandbox: jest.fn(() => ({ mutate: jest.fn(), isPending: false })),
  useStopSandbox: jest.fn(() => ({ mutate: jest.fn(), isPending: false })),
  // The header and its actions are covered in SandboxDetailPage.header.spec.tsx.
  useDeleteSandbox: jest.fn(() => ({
    mutate: jest.fn(),
    reset: jest.fn(),
    isPending: false,
    isError: false,
  })),
}));

jest.mock('../../api/policy', () => ({
  useDraftPolicy: jest.fn(() => ({ data: undefined })),
  useSandboxPolicy: jest.fn(() => ({ data: undefined })),
}));

jest.mock('../../api/providers', () => ({
  useProviderExpiry: jest.fn(() => ({ expiring: [], expired: [] })),
}));

jest.mock('../../app/AlertContext', () => ({
  useAlerts: jest.fn(() => ({
    addAlert: jest.fn(),
    addSuccess: jest.fn(),
    addDanger: jest.fn(),
  })),
}));

// The banner has a spec of its own. What this page decides is which of its
// links there is anything behind from the tab that is open.
const mockAttentionProps: { onReviewDrafts?: () => void }[] = [];
const lastAttention = () => mockAttentionProps[mockAttentionProps.length - 1];
jest.mock('../../components/sandbox/SandboxAttention', () => ({
  __esModule: true,
  default: (props: { onReviewDrafts?: () => void }) => {
    mockAttentionProps.push(props);
    return null;
  },
}));

// The tabs have specs of their own. Here each is a marker that says it was
// mounted, which is what this page decides.
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
jest.mock('../../components/sandbox/SandboxSettingsTab', () => ({
  __esModule: true,
  default: ({
    workspace,
    sandboxName,
  }: {
    workspace: string;
    sandboxName: string;
  }) => (
    <div data-testid="settings-tab-content">
      {workspace}/{sandboxName}
    </div>
  ),
}));

import { useSandbox } from '../../api/sandboxes';
const mockUseSandbox = useSandbox as jest.Mock;

const sandbox: Sandbox = {
  metadata: {
    id: 'sb-1',
    name: 'agent-1',
    workspace: 'team-a',
    createdAtMs: Date.now() - 60_000,
    resourceVersion: 2,
  },
  createdFromWorkloadTemplate: { name: 'claude-harness', resourceVersion: '7' },
  spec: {
    image: 'base',
    command: ['sleep', 'infinity'],
    gpu: true,
    gpuCount: 1,
    template: { resources: { limits: { cpu: '500m', memory: '512Mi' } } },
  },
  status: {
    phase: 'READY',
    currentPolicyVersion: 1,
    configurationAdmission: {
      state: 'ACCEPTED',
      policyVersion: 1,
      configRevision: '12',
      providerEnvRevision: '34',
    },
    endpointStatuses: [
      {
        endpointId: 'ep-1',
        host: 'mcp.example.test',
        ports: [443],
        lastResult: 'HTTP_RESPONSE_RECEIVED',
      },
    ],
  },
};

const renderPage = (activeTab?: string) => {
  mockUseSandbox.mockReturnValue({
    isLoading: false,
    isError: false,
    data: sandbox,
  });
  return render(
    <MemoryRouter>
      <SandboxDetailPage
        workspace="team-a"
        sandboxName="agent-1"
        activeTab={activeTab}
      />
    </MemoryRouter>,
  );
};

describe('SandboxDetailPage', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockAttentionProps.length = 0;
    mockFlags = { ...mockFlags, settings: true };
  });

  it('shows what the sandbox was created with on the Details tab', () => {
    renderPage();
    const details = screen.getByTestId('sandbox-details-card');
    expect(details).toHaveTextContent('CPU limit500m');
    expect(details).toHaveTextContent('Memory limit512Mi');
    expect(screen.getByTestId('sandbox-gpu')).toHaveTextContent('1');
    expect(screen.getByTestId('sandbox-command')).toHaveTextContent(
      'sleep infinity',
    );
    expect(screen.getByTestId('sandbox-workload-template')).toHaveTextContent(
      'claude-harness (revision 7)',
    );
  });

  it('shows the configuration admission and the tool server connections', () => {
    renderPage();
    expect(screen.getByTestId('sandbox-admission-state')).toHaveTextContent(
      'Accepted',
    );
    expect(screen.getByTestId('sandbox-endpoints-card')).toHaveTextContent(
      'mcp.example.test',
    );
  });

  it('has a Settings tab, and reads the settings only while it is open', () => {
    renderPage();
    expect(screen.getByRole('tab', { name: 'Settings' })).toBeInTheDocument();
    expect(
      screen.queryByTestId('settings-tab-content'),
    ).not.toBeInTheDocument();
  });

  it('opens the settings of this sandbox on the Settings tab', () => {
    renderPage('settings');
    expect(screen.getByTestId('settings-tab-content')).toHaveTextContent(
      'team-a/agent-1',
    );
  });

  // The banner sits above the tabs, so it is on screen on every one of them.
  // On the Proposals tab its Review link leads where the reader already is.
  it('offers no Review of the proposals from the Proposals tab', () => {
    renderPage('proposals');
    expect(lastAttention()?.onReviewDrafts).toBeUndefined();
  });

  it('offers it from every other tab', () => {
    renderPage('details');
    expect(lastAttention()?.onReviewDrafts).toBeDefined();
  });

  it('has no Settings tab where the settings feature is off', () => {
    mockFlags = { ...mockFlags, settings: false };
    renderPage('settings');
    expect(
      screen.queryByRole('tab', { name: 'Settings' }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByTestId('settings-tab-content'),
    ).not.toBeInTheDocument();
  });
});
