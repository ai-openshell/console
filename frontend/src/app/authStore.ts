// Client-side auth state. The session belongs to the auth proxy in front of
// the BFF, which keeps it in its own HttpOnly cookie: JavaScript never sees a
// token. The only state kept here is the dev-mode flag ("Continue as
// developer" when AUTH_DISABLED=true), which is not a credential.
const DEV_MODE_KEY = 'console.devMode';

export const isDevSession = (): boolean =>
  window.sessionStorage.getItem(DEV_MODE_KEY) === 'true';

export const setDevSession = (): void => {
  window.sessionStorage.setItem(DEV_MODE_KEY, 'true');
};

export const clearDevSession = (): void => {
  window.sessionStorage.removeItem(DEV_MODE_KEY);
};
