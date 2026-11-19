import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { Backup } from './Backup';

function mockStatus(body: Record<string, unknown>) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (!url.endsWith('/api/backup/status')) throw new Error(`unexpected fetch ${url}`);
      return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } });
    }),
  );
}

// PAIRED is a healthy SQLite instance: the driver decides whether the screen warns that no
// capsule can carry the database.
const PAIRED = {
  key_pinned: true,
  paired: true,
  interval_sec: 86400,
  recovery_url: 'https://recovery.example',
  recovery_key_id: 'k1',
  threshold: 2,
  total_shares: 3,
  database_driver: 'sqlite',
  members: ['data/kycalendar.db'],
};

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('Backup', () => {
  it('says what a schedule waits for instead of a next run it cannot keep', async () => {
    const next = '2026-11-01T03:00:00Z';
    mockStatus({ ...PAIRED, key_pinned: false, paired: false, next_run_at: next });
    render(<Backup />);
    expect(await screen.findByText('Waiting for recovery key')).toBeTruthy();
    expect(screen.queryByText(/^Next /)).toBeNull();
    cleanup();
    mockStatus({ ...PAIRED, next_run_at: next });
    render(<Backup />);
    expect(await screen.findByText(/^Next /)).toBeTruthy();
  });

  it('warns when a key is pinned but there is no destination', async () => {
    mockStatus({ key_pinned: true, paired: false, interval_sec: 0, recovery_key_id: 'k1', threshold: 2, total_shares: 3, database_driver: 'sqlite', members: [] });
    render(<Backup />);
    expect(await screen.findByText(/nowhere to go/i)).toBeTruthy();
    expect(screen.getByText(/automatic backups are off/i)).toBeTruthy();
    expect(screen.getByText(/KYCALENDAR_BACKUP_DIR not set/)).toBeTruthy();
  });

  it('never renders the token', async () => {
    mockStatus({
      key_pinned: true,
      paired: true,
      interval_sec: 86400,
      recovery_url: 'https://recovery.example',
      recovery_key_id: 'k1',
      threshold: 2,
      total_shares: 3,
      database_driver: 'sqlite',
      members: ['data/kycalendar.db'],
    });
    const { container } = render(<Backup />);
    expect(await screen.findByText('https://recovery.example')).toBeTruthy();
    expect(container.textContent).not.toMatch(/token/i);
    expect(screen.getByText('data/kycalendar.db')).toBeTruthy();
    expect(screen.getByRole('button', { name: /unpair/i })).toBeTruthy();
  });

  it('says how a PostgreSQL capsule restores', async () => {
    mockStatus({ ...PAIRED, database_driver: 'postgres' });
    render(<Backup />);
    expect(await screen.findByText(/carries a verified SQLite snapshot/)).toBeTruthy();
  });

  it('does not show the PostgreSQL note on SQLite', async () => {
    mockStatus(PAIRED);
    render(<Backup />);
    await screen.findByText('https://recovery.example');
    expect(screen.queryByText(/carries a verified SQLite snapshot/)).toBeNull();
  });

  it('shows an unreadable status as unreadable, not as unpaired', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('{"error":"KyCalendar is temporarily unavailable; try again","code":"unavailable"}', { status: 503, headers: { 'Content-Type': 'application/json' } })));
    render(<Backup />);
    expect(await screen.findByText(/Could not load backup status: KyCalendar is temporarily unavailable/)).toBeTruthy();
    for (const text of ['Not paired', 'None', 'Off', /No backups are being made/]) {
      expect(screen.queryByText(text)).toBeNull();
    }
    expect(screen.queryByRole('button', { name: /pin/i })).toBeNull();
    expect(screen.queryByRole('button', { name: /^pair$/i })).toBeNull();
  });

  it('clears the facts when a refresh cannot read the status', async () => {
    let ok = true;
    vi.stubGlobal('fetch', vi.fn(async () => ok
      ? new Response(JSON.stringify(PAIRED), { status: 200, headers: { 'Content-Type': 'application/json' } })
      : new Response('<html>Bad gateway</html>', { status: 502, headers: { 'Content-Type': 'text/html' } })));
    render(<Backup />);
    expect(await screen.findByText('https://recovery.example')).toBeTruthy();
    ok = false;
    fireEvent.click(screen.getByRole('button', { name: /refresh/i }));
    const alert = await screen.findByText(/Could not load backup status/);
    expect(alert.textContent?.match(/Could not load backup status/g)?.length).toBe(1);
    expect(screen.queryByText('https://recovery.example')).toBeNull();
    expect(screen.queryByText('Paired')).toBeNull();
  });

  it('does not call an unusable key unpaired or ask for a first pin', async () => {
    mockStatus({ ...PAIRED, key_pinned: false, paired: false, recovery_key_error: 'recovery.pub on this server names another key than the pin' });
    render(<Backup />);
    expect(await screen.findByText(/names another key/)).toBeTruthy();
    expect(screen.queryByText('Not paired')).toBeNull();
    expect(screen.queryByText(/No backups are being made/)).toBeNull();
    expect(screen.getAllByText('Unusable').length).toBeGreaterThan(0);
  });

  it('shows a failed last run, which the schedule alone would hide', async () => {
    mockStatus({ ...PAIRED, last_run: { at: '2026-10-06T10:00:00Z', outcome: 'failure', error: 'capsule too large' } });
    render(<Backup />);
    expect(await screen.findByText(/Last run failed/)).toBeTruthy();
    expect(screen.getByText(/capsule too large/)).toBeTruthy();
  });

  it('shows a successful last run', async () => {
    mockStatus({ ...PAIRED, last_run: { at: '2026-10-06T10:00:00Z', outcome: 'success' } });
    render(<Backup />);
    expect(await screen.findByText(/Last run succeeded/)).toBeTruthy();
  });

  it('asks for a fresh sign-in when unpair is refused, and stays paired', async () => {
    vi.stubGlobal('confirm', () => true);
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url.endsWith('/api/backup/status')) return new Response(JSON.stringify(PAIRED), { status: 200 });
        if (url.endsWith('/api/backup/pairing') && init?.method === 'DELETE') {
          return new Response(JSON.stringify({ error: 'Sign in again to unpair', code: 'reauth_required' }), { status: 403 });
        }
        throw new Error(`unexpected fetch ${url}`);
      }),
    );
    render(<Backup />);
    fireEvent.click(await screen.findByRole('button', { name: /unpair/i }));
    expect(await screen.findByText('Sign in again to unpair')).toBeTruthy();
    expect(screen.queryByText(/^Unpaired\./)).toBeNull();
    expect(screen.getByText('https://recovery.example')).toBeTruthy();
  });
});
