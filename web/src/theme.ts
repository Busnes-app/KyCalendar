import { readChoice, saveChoice, watchChoice } from './ky-ui/theme';
import { SYSTEM_THEME, THEME_PAIRS, applyThemeVariables, findPair, findTheme } from './ky-ui/themes';
// Busnes.app defaults; explicit choices are local to this browser. The saved value is `system`
// (Busnes following the OS), a pair ID (that pair following the OS) or a half ID (a fixed mode).
// Aliases resolve to their half; removed IDs fall back to `system`.
export const THEME_CHOICES = THEME_PAIRS.map(pair => ({
  pair,
  choices: [
    { mode: 'system', label: 'Match system', value: pair.id === 'busnes-pair' ? SYSTEM_THEME.id : pair.id },
    { mode: 'light', label: 'Light', value: pair.light },
    { mode: 'dark', label: 'Dark', value: pair.dark },
  ],
}));
export function storedTheme(): string {
  const saved = readChoice('ky_theme');
  return findPair(saved)?.id ?? findTheme(saved)?.id ?? SYSTEM_THEME.id;
}
const media = window.matchMedia?.('(prefers-color-scheme: dark)');
export function applyTheme(theme: string, persist = false) {
  const option = findTheme(theme);
  if (!option) return;
  const resolved = option.id === 'system' ? (media?.matches ? 'busnes-dark' : 'busnes-light') : option.id;
  const scheme = applyThemeVariables(resolved) ?? (resolved === 'busnes-light' ? 'light' : 'dark');
  document.documentElement.dataset.theme = resolved;
  document.documentElement.dataset.scheme = scheme;
  document.documentElement.style.colorScheme = scheme;
  if (persist) saveChoice('ky_theme', findPair(theme)?.id ?? option.id);
}
applyTheme(storedTheme());
watchChoice('ky_theme', () => applyTheme(storedTheme()));
