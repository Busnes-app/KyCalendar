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

  it('says the pinned key is unusable without contradicting itself', async () => {
    mockStatus({ ...PAIRED, key_pinned: false, paired: false, recovery_key_error: 'recovery.pub on this server names another key than the pin' });
    render(<Backup />);
    expect(await screen.findByText(/until this server can use the pinned key/)).toBeTruthy();
    expect(screen.queryByText(/Nothing can be sealed until a key is pinned/)).toBeNull();
    expect(screen.queryByText('No off-site copy')).toBeNull();
    expect(screen.getByText('https://recovery.example')).toBeTruthy();
  });

  it('says it is loading until the first status arrives', async () => {
    let answer: (r: Response) => void = () => {};
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>((resolve) => { answer = resolve; })));
    render(<Backup />);
    expect(screen.getByRole('status').textContent).toBe('Loading backup status…');
    answer(new Response(JSON.stringify(PAIRED), { status: 200 }));
    expect(await screen.findByText('https://recovery.example')).toBeTruthy();
    expect(screen.queryByText('Loading backup status…')).toBeNull();
  });

  it('keeps Refresh focusable while busy and ignores a second press', async () => {
    let answer: (r: Response) => void = () => {};
    let calls = 0;
    vi.stubGlobal('fetch', vi.fn(() => {
      calls++;
      if (calls === 1) return Promise.resolve(new Response(JSON.stringify(PAIRED), { status: 200 }));
      return new Promise<Response>((resolve) => { answer = resolve; });
    }));
    render(<Backup />);
    expect(await screen.findByText('https://recovery.example')).toBeTruthy();
    const refresh = screen.getByRole('button', { name: /refresh/i });
    refresh.focus();
    fireEvent.click(refresh);
    // disabled would drop focus to the page in real browsers; aria-disabled keeps it.
    expect((refresh as HTMLButtonElement).disabled).toBe(false);
    expect(refresh.getAttribute('aria-disabled')).toBe('true');
    expect(document.activeElement).toBe(refresh);
    fireEvent.click(refresh);
    expect(calls).toBe(2);
    answer(new Response(JSON.stringify(PAIRED), { status: 200 }));
    await vi.waitFor(() => expect(refresh.getAttribute('aria-disabled')).toBe('false'));
  });

  it('keeps an unpair result and its revoke advice when the re-read fails', async () => {
    vi.stubGlobal('confirm', () => true);
    let unpaired = false;
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/api/backup/status')) {
        return unpaired ? new Response('<html>Bad gateway</html>', { status: 502 }) : new Response(JSON.stringify(PAIRED), { status: 200 });
      }
      if (url.endsWith('/api/backup/pairing') && init?.method === 'DELETE') {
        unpaired = true;
        return new Response('{}', { status: 200 });
      }
      throw new Error(`unexpected fetch ${url}`);
    }));
    render(<Backup />);
    fireEvent.click(await screen.findByRole('button', { name: /unpair/i }));
    expect(await screen.findByText(/Could not load backup status/)).toBeTruthy();
    expect(screen.getByText(/ask the KyRecovery admin to revoke this service there/)).toBeTruthy();
  });

  it('keeps a sealed capsule result when the re-read fails', async () => {
    let ran = false;
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/api/backup/status')) {
        return ran ? new Response('{"error":"unavailable"}', { status: 503 }) : new Response(JSON.stringify(PAIRED), { status: 200 });
      }
      if (url.endsWith('/api/backup/deposit')) {
        ran = true;
        return new Response(JSON.stringify({ manifest: { capsule_id: 'cap-1' }, size_bytes: 2048, local_path: '/backups/cap-1.kycap' }), { status: 200 });
      }
      throw new Error(`unexpected fetch ${url}`);
    }));
    render(<Backup />);
    fireEvent.click(await screen.findByRole('button', { name: /back up now/i }));
    expect(await screen.findByText(/Could not load backup status/)).toBeTruthy();
    expect(screen.getByText(/Capsule cap-1 .* written to \/backups\/cap-1.kycap/)).toBeTruthy();
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
