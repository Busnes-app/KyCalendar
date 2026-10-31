import { cleanup, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

vi.mock('@fullcalendar/react', () => ({ default: () => <div data-testid="fullcalendar" /> }));

import { App } from '../App';
import { AppRail, loadSuiteApps } from './AppRail';

const apps = [
  { id: 'kypost', name: 'KyPost', url: 'https://mail.example.com/api/auth/oidc/login' },
  { id: 'kycalendar', name: 'KyCalendar', url: 'https://cal.example.com/api/sso/kysignon/login' },
  { id: 'kysomething', name: 'Unknown app', url: 'https://x.example.com/' },
];

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe('AppRail', () => {
  it('links every app in order, exactly as given, and marks KyCalendar current', () => {
    render(<AppRail apps={apps} />);
    const nav = screen.getByRole('navigation', { name: 'Apps' });
    const links = within(nav).getAllByRole('link');
    expect(links.map((a) => a.getAttribute('aria-label'))).toEqual(['KyPost', 'KyCalendar', 'Unknown app']);
    expect(links.map((a) => a.getAttribute('href'))).toEqual(apps.map((a) => a.url));
    expect(links.map((a) => a.getAttribute('aria-current'))).toEqual([null, 'page', null]);
    expect(links.map((a) => a.getAttribute('target'))).toEqual([null, null, null]);
    for (const link of links) {
      expect(link.querySelector('img')?.getAttribute('src')).toMatch(/^data:image\//);
    }
  });

  it('renders nothing for an empty list', () => {
    const { container } = render(<AppRail apps={[]} />);
    expect(container.innerHTML).toBe('');
  });

  it('loads an empty list on failure or a malformed answer', async () => {
    const fetch = vi.spyOn(globalThis, 'fetch');
    fetch.mockResolvedValueOnce(new Response('{}', { status: 500 }));
    expect(await loadSuiteApps()).toEqual([]);
    fetch.mockResolvedValueOnce(new Response('{"apps":"no"}', { status: 200 }));
    expect(await loadSuiteApps()).toEqual([]);
    fetch.mockRejectedValueOnce(new TypeError('offline'));
    expect(await loadSuiteApps()).toEqual([]);
  });
});

function serve(suite: unknown) {
  return vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input).split('?')[0];
    if (path === '/api/auth/me') return new Response(JSON.stringify({ authenticated: true, user: { id: 'u1', username: 'u', role: 'admin' } }), { status: 200 });
    if (path === '/api/suite/apps') return new Response(JSON.stringify(suite), { status: 200 });
    if (path === '/api/admin/groups') return new Response('{"groups":[],"total":0}', { status: 200 });
    return new Response('{}', { status: 200 });
  });
}

describe('App rail in the shell', () => {
  it('fetches once and renders the rail in the sidebar foot and the content foot', async () => {
    const fetch = serve({ apps });
    const { container } = render(<App />);
    expect(await screen.findAllByRole('navigation', { name: 'Apps' })).toHaveLength(2);
    expect(container.querySelector('.ky-sidebar-foot > nav.ky-app-rail:first-child')).toBeTruthy();
    expect(container.querySelector('.ky-content-foot > nav.ky-app-rail:first-child')).toBeTruthy();
    expect(fetch.mock.calls.filter(([input]) => String(input) === '/api/suite/apps')).toHaveLength(1);
  });

  it('leaves the rail out when the list is empty', async () => {
    serve({ apps: [] });
    render(<App />);
    await screen.findByRole('navigation', { name: 'Primary' });
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(screen.queryByRole('navigation', { name: 'Apps' })).toBeNull();
  });
});
