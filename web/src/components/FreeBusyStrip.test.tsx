import { cleanup, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { FreeBusyStrip } from './FreeBusyStrip';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const at = (h: number) => new Date(2026, 10, 16, h).toISOString();

function serve(body: unknown, status = 200) {
  const urls: string[] = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    urls.push(String(input));
    return new Response(JSON.stringify(body), { status });
  });
  return urls;
}

describe('FreeBusyStrip', () => {
  it('asks once for every guest and marks who is busy at the chosen time', async () => {
    const urls = serve({ people: [
      { email: 'ann@example.com', busy: [{ start: at(9), end: at(10) }] },
      { email: 'bob@example.com', busy: [{ start: at(13), end: at(14), tentative: true }] },
      { email: 'out@elsewhere.example', unknown: true, busy: [] },
    ] });
    render(<FreeBusyStrip emails={['ann@example.com', 'bob@example.com', 'out@elsewhere.example']} start="2026-11-16T09:30" end="2026-11-16T10:30" allDay={false} />);
    await waitFor(() => expect(screen.getAllByText(/at this time/)).toHaveLength(2));
    expect(urls).toHaveLength(1);
    const u = new URL(urls[0], 'http://x');
    expect(u.pathname).toBe('/api/freebusy');
    expect(u.searchParams.get('users')).toBe('ann@example.com,bob@example.com,out@elsewhere.example');
    expect(new Date(u.searchParams.get('start')!).getTime()).toBe(new Date(2026, 10, 16).getTime());
    expect(new Date(u.searchParams.get('end')!).getTime()).toBe(new Date(2026, 10, 17).getTime());
    const rows = document.querySelectorAll('.kc-fb-row');
    expect(rows).toHaveLength(2); // nobody the server does not know
    expect(rows[0].getAttribute('data-conflict')).toBe('true');
    expect(rows[0].textContent).toMatch(/Busy at this time/);
    expect(rows[1].hasAttribute('data-conflict')).toBe(false);
    expect(rows[1].textContent).toMatch(/Free at this time.*\(maybe\)/);
  });

  it('shows nothing without guests or when the server refuses', async () => {
    const urls = serve({ error: 'x', code: 'scheduling_off' }, 422);
    render(<FreeBusyStrip emails={[]} start="2026-11-16T09:30" end="2026-11-16T10:30" allDay={false} />);
    expect(document.querySelector('.kc-freebusy')).toBeNull();
    cleanup();
    render(<FreeBusyStrip emails={['ann@example.com']} start="2026-11-16T09:30" end="2026-11-16T10:30" allDay={false} />);
    await waitFor(() => expect(urls).toHaveLength(1));
    expect(document.querySelector('.kc-freebusy')).toBeNull();
  });

  it('treats an all-day event as the whole day', async () => {
    serve({ people: [{ email: 'ann@example.com', busy: [{ start: at(15), end: at(16) }] }] });
    render(<FreeBusyStrip emails={['ann@example.com']} start="2026-11-16" end="2026-11-16" allDay />);
    await waitFor(() => expect(screen.getByText('Busy at this time')).toBeTruthy());
  });
});
