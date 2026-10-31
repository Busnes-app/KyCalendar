import { useEffect, useState } from 'react';
import { THEME_CHOICES, applyTheme, storedTheme } from '../theme';

export function ThemeSwitcher() {
  const [theme, setTheme] = useState(storedTheme);
  useEffect(() => {
    const sync = () => setTheme(storedTheme());
    window.addEventListener('storage', sync);
    return () => window.removeEventListener('storage', sync);
  }, []);
  return <select className="theme-selector" aria-label="Color theme" value={theme} onChange={event => {
    setTheme(event.target.value);
    applyTheme(event.target.value, true);
  }}>{THEME_CHOICES.map(({ pair, choices }) => (
    <optgroup key={pair.id} label={pair.name}>
      {choices.map(({ mode, label, value }) => <option key={mode} value={value}>{pair.name} · {label}</option>)}
    </optgroup>
  ))}</select>;
}
