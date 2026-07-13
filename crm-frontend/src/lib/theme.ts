// Theme wiring (U2): the .dark token palette shipped in index.css was dead code
// — nothing ever toggled it. Preference is stored in localStorage ('light' |
// 'dark' | 'system'); 'system' follows prefers-color-scheme live.

export type ThemePreference = 'light' | 'dark' | 'system';

const STORAGE_KEY = 'theme';
const media = () => window.matchMedia('(prefers-color-scheme: dark)');

export function getThemePreference(): ThemePreference {
  const v = localStorage.getItem(STORAGE_KEY);
  return v === 'light' || v === 'dark' || v === 'system' ? v : 'system';
}

function resolve(pref: ThemePreference): 'light' | 'dark' {
  if (pref === 'system') return media().matches ? 'dark' : 'light';
  return pref;
}

function apply(pref: ThemePreference) {
  document.documentElement.classList.toggle('dark', resolve(pref) === 'dark');
}

export function setThemePreference(pref: ThemePreference) {
  localStorage.setItem(STORAGE_KEY, pref);
  apply(pref);
}

// initTheme applies the stored preference at boot and keeps 'system' live as
// the OS scheme changes. Call once from the app entry point.
export function initTheme() {
  apply(getThemePreference());
  media().addEventListener('change', () => {
    if (getThemePreference() === 'system') apply('system');
  });
}
