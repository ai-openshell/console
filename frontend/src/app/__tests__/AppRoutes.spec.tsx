import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';

import type { AuthConfig, CurrentUser } from '../../types';
import {
  sessionExpiredError,
  setSessionExpiredHandler,
} from '../../api/client';

jest.mock('../../api/auth', () => ({
  useAuthConfig: jest.fn(),
  useCurrentUser: jest.fn(),
}));

jest.mock('../AuthenticatedRoutes', () => {
  const MockAuthenticatedRoutes = ({
    sessionAlert,
  }: {
    sessionAlert?: React.ReactNode;
  }) => {
    const [draft, setDraft] = React.useState('');
    React.useEffect(() => () => mockShellUnmount(), []);
    return (
      <div data-testid="authenticated-shell">
        {sessionAlert}
        <label>
          Unsaved draft
          <input
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
          />
        </label>
      </div>
    );
  };
  return { __esModule: true, default: MockAuthenticatedRoutes };
});

jest.mock('../authSession', () => ({
  ...jest.requireActual('../authSession'),
  reloadOnceForProxyReauth: jest.fn(),
}));

jest.mock('../logout', () => ({
  redirectToProxySignOut: jest.fn(),
}));

import AppRoutes from '../AppRoutes';
import { useAuthConfig, useCurrentUser } from '../../api/auth';
import { catalogs } from '../../i18n';
import { reloadOnceForProxyReauth } from '../authSession';
import { redirectToProxySignOut } from '../logout';

const mockUseAuthConfig = useAuthConfig as jest.Mock;
const mockUseCurrentUser = useCurrentUser as jest.Mock;
const mockSignOut = redirectToProxySignOut as jest.Mock;
const mockAutoReload = reloadOnceForProxyReauth as jest.Mock;
const mockShellUnmount = jest.fn();

const features: AuthConfig['features'] = {
  terminal: true,
  fileTransfer: true,
  settings: true,
  globalPolicy: true,
  credentialRefresh: true,
  services: true,
  draftPolicy: true,
};

const idleWhoami = {
  data: undefined,
  isLoading: false,
  isPending: false,
  isFetching: false,
  isError: false,
  error: null,
  refetch: jest.fn(),
};

const gate = () => (
  <MemoryRouter
    future={{ v7_startTransition: true, v7_relativeSplatPath: true }}
  >
    <AppRoutes />
  </MemoryRouter>
);

const renderGate = () => render(gate());

const requireAuth = (logoutUrl?: string) => {
  mockUseAuthConfig.mockReturnValue({
    data: { authDisabled: false, features, logoutUrl } satisfies AuthConfig,
    isLoading: false,
  });
};

