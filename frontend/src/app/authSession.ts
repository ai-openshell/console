const PROXY_REAUTH_RELOAD_KEY = 'console.proxyReauthReload';

/** Clear the one-shot reload flag after a successful whoami / re-auth. */
export const clearProxyReauthReloadFlag = (): void => {
  sessionStorage.removeItem(PROXY_REAUTH_RELOAD_KEY);
};

/** @internal For unit tests — sessionStorage gate before reload. */
export const tryMarkProxyReauthReload = (): boolean => {
  if (sessionStorage.getItem(PROXY_REAUTH_RELOAD_KEY)) {
    return false;
  }
  sessionStorage.setItem(PROXY_REAUTH_RELOAD_KEY, '1');
  return true;
};

/**
 * Reload once so a fronting auth proxy can re-authenticate the document
 * request (ADR 0002). Registered via setSessionExpiredHandler after bootstrap
 * whoami succeeds — not during initial unauthenticated load.
 */
export const reloadOnceForProxyReauth = (): void => {
  if (!tryMarkProxyReauthReload()) {
    return;
  }
  window.location.reload();
};
