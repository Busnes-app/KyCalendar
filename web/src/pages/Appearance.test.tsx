import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

// theme.ts applies the saved choice on import, so each case imports it afresh.
async function page(saved: string | null) {
  vi.resetModules();
  localStorage.clear();
  if (saved !== null) localStorage.setItem('ky_theme', saved);
  const { Appearance } = await import('./Appearance');
  render(<Appearance />);
}
const root = document.documentElement;
const radio = (pair: string, mode: RegExp) =>
  within(screen.getByRole('group', { name: pair })).getByRole('radio', { name: mode }) as HTMLInputElement;

describe('Appearance', () => {
  beforeEach(() => { delete root.dataset.theme; root.removeAttribute('style'); });

  it('offers six pairs with Match system, Light and Dark, Busnes Match system checked by default', async () => {
    await page(null);
    expect(screen.getByRole('group', { name: 'Theme' })).toBeTruthy();
    for (const pair of ['Busnes', 'Matter', 'Ocean', 'Forest', 'Ky', 'Nord']) {
      expect(within(screen.getByRole('group', { name: pair })).getAllByRole('radio').map(r => r.closest('label')?.textContent)).toEqual(['Match system', 'Light', 'Dark']);
    }
    expect(screen.getAllByRole('radio')).toHaveLength(18);
    expect(screen.getAllByRole('radio').filter(r => (r as HTMLInputElement).checked)).toEqual([radio('Busnes', /^Match system/)]);
    expect(document.querySelector('.ky-appearance [style]')).toBeNull();
  });

  it('keeps an earlier saved half ID as a fixed mode and applies its palette', async () => {
    await page('nord');
    expect(radio('Nord', /^Dark/).checked).toBe(true);
    expect(root.dataset.theme).toBe('nord');
    expect(root.style.getPropertyValue('--line-strong')).toBe('#5e81ac');
    expect(localStorage.getItem('ky_theme')).toBe('nord');
  });

  it('saves a fixed half, then a pair following the OS, then clears colours for Busnes', async () => {
    await page(null);
    fireEvent.click(radio('Ocean', /^Dark/));
    expect(localStorage.getItem('ky_theme')).toBe('ocean');
    expect(root.dataset.theme).toBe('ocean');
    expect(root.style.getPropertyValue('--bg')).toBe('#0f1b24');
    fireEvent.click(radio('Ocean', /^Match system/));
    expect(localStorage.getItem('ky_theme')).toBe('ocean-pair');
    expect(root.dataset.theme).toBe('white-cliffs');
    expect(radio('Ocean', /^Match system/).checked).toBe(true);
    fireEvent.click(radio('Busnes', /^Light/));
    expect(localStorage.getItem('ky_theme')).toBe('busnes-light');
    expect(root.dataset.theme).toBe('busnes-light');
    expect(root.style.getPropertyValue('--bg')).toBe('');
    expect(root.style.colorScheme).toBe('light');
  });

  it('resolves aliases to their half: patina is Patina Ky, Paper Clean is Nord Light', async () => {
    await page('patina');
    expect(radio('Ky', /^Dark/).checked).toBe(true);
    expect(root.style.getPropertyValue('--ink-strong')).toBe('#e2e8f0');
    cleanup();
    await page('Paper Clean');
    expect(radio('Nord', /^Light/).checked).toBe(true);
    expect(root.style.getPropertyValue('--bg')).toBe('#eceff4');
  });

  it('resets a removed saved ID to Busnes following the OS', async () => {
    await page('sky');
    expect(radio('Busnes', /^Match system/).checked).toBe(true);
    expect(root.dataset.theme).toBe('busnes-light');
    expect(root.style.getPropertyValue('--bg')).toBe('');
  });

  it('re-resolves a saved pair when the OS scheme changes', async () => {
    const media = Object.assign(new EventTarget(), { matches: false });
    vi.stubGlobal('matchMedia', () => media);
    await page('forest-pair');
    expect(radio('Forest', /^Match system/).checked).toBe(true);
    expect(root.dataset.theme).toBe('forest-light');
    media.matches = true;
    media.dispatchEvent(new Event('change'));
    expect(root.dataset.theme).toBe('forest');
    expect(root.style.getPropertyValue('--bg')).toBe('#142018');
    expect(localStorage.getItem('ky_theme')).toBe('forest-pair');
  });
});
