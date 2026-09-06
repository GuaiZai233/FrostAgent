export const DEFAULT_UI_SCALE = '1.1';
export const UI_SCALE_STORAGE_KEY = 'user-ui-scale';

export interface ScaleOption {
  value: string;
  label: string;
  desc: string;
  isDefault?: boolean;
}

export const scaleOptions: ScaleOption[] = [
  { value: '1.0', label: '100%', desc: '标准原始比例' },
  { value: '1.05', label: '105%', desc: '稍微放大' },
  { value: '1.1', label: '110%', desc: '默认推荐，界面与字号更舒适', isDefault: true },
  { value: '1.15', label: '115%', desc: '更大视觉字号' },
  { value: '1.2', label: '120%', desc: '高分屏或远距离观看' },
];

const listeners = new Set<(scale: string) => void>();

function loadPreference(): string {
  try {
    const stored = localStorage.getItem(UI_SCALE_STORAGE_KEY);
    if (stored && ['0.9', '0.95', '1.0', '1.05', '1.1', '1.15', '1.2', '1.25'].includes(stored)) {
      return stored;
    }
  } catch {
    // localStorage might fail in private/restricted environments
  }
  return DEFAULT_UI_SCALE;
}

let currentScale: string = loadPreference();

export function getScale(): string {
  return currentScale;
}

export function setScale(scale: string): void {
  currentScale = scale;
  try {
    localStorage.setItem(UI_SCALE_STORAGE_KEY, scale);
  } catch {
    // ignore
  }
  applyScale();
  notifyListeners();
}

export function resetScale(): void {
  setScale(DEFAULT_UI_SCALE);
}

function applyScale(): void {
  const html = document.documentElement;
  html.style.zoom = currentScale;
  html.style.setProperty('--ui-scale', currentScale);
}

function notifyListeners(): void {
  for (const listener of listeners) {
    listener(currentScale);
  }
}

// Initialize on script load
applyScale();

export function onScaleChange(listener: (scale: string) => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

export const scaleManager = {
  getScale,
  setScale,
  resetScale,
  init: applyScale,
  onChange: onScaleChange,
  DEFAULT_SCALE: DEFAULT_UI_SCALE,
  options: scaleOptions,
};
