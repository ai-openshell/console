import {
  clearProxyReauthReloadFlag,
  reloadOnceForProxyReauth,
  tryMarkProxyReauthReload,
} from '../authSession';

const KEY = 'console.proxyReauthReload';

describe('authSession', () => {
  beforeEach(() => {
    sessionStorage.clear();
  });

  it('tryMarkProxyReauthReload allows only one slot per tab session', () => {
    expect(tryMarkProxyReauthReload()).toBe(true);
    expect(tryMarkProxyReauthReload()).toBe(false);
    expect(sessionStorage.getItem(KEY)).toBe('1');
  });

  it('reloadOnceForProxyReauth consumes only one reload slot per tab session', () => {
    reloadOnceForProxyReauth();
    reloadOnceForProxyReauth();

    expect(sessionStorage.getItem(KEY)).toBe('1');
  });

  it('clearProxyReauthReloadFlag allows a subsequent reload slot', () => {
    reloadOnceForProxyReauth();
    clearProxyReauthReloadFlag();
    expect(tryMarkProxyReauthReload()).toBe(true);
  });
});
