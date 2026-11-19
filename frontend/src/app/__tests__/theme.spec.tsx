import { fireEvent, render, screen } from '@testing-library/react';

import { ThemeProvider, useTheme } from '../theme';

const THEME_STORAGE_KEY = 'console-theme';
const DARK_THEME_CLASS = 'pf-v6-theme-dark';

// Two surfaces of the kind the provider exists for: the masthead toggle and
// one of the editors that only reads the theme.
const Toggle: React.FC = () => {
  const { theme, toggleTheme } = useTheme();
  return (
    <button type="button" onClick={toggleTheme} data-testid="toggle">
      {theme}
    </button>
  );
};

const Reader: React.FC = () => {
  const { theme } = useTheme();
  return <span data-testid="reader">{theme}</span>;
};

beforeEach(() => {
  window.localStorage.clear();
  document.documentElement.classList.remove(DARK_THEME_CLASS);
  // jsdom has no matchMedia, and the provider asks the system for a theme
  // whenever none was stored.
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: () => ({ matches: false }),
  });
});

describe('ThemeProvider', () => {
  it('starts from the stored theme', () => {
    window.localStorage.setItem(THEME_STORAGE_KEY, 'dark');
    render(
      <ThemeProvider>
        <Reader />
      </ThemeProvider>,
    );
    expect(screen.getByTestId('reader')).toHaveTextContent('dark');
  });

  it('falls back to the system theme when none was stored', () => {
    render(
      <ThemeProvider>
        <Reader />
      </ThemeProvider>,
    );
    expect(screen.getByTestId('reader')).toHaveTextContent('light');
  });

  it('takes every surface with it when the theme is toggled', () => {
    render(
      <ThemeProvider>
        <Toggle />
        <Reader />
      </ThemeProvider>,
    );

    fireEvent.click(screen.getByTestId('toggle'));

    // The reader is what a hook with state of its own would have left behind.
    expect(screen.getByTestId('toggle')).toHaveTextContent('dark');
    expect(screen.getByTestId('reader')).toHaveTextContent('dark');
    expect(document.documentElement).toHaveClass(DARK_THEME_CLASS);
    expect(window.localStorage.getItem(THEME_STORAGE_KEY)).toBe('dark');

    fireEvent.click(screen.getByTestId('toggle'));

    expect(screen.getByTestId('reader')).toHaveTextContent('light');
    expect(document.documentElement).not.toHaveClass(DARK_THEME_CLASS);
    expect(window.localStorage.getItem(THEME_STORAGE_KEY)).toBe('light');
  });

  // Storage that throws — blocked site data, some private windows — must not
  // stop the app from starting or the toggle from working.
  it('starts and toggles when storage throws', () => {
    const getItem = jest
      .spyOn(Storage.prototype, 'getItem')
      .mockImplementation(() => {
        throw new Error('blocked');
      });
    const setItem = jest
      .spyOn(Storage.prototype, 'setItem')
      .mockImplementation(() => {
        throw new Error('blocked');
      });
    try {
      render(
        <ThemeProvider>
          <Toggle />
          <Reader />
        </ThemeProvider>,
      );
      fireEvent.click(screen.getByTestId('toggle'));
      expect(screen.getByTestId('reader')).toHaveTextContent('dark');
      expect(document.documentElement).toHaveClass(DARK_THEME_CLASS);
    } finally {
      getItem.mockRestore();
      setItem.mockRestore();
    }
  });

  it('reads as light outside a provider', () => {
    render(<Reader />);
    expect(screen.getByTestId('reader')).toHaveTextContent('light');
  });
});
