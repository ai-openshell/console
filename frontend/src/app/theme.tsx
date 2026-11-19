import { createContext, useContext, useState } from 'react';

export type Theme = 'light' | 'dark';

const THEME_STORAGE_KEY = 'console-theme';
const DARK_THEME_CLASS = 'pf-v6-theme-dark';

// Storage can throw — blocked site data, some private windows — and the theme
// is read at boot, so a failure to read or write it is no theme remembered,
// never an app that does not start.
export const getStoredTheme = (): Theme | null => {
  try {
    const stored = window.localStorage.getItem(THEME_STORAGE_KEY);
    return stored === 'light' || stored === 'dark' ? stored : null;
  } catch {
    return null;
  }
};

const storeTheme = (theme: Theme): void => {
  try {
    window.localStorage.setItem(THEME_STORAGE_KEY, theme);
  } catch {
    // Not remembered; the toggle still applies for this visit.
  }
};

export const getSystemTheme = (): Theme =>
  window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';

export const applyTheme = (theme: Theme): void => {
  document.documentElement.classList.toggle(DARK_THEME_CLASS, theme === 'dark');
};

export const initTheme = (): Theme => {
  const theme = getStoredTheme() ?? getSystemTheme();
  applyTheme(theme);
  return theme;
};

type ThemeContextValue = { theme: Theme; toggleTheme: () => void };

// Light by default, so a component rendered outside the provider, as in a
// unit test, is readable.
const ThemeContext = createContext<ThemeContextValue>({
  theme: 'light',
  toggleTheme: () => undefined,
});

// The theme the whole app reads: the masthead toggle, the two policy document
// editors, the log viewer and the terminal. Context rather than a hook with
// state of its own, because those are several call sites and several copies
// of useState would not see each other's toggle.
export const useTheme = (): ThemeContextValue => useContext(ThemeContext);

export const ThemeProvider: React.FC<{ children: React.ReactNode }> = ({
  children,
}) => {
  const [theme, setTheme] = useState<Theme>(
    () => getStoredTheme() ?? getSystemTheme(),
  );

  const toggleTheme = () => {
    const next: Theme = theme === 'dark' ? 'light' : 'dark';
    applyTheme(next);
    storeTheme(next);
    setTheme(next);
  };

  return (
    <ThemeContext.Provider value={{ theme, toggleTheme }}>
      {children}
    </ThemeContext.Provider>
  );
};
