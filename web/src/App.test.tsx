import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

vi.mock('@fullcalendar/react', () => ({ default: () => <div data-testid="fullcalendar" /> }));
const dashboard = vi.hoisted(() => ({ renders: 0 }));
vi.mock('./pages/Dashboard', () => ({ Dashboard: () => { dashboard.renders++; return <p>Overview</p>; } }));

import { App } from './App';

function signedInAs(role: string, extra = {}) {
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input).split('?')[0];
    if (path === '/api/auth/me') return new Response(JSON.stringify({ authenticated: true, user: { id: 'u1', username: 'u', role, ...extra } }), { status: 200 });
    if (path === '/api/calendars') return new Response('[]', { status: 200 });
    if (path === '/api/app-passwords') return new Response('[]', { status: 200 });
    if (path === '/api/admin/groups') return new Response('{"groups":[],"total":0}', { status: 200 });
    return new Response('{}', { status: 200 });
  });
}

afterEach(() => {
  dashboard.renders = 0;
  history.replaceState(null, '', '/');
  cleanup();
  vi.restoreAllMocks();
});

describe('App landing', () => {
  it('lands everyday users on the calendar', async () => {
    signedInAs('user');
    render(<App />);
    // The calendar page is a lazy chunk; its first import can pass 1s on a loaded machine.
    expect(await screen.findByTestId('fullcalendar', {}, { timeout: 5000 })).toBeTruthy();
    expect(screen.getByRole('button', { name: /Calendar/ })).toBeTruthy();
    expect(dashboard.renders).toBe(0); // no Overview flash before the calendar
  });

  it('shows admins no Calendar item and no calendar page', async () => {
    signedInAs('admin');
    render(<App />);
    await screen.findByRole('navigation', { name: 'Primary' });
    expect(screen.queryByRole('button', { name: /^Calendar$/ })).toBeNull();
    expect(screen.queryByTestId('fullcalendar')).toBeNull();
  });

  it('keeps admin pages from everyday users', async () => {
    signedInAs('user');
    render(<App />);
    const nav = await screen.findByRole('navigation', { name: 'Primary' });
    for (const label of ['Overview', 'People', 'Groups', 'Sign-in', 'Directory & SCIM', 'Backups', 'Settings']) {
      expect(within(nav).queryByRole('button', { name: label })).toBeNull();
    }
  });

  it('opens the Groups page for admins', async () => {
    signedInAs('admin');
    render(<App />);
    const nav = await screen.findByRole('navigation', { name: 'Primary' });
    fireEvent.click(within(nav).getByRole('button', { name: 'Groups' }));
    expect(await screen.findByRole('heading', { name: 'Groups' })).toBeTruthy();
  });

  it('shows the server version from /api/settings in the About window', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const path = String(input).split('?')[0];
      if (path === '/api/auth/me') return new Response(JSON.stringify({ authenticated: true, user: { id: 'u1', username: 'u', role: 'admin' } }), { status: 200 });
      if (path === '/api/settings') return new Response('{"version":"9.8.7"}', { status: 200 });
      return new Response('{}', { status: 200 });
    });
    render(<App />);
    // The sidebar's licence line; phones show a second one at the page end (CSS decides which).
    fireEvent.click((await screen.findAllByRole('button', { name: /MIT License/ }))[0]);
    expect((await screen.findAllByText('v9.8.7')).length).toBeGreaterThan(0);
  });
});

describe('Settings', () => {
  it('keeps personal pages out of the navigation and behind the footer gear', async () => {
    signedInAs('user', { sso_provider: 'local' });
    render(<App />);
    const nav = await screen.findByRole('navigation', { name: 'Primary' });
    expect(within(nav).getAllByRole('button').map((b) => b.textContent)).toEqual(['Calendar']);
    expect(screen.queryByText('Sign out')).toBeNull();
    const gear = screen.getAllByRole('button', { name: 'Settings' })[0];
    fireEvent.click(gear);
    fireEvent.click(within(screen.getByRole('menu', { name: 'Settings' })).getByRole('menuitem', { name: 'Account' }));
    window.dispatchEvent(new HashChangeEvent('hashchange'));
    expect(await screen.findByRole('heading', { level: 1, name: 'Settings' })).toBeTruthy();
    const tabs = screen.getByRole('navigation', { name: 'Settings sections' });
    expect(within(tabs).getAllByRole('link').map((a) => a.textContent)).toEqual(['Account', 'Phones & apps', 'Appearance']);
    expect(within(tabs).getByRole('link', { name: 'Account' }).getAttribute('aria-current')).toBe('page');
    expect(gear.getAttribute('aria-current')).toBe('page');
    expect(within(nav).getByRole('button', { name: 'Calendar' }).getAttribute('aria-current')).toBeNull();
    expect(screen.getByRole('heading', { name: 'Two-step verification is off' })).toBeTruthy();
  });

  it('sends the old page addresses to their Settings section', async () => {
    for (const [old, section] of [['devices', 'Phones & apps'], ['appearance', 'Appearance'], ['security', 'Account']]) {
      history.replaceState(null, '', `/#/${old}`);
      signedInAs('user');
      render(<App />);
      const tabs = await screen.findByRole('navigation', { name: 'Settings sections' });
      expect(within(tabs).getByRole('link', { name: section }).getAttribute('aria-current')).toBe('page');
      expect(location.hash).toBe(within(tabs).getByRole('link', { name: section }).getAttribute('href'));
      cleanup();
      vi.restoreAllMocks();
    }
  });

  it('gives administrators Account and Appearance, and Server settings in the navigation', async () => {
    history.replaceState(null, '', '/#/settings/devices');
    signedInAs('admin');
    render(<App />);
    const tabs = await screen.findByRole('navigation', { name: 'Settings sections' });
    expect(within(tabs).getAllByRole('link').map((a) => a.textContent)).toEqual(['Account', 'Appearance']);
    expect(within(tabs).getByRole('link', { name: 'Account' }).getAttribute('aria-current')).toBe('page');
    const nav = screen.getByRole('navigation', { name: 'Primary' });
    expect(within(nav).queryByRole('button', { name: 'Settings' })).toBeNull();
    fireEvent.click(within(nav).getByRole('button', { name: 'Server settings' }));
    expect(await screen.findByRole('heading', { level: 1, name: 'Server settings' })).toBeTruthy();
  });
});
