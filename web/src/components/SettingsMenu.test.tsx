import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { SettingsMenu } from './SettingsMenu';

afterEach(cleanup);

const gear = () => screen.getByRole('button', { name: 'Settings' });
const menu = () => document.getElementById('m') as HTMLElement;
const labels = () => [...menu().querySelectorAll('[role="menuitem"]')].map((a) => a.textContent);

describe('SettingsMenu', () => {
  it('lists Account, Phones & apps and Appearance for everyday users', () => {
    render(<SettingsMenu id="m" user={{ role: 'user' }} />);
    expect(labels()).toEqual(['Account', 'Phones & apps', 'Appearance']);
    expect([...menu().querySelectorAll('a')].map((a) => a.getAttribute('href'))).toEqual(['#/settings/account', '#/settings/devices', '#/settings/appearance']);
  });

  it('leaves Phones & apps out for administrators', () => {
    render(<SettingsMenu id="m" user={{ role: 'admin' }} />);
    expect(labels()).toEqual(['Account', 'Appearance']);
  });

  it('marks the open section and the gear current only while Settings is open', () => {
    const { rerender } = render(<SettingsMenu id="m" user={{ role: 'user' }} />);
    expect(gear().getAttribute('aria-current')).toBeNull();
    expect(menu().querySelector('[aria-current]')).toBeNull();
    rerender(<SettingsMenu id="m" user={{ role: 'user' }} section="devices" />);
    expect(gear().getAttribute('aria-current')).toBe('page');
    expect([...menu().querySelectorAll('[aria-current="page"]')].map((a) => a.textContent)).toEqual(['Phones & apps']);
  });

  it('opens on a click with focus on the first item, and Escape returns focus to the gear', () => {
    render(<SettingsMenu id="m" user={{ role: 'user' }} />);
    expect(gear().getAttribute('aria-haspopup')).toBe('menu');
    expect(gear().getAttribute('aria-controls')).toBe('m');
    expect(gear().getAttribute('aria-expanded')).toBe('false');
    expect(menu().hidden).toBe(true);
    fireEvent.click(gear());
    expect(gear().getAttribute('aria-expanded')).toBe('true');
    expect(menu().hidden).toBe(false);
    expect(document.activeElement?.textContent).toBe('Account');
    fireEvent.keyDown(document.activeElement!, { key: 'Escape' });
    expect(menu().hidden).toBe(true);
    expect(document.activeElement).toBe(gear());
  });

  it('moves with the arrows, Home and End, and wraps', () => {
    render(<SettingsMenu id="m" user={{ role: 'user' }} />);
    fireEvent.keyDown(gear(), { key: 'ArrowDown' });
    const items = within(menu()).getAllByRole('menuitem', { hidden: true });
    expect(document.activeElement).toBe(items[0]);
    fireEvent.keyDown(items[0], { key: 'ArrowDown' });
    expect(document.activeElement).toBe(items[1]);
    fireEvent.keyDown(items[1], { key: 'End' });
    expect(document.activeElement).toBe(items[2]);
    fireEvent.keyDown(items[2], { key: 'ArrowDown' });
    expect(document.activeElement).toBe(items[0]);
    fireEvent.keyDown(items[0], { key: 'ArrowUp' });
    expect(document.activeElement).toBe(items[2]);
    fireEvent.keyDown(items[2], { key: 'Home' });
    expect(document.activeElement).toBe(items[0]);
  });

  it('closes on Tab, on a click outside and when an item is chosen', () => {
    render(<><SettingsMenu id="m" user={{ role: 'user' }} /><p>outside</p></>);
    fireEvent.click(gear());
    fireEvent.keyDown(document.activeElement!, { key: 'Tab' });
    expect(menu().hidden).toBe(true);
    fireEvent.click(gear());
    fireEvent.pointerDown(screen.getByText('outside'));
    expect(menu().hidden).toBe(true);
    fireEvent.click(gear());
    fireEvent.click(within(menu()).getByRole('menuitem', { name: 'Appearance' }));
    expect(menu().hidden).toBe(true);
    expect(document.activeElement).toBe(gear());
  });
});
