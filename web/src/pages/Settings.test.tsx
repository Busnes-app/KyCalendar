import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { Settings } from './Settings';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });
const ROUTE = '/api/settings/rate-limit-networks';

describe('Settings', () => {
  it('shows the version the server reports', () => {
    render(<Settings settings={{ db_driver: 'sqlite', version: '0.5.0' }} />);
    expect(screen.getByText('Version').nextElementSibling?.textContent).toBe('0.5.0');
  });

  it('shows the stored networks one per line and saves an edited list', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      expect(String(input)).toBe(ROUTE);
      if (init?.method === 'PUT') {
        expect(JSON.parse(String(init.body))).toEqual({ networks: '203.0.113.0/24,198.51.100.7' });
        return json({ networks: '203.0.113.0/24,198.51.100.7/32', environment: '192.0.2.0/24', exempted: 0 });
      }
      return json({ networks: '203.0.113.0/24', environment: '192.0.2.0/24', exempted: 41 });
    });
    const { container } = render(<Settings settings={{}} />);
    const box = (await screen.findByDisplayValue('203.0.113.0/24')) as HTMLTextAreaElement;
    expect(container.textContent).toMatch(/41 since/);
    expect(container.textContent).toMatch(/192\.0\.2\.0\/24/);
    expect(container.textContent).toMatch(/never add guest Wi-Fi, a VPN provider or your reverse proxy/);

    fireEvent.change(box, { target: { value: '203.0.113.0/24\n 198.51.100.7 \n' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save networks' }));
    expect((await screen.findByRole('status')).textContent).toMatch(/Saved/);
    expect(box.value).toBe('203.0.113.0/24\n198.51.100.7/32');
  });

  it('shows why the server refused a list and keeps what was typed', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (_input, init) =>
      init?.method === 'PUT'
        ? json({ error: '"10.0.0.0/8" is too broad; the widest IPv4 range allowed is /16' }, 400)
        : json({ networks: '', environment: '', exempted: 0 }),
    );
    const { container } = render(<Settings settings={{}} />);
    const box = (await screen.findByPlaceholderText('203.0.113.0/24')) as HTMLTextAreaElement;
    await screen.findByText(/0 since/);
    fireEvent.change(box, { target: { value: '10.0.0.0/8' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save networks' }));
    expect((await screen.findByRole('alert')).textContent).toBe('"10.0.0.0/8" is too broad; the widest IPv4 range allowed is /16');
    expect(box.value).toBe('10.0.0.0/8');
    expect(container.textContent).not.toMatch(/Also set by the server environment/);
  });

  it('asks for a fresh sign-in when the session is too old to save', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (_input, init) =>
      init?.method === 'PUT'
        ? json({ error: 'Sign in again to change office networks', code: 'reauth_required' }, 403)
        : json({ networks: '', environment: '', exempted: 0 }),
    );
    render(<Settings settings={{}} />);
    await screen.findByText(/0 since/);
    fireEvent.click(screen.getByRole('button', { name: 'Save networks' }));
    expect((await screen.findByRole('alert')).textContent).toMatch(/sign in again/i);
  });
});
