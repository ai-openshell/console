/** @jest-environment node */

import { getAuthConfig } from '../../api/auth';
import type { AuthConfig } from '../../types';
import { logout } from '../logout';

jest.mock('../../api/auth', () => ({ getAuthConfig: jest.fn() }));

const mockGetAuthConfig = getAuthConfig as jest.MockedFunction<
  typeof getAuthConfig
>;
const assign = jest.fn();
const removeItem = jest.fn();

const authConfig = (overrides: Partial<AuthConfig> = {}): AuthConfig =>
  ({ authDisabled: false, ...overrides }) as AuthConfig;

describe('logout', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    Object.defineProperty(globalThis, 'window', {
      configurable: true,
      value: { location: { assign }, sessionStorage: { removeItem } },
    });
    mockGetAuthConfig.mockResolvedValue(
      authConfig({ logoutUrl: '/oauth2/sign_out?rd=/oauth2/sign_in' }),
    );
  });

  afterAll(() => {
    Reflect.deleteProperty(globalThis, 'window');
  });

  it('sends the browser through proxy sign-out to its sign-in page', async () => {
    await logout();

    expect(assign).toHaveBeenCalledWith('/oauth2/sign_out?rd=/oauth2/sign_in');
    expect(removeItem).toHaveBeenCalledWith('console.devMode');
  });

  it('uses the proxy sign-in redirect when config omits the URL', async () => {
    mockGetAuthConfig.mockResolvedValue(authConfig());

    await logout();

    expect(assign).toHaveBeenCalledWith('/oauth2/sign_out?rd=/oauth2/sign_in');
  });

  it('keeps custom proxy URLs as browser navigations', async () => {
    mockGetAuthConfig.mockResolvedValue(
      authConfig({ logoutUrl: '/platform/logout' }),
    );

    await logout();

    expect(assign).toHaveBeenCalledWith('/platform/logout');
  });

  it('keeps dev logout on the dev login page', async () => {
    mockGetAuthConfig.mockResolvedValue(authConfig({ authDisabled: true }));

    await logout();

    expect(assign).toHaveBeenCalledWith('/login');
  });
});
