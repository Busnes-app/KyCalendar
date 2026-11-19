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

  it('shows a failed pairing in the KyRecovery panel and moves focus to it', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/api/backup/status')) return new Response(JSON.stringify({ ...PAIRED, paired: false, recovery_url: undefined }), { status: 200 });
      if (url.endsWith('/api/backup/pair-remote')) return new Response(JSON.stringify({ error: 'The pairing code has expired' }), { status: 400 });
      throw new Error(`unexpected fetch ${url}`);
    }));
    render(<Backup />);
    fireEvent.change(await screen.findByLabelText('Server URL'), { target: { value: 'https://recovery.example' } });
    fireEvent.change(screen.getByLabelText('Pairing code'), { target: { value: '123456' } });
    const button = screen.getByRole('button', { name: /^pair$/i });
    fireEvent.click(button);
    const alert = (await screen.findByText('The pairing code has expired')).closest('.dr-alert') as HTMLElement;
    expect(alert.closest('.panel')?.contains(button)).toBe(true);
    expect(document.activeElement).toBe(alert);
  });

  it('keeps all 7 actions focusable while busy and ignores a second press', async () => {
    vi.stubGlobal('confirm', () => true);
    const UNPINNED = { ...PAIRED, key_pinned: false, paired: false, recovery_url: undefined };
    for (const [body, actions] of [
      [PAIRED, ['Back up now', 'Run restore drill', 'Save', 'Re-pair', 'Unpair']],
      [UNPINNED, ['Pin key', 'Pair']],
    ] as const) {
      let calls = 0;
      vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
        if (String(input).endsWith('/api/backup/status')) return Promise.resolve(new Response(JSON.stringify(body), { status: 200 }));
        calls++;
        return new Promise<Response>(() => {}); // every action stays busy
      }));
      render(<Backup />);
      await screen.findByRole('button', { name: 'Refresh' });
      await vi.waitFor(() => expect(screen.getByRole('button', { name: 'Refresh' }).getAttribute('aria-disabled')).toBe('false'));
      fireEvent.change(screen.getByLabelText('Pairing code'), { target: { value: '123456' } });
      if (body === PAIRED) fireEvent.change(screen.getByLabelText('Back up automatically'), { target: { value: String(7 * 86400) } });
      else {
        fireEvent.change(screen.getByLabelText('Server URL'), { target: { value: 'https://recovery.example' } });
        fireEvent.change(screen.getByLabelText('Suite recovery public key'), { target: { value: 'key' } });
      }
      for (const name of actions) {
        const button = screen.getByRole('button', { name: new RegExp(`^${name}$`, 'i') });
        button.focus();
        fireEvent.click(button);
        await vi.waitFor(() => expect(button.getAttribute('aria-disabled'), name).toBe('true'));
        expect((button as HTMLButtonElement).disabled, name).toBe(false);
        expect(document.activeElement, name).toBe(button);
        const before = calls;
        fireEvent.click(button);
        expect(calls, `${name} pressed again while busy`).toBe(before);
      }
      cleanup();
    }
  });

  // Only an outcome an action just produced takes focus: a Refresh that moves an older result
  // (a failed read lifts it to the top, a good one puts it back) leaves focus on Refresh.
  it('keeps focus on Refresh while an older result is on screen', async () => {
    let read = 'ok';
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/api/backup/deposit')) return new Response(JSON.stringify({ manifest: { capsule_id: 'cap-1' }, size_bytes: 2048, local_path: '/backups/cap-1.kycap' }), { status: 200 });
      return read === 'ok' ? new Response(JSON.stringify(PAIRED), { status: 200 }) : new Response('{"error":"unavailable"}', { status: 503 });
    }));
    render(<Backup />);
    fireEvent.click(await screen.findByRole('button', { name: /back up now/i }));
    const result = (await screen.findByText(/Capsule cap-1/)).closest('.dr-alert');
    await vi.waitFor(() => expect(document.activeElement).toBe(result));
    const refresh = screen.getByRole('button', { name: 'Refresh' });
    for (const next of ['ok', 'fail', 'ok']) {
      read = next;
      refresh.focus();
      fireEvent.click(refresh);
      await vi.waitFor(() => expect(refresh.getAttribute('aria-disabled')).toBe('false'));
      expect(screen.getByText(/Capsule cap-1/)).toBeTruthy();
      expect(document.activeElement, `after a ${next} Refresh`).toBe(refresh);
    }
  });

  it('shows the drill, Save and Pin outcomes beside their action, focused', async () => {
    vi.stubGlobal('confirm', () => true);
    let body: Record<string, unknown> = PAIRED;
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/api/backup/status')) return new Response(JSON.stringify(body), { status: 200 });
      if (url.endsWith('/api/backup/drill')) return new Response(JSON.stringify({ passed: true, duration_ms: 12, checks: [{ name: 'Open', passed: true, message: 'opened' }] }), { status: 200 });
      if (url.endsWith('/api/backup/schedule')) {
        body = { ...PAIRED, interval_sec: JSON.parse(String(init?.body)).interval_sec };
        return new Response(init?.body, { status: 200 });
      }
      if (url.endsWith('/api/backup/pin-key')) return new Response(JSON.stringify({ error: 'Not a recovery key' }), { status: 400 });
      throw new Error(`unexpected fetch ${url}`);
    }));
    render(<Backup />);
    const drill = await screen.findByRole('button', { name: /run restore drill/i });
    fireEvent.click(drill);
    const drillOutcome = (await screen.findByText(/^Restore drill/)).closest('.dr-drill') as HTMLElement;
    expect(drillOutcome.closest('.panel')?.contains(drill)).toBe(true);
    await vi.waitFor(() => expect(document.activeElement).toBe(drillOutcome));

    fireEvent.change(screen.getByLabelText('Back up automatically'), { target: { value: String(7 * 86400) } });
    const save = screen.getByRole('button', { name: 'Save' });
    save.focus();
    fireEvent.click(save);
    const saved = (await screen.findByText('Backing up weekly.')).closest('.dr-alert') as HTMLElement;
    expect(saved.closest('.panel')?.contains(save)).toBe(true);
    await vi.waitFor(() => expect(document.activeElement).toBe(saved)); // never the page, though Save now disables

    cleanup();
    body = { ...PAIRED, key_pinned: false, paired: false };
    render(<Backup />);
    fireEvent.change(await screen.findByLabelText('Suite recovery public key'), { target: { value: 'key' } });
    const pin = screen.getByRole('button', { name: 'Pin key' });
    fireEvent.click(pin);
    const refused = (await screen.findByText('Not a recovery key')).closest('.dr-alert') as HTMLElement;
    expect(refused.closest('.panel')?.contains(pin)).toBe(true);
    await vi.waitFor(() => expect(document.activeElement).toBe(refused));
  });

  it('confirms a pinned key, focused, and keeps a saved schedule when the re-read fails', async () => {
    let body: Record<string, unknown> = { ...PAIRED, key_pinned: false, paired: false };
    let readable = true;
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/api/backup/status')) return readable ? new Response(JSON.stringify(body), { status: 200 }) : new Response('{"error":"unavailable"}', { status: 503 });
      if (url.endsWith('/api/backup/pin-key')) {
        body = PAIRED;
        return new Response('{}', { status: 200 });
      }
      if (url.endsWith('/api/backup/schedule')) {
        readable = false;
        return new Response(JSON.stringify({ interval_sec: 7 * 86400 }), { status: 200 });
      }
      throw new Error(`unexpected fetch ${url}`);
    }));
    render(<Backup />);
    fireEvent.change(await screen.findByLabelText('Suite recovery public key'), { target: { value: 'key' } });
    fireEvent.click(screen.getByRole('button', { name: 'Pin key' }));
    const pinned = (await screen.findByText('Key pinned.')).closest('.dr-alert') as HTMLElement;
    await vi.waitFor(() => expect(document.activeElement).toBe(pinned));

    fireEvent.change(screen.getByLabelText('Back up automatically'), { target: { value: String(7 * 86400) } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByText(/Could not load backup status/)).toBeTruthy();
    const saved = (await screen.findByText('Backing up weekly.')).closest('.dr-alert') as HTMLElement;
    await vi.waitFor(() => expect(document.activeElement).toBe(saved));
  });

  it('never shows a placeholder letter for the custodian count', async () => {
    mockStatus({ ...PAIRED, key_pinned: false, paired: false, threshold: undefined });
    render(<Backup />);
    expect(await screen.findByText(/that takes the custodian cards together/)).toBeTruthy();
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
