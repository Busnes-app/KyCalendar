import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

vi.mock('@fullcalendar/react', () => ({ default: () => <div data-testid="fullcalendar" /> }));
const dashboard = vi.hoisted(() => ({ renders: 0 }));
vi.mock('./pages/Dashboard', () => ({ Dashboard: () => { dashboard.renders++; return <p>Overview</p>; } }));

import { App } from './App';

function signedInAs(role: string) {
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input).split('?')[0];
    if (path === '/api/auth/me') return new Response(JSON.stringify({ authenticated: true, user: { id: 'u1', username: 'u', role } }), { status: 200 });
    if (path === '/api/calendars') return new Response('[]', { status: 200 });
    return new Response('{}', { status: 200 });
  });
}

afterEach(() => {
  dashboard.renders = 0;
  cleanup();
  vi.restoreAllMocks();
});

describe('App landing', () => {
  it('lands everyday users on the calendar', async () => {
    signedInAs('user');
    render(<App />);
    expect(await screen.findByTestId('fullcalendar')).toBeTruthy();
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
});
