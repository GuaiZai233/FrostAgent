export type ThemeMode = 'system' | 'light' | 'dark';

const STORAGE_KEY = 'user-theme';
const listeners = new Set<(mode: ThemeMode, effective: 'light' | 'dark') => void>();

const mediaQuery = window.matchMedia('(prefers-color-scheme: dark)');

function loadPreference(): ThemeMode {
  const stored = localStorage.getItem(STORAGE_KEY);
  if (stored === 'light' || stored === 'dark' || stored === 'system') {
    return stored;
  }
  return 'system';
}

let currentMode: ThemeMode = loadPreference();

export function getEffectiveMode(): 'light' | 'dark' {
  if (currentMode === 'system') {
    return mediaQuery.matches ? 'dark' : 'light';
  }
  return currentMode;
}

export function getTheme(): ThemeMode {
  return currentMode;
}

export function setTheme(mode: ThemeMode): void {
  currentMode = mode;
  localStorage.setItem(STORAGE_KEY, mode);
  applyTheme();
  notifyListeners();
}

function applyTheme(): void {
  const effective = getEffectiveMode();
  const html = document.documentElement;
  html.classList.toggle('dark', effective === 'dark');
  html.style.colorScheme = effective;
}

function notifyListeners(): void {
  const effective = getEffectiveMode();
  for (const listener of listeners) {
    listener(currentMode, effective);
  }
}

mediaQuery.addEventListener('change', () => {
  if (currentMode === 'system') {
    applyTheme();
    notifyListeners();
  }
});

// Initialize on script load
applyTheme();

export function onThemeChange(listener: (mode: ThemeMode, effective: 'light' | 'dark') => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

export const themeManager = {
  getMode: getTheme,
  setMode: setTheme,
  getEffectiveMode,
  init: applyTheme,
  onChange: onThemeChange,
};