describe('AppRoutes', () => {
  beforeEach(() => {
    sessionStorage.clear();
    jest.clearAllMocks();
    setSessionExpiredHandler(null);
    mockUseCurrentUser.mockReturnValue(idleWhoami);
  });

  afterEach(() => {
    setSessionExpiredHandler(null);
  });

  it('shows the dev login page when AUTH_DISABLED is true', () => {
    mockUseAuthConfig.mockReturnValue({
      data: { authDisabled: true, features } satisfies AuthConfig,
      isLoading: false,
    });

    renderGate();

    expect(screen.getByTestId('dev-login')).toBeInTheDocument();
    expect(screen.queryByTestId('authenticated-shell')).not.toBeInTheDocument();
  });

  it('shows Authentication required when the BFF has no bearer', () => {
    requireAuth();
    mockUseCurrentUser.mockReturnValue({
      ...idleWhoami,
      isError: true,
      error: { status: 401, code: 'unauthorized', message: 'Session expired' },
    });

    renderGate();

    expect(screen.getByTestId('auth-required')).toBeInTheDocument();
    expect(screen.getByText('make dev').closest('code')).toBeInTheDocument();
    expect(screen.queryByTestId('authenticated-shell')).not.toBeInTheDocument();
  });

  it('shows the gateway rejection over cached user data and signs out via the configured proxy', () => {
    requireAuth('/custom/sign_out');
    mockUseCurrentUser.mockReturnValue({
      ...idleWhoami,
      data: { subject: 'cached-user', roles: [] } satisfies CurrentUser,
      isError: true,
      error: {
        status: 401,
        code: 'unauthenticated',
        message: 'token rejected',
      },
    });

    renderGate();

    expect(
      screen.getByText(catalogs.en.auth.sessionRejectedTitle),
    ).toBeInTheDocument();
    expect(
      screen.getByText(catalogs.en.auth.sessionRejectedBody),
    ).toBeInTheDocument();
    expect(screen.getByText('token rejected')).toBeInTheDocument();
    expect(screen.queryByTestId('auth-required')).not.toBeInTheDocument();
    expect(screen.queryByTestId('authenticated-shell')).not.toBeInTheDocument();
    sessionExpiredError('unauthorized');
    expect(mockAutoReload).not.toHaveBeenCalled();
    fireEvent.click(
      screen.getByRole('button', { name: catalogs.en.auth.sessionSignOut }),
    );
    expect(mockSignOut).toHaveBeenCalledWith('/custom/sign_out');
  });

  it('shows a generic session error and sign-out for an unknown 401 code', () => {
    requireAuth();
    mockUseCurrentUser.mockReturnValue({
      ...idleWhoami,
      isError: true,
      error: {
        status: 401,
        code: 'new_auth_code',
        message: 'unknown response',
      },
    });

    renderGate();

    expect(
      screen.getByText(catalogs.en.auth.sessionVerifyFailed),
    ).toBeInTheDocument();
    expect(
      screen.getByText(catalogs.en.auth.sessionUnknownUnauthorizedBody),
    ).toBeInTheDocument();
    expect(screen.queryByText('unknown response')).not.toBeInTheDocument();
    expect(screen.queryByTestId('auth-required')).not.toBeInTheDocument();
    expect(screen.queryByTestId('authenticated-shell')).not.toBeInTheDocument();
    fireEvent.click(
      screen.getByRole('button', { name: catalogs.en.auth.sessionSignOut }),
    );
    expect(mockSignOut).toHaveBeenCalledWith(undefined);
  });

  it('shows the gateway response for 502 and retries whoami', () => {
    const refetch = jest.fn();
    requireAuth();
    mockUseCurrentUser.mockReturnValue({
      ...idleWhoami,
      isError: true,
      error: {
        status: 502,
        code: 'gateway_unavailable',
        message: 'OpenShell gateway is unreachable',
      },
      refetch,
    });

    renderGate();

    expect(
      screen.getByText('OpenShell gateway is unreachable'),
    ).toBeInTheDocument();
    expect(
      screen.queryByText(catalogs.en.auth.sessionBffUnreachable),
    ).not.toBeInTheDocument();
    expect(
      screen.getByText(catalogs.en.auth.sessionGatewayUnavailableHelp),
    ).toBeInTheDocument();
    expect(screen.queryByTestId('authenticated-shell')).not.toBeInTheDocument();
    fireEvent.click(
      screen.getByRole('button', { name: catalogs.en.common.actions.retry }),
    );
    expect(refetch).toHaveBeenCalledTimes(1);
  });

  it('advises users to contact an administrator and sign out for permission errors', () => {
    const refetch = jest.fn();
    requireAuth('/custom/sign_out');
    mockUseCurrentUser.mockReturnValue({
      ...idleWhoami,
      isError: true,
      error: {
        status: 403,
        code: 'permission_denied',
        message: 'Access denied',
      },
      refetch,
    });

    renderGate();

    expect(screen.getByText('Access denied')).toBeInTheDocument();
    expect(
      screen.getByText(catalogs.en.auth.sessionPermissionDeniedHelp),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: catalogs.en.common.actions.retry }),
    ).not.toBeInTheDocument();
    fireEvent.click(
      screen.getByRole('button', { name: catalogs.en.auth.sessionSignOut }),
    );
    expect(mockSignOut).toHaveBeenCalledWith('/custom/sign_out');
    expect(refetch).not.toHaveBeenCalled();
    expect(screen.queryByTestId('authenticated-shell')).not.toBeInTheDocument();
  });

  it('shows network recovery guidance for a network failure', () => {
    requireAuth();
    mockUseCurrentUser.mockReturnValue({
      ...idleWhoami,
      isError: true,
      error: new TypeError('Failed to fetch'),
    });

    renderGate();

    expect(
      screen.getByText(catalogs.en.auth.sessionBffUnreachable),
    ).toBeInTheDocument();
    expect(screen.queryByText('Failed to fetch')).not.toBeInTheDocument();
    expect(screen.queryByTestId('authenticated-shell')).not.toBeInTheDocument();
  });

  it.each([
    [502, 'gateway_unavailable'],
    [408, 'timeout'],
    [429, 'rate_limited'],
    [undefined, undefined],
  ])(
    'keeps cached page state during a transient whoami failure (%s)',
    (status, code) => {
      const refetch = jest.fn();
      const user: CurrentUser = { subject: 'cached-user', roles: [] };
      requireAuth();
      mockUseCurrentUser.mockReturnValue({
        ...idleWhoami,
        data: user,
        refetch,
      });
      const view = renderGate();
      fireEvent.change(screen.getByRole('textbox', { name: 'Unsaved draft' }), {
        target: { value: 'keep this' },
      });
      sessionStorage.setItem('console.proxyReauthReload', '1');
      mockUseCurrentUser.mockReturnValue({
        ...idleWhoami,
        data: user,
        isError: true,
        error:
          status === undefined
            ? new TypeError('Failed to fetch')
            : { status, code, message: 'Temporary whoami failure' },
        refetch,
      });
      view.rerender(gate());

      expect(screen.getByTestId('authenticated-shell')).toBeInTheDocument();
      expect(
        screen.getByRole('textbox', { name: 'Unsaved draft' }),
      ).toHaveValue('keep this');
      expect(mockShellUnmount).not.toHaveBeenCalled();
      expect(
        screen.getByText(
          status === undefined
            ? catalogs.en.auth.sessionBffUnreachable
            : 'Temporary whoami failure',
        ),
      ).toBeInTheDocument();
      expect(sessionStorage.getItem('console.proxyReauthReload')).toBe('1');
      sessionExpiredError('unauthorized');
      expect(mockAutoReload).toHaveBeenCalledTimes(1);
      fireEvent.click(
        screen.getByRole('button', { name: catalogs.en.common.actions.retry }),
      );
      expect(refetch).toHaveBeenCalledTimes(1);

      mockUseCurrentUser.mockReturnValue({
        ...idleWhoami,
        data: user,
        refetch,
      });
      view.rerender(gate());
      expect(
        screen.getByRole('textbox', { name: 'Unsaved draft' }),
      ).toHaveValue('keep this');
      expect(
        screen.queryByText('Temporary whoami failure'),
      ).not.toBeInTheDocument();
      expect(mockShellUnmount).not.toHaveBeenCalled();
      expect(sessionStorage.getItem('console.proxyReauthReload')).toBeNull();
    },
  );

  it.each([
    [403, 'permission_denied'],
    [422, 'invalid_argument'],
  ])('blocks cached user data on a client error (%s)', (status, code) => {
    requireAuth();
    mockUseCurrentUser.mockReturnValue({
      ...idleWhoami,
      data: { subject: 'cached-user', roles: [] } satisfies CurrentUser,
      isError: true,
      error: {
        status,
        code,
        message: 'Access denied',
      },
    });

    renderGate();

    expect(screen.getByText('Access denied')).toBeInTheDocument();
    expect(screen.queryByTestId('authenticated-shell')).not.toBeInTheDocument();
  });

  it('shows loading spinner while whoami is pending', () => {
    mockUseAuthConfig.mockReturnValue({
      data: { authDisabled: false, features } satisfies AuthConfig,
      isLoading: false,
    });
    mockUseCurrentUser.mockReturnValue({
      data: undefined,
      isLoading: false,
      isPending: true,
      isError: false,
      error: null,
      refetch: jest.fn(),
    });

    renderGate();

    expect(
      screen.getByLabelText(catalogs.en.auth.sessionLoading),
    ).toBeInTheDocument();
    expect(screen.queryByTestId('auth-required')).not.toBeInTheDocument();
    expect(screen.queryByTestId('authenticated-shell')).not.toBeInTheDocument();
  });

  it('mounts the authenticated shell after a successful whoami', () => {
    const user: CurrentUser = { subject: 'user-1', roles: [] };
    mockUseAuthConfig.mockReturnValue({
      data: { authDisabled: false, features } satisfies AuthConfig,
      isLoading: false,
    });
    mockUseCurrentUser.mockReturnValue({
      data: user,
      isLoading: false,
      isPending: false,
      isError: false,
      error: null,
      refetch: jest.fn(),
    });

    renderGate();

    expect(screen.getByTestId('authenticated-shell')).toBeInTheDocument();
  });
});
