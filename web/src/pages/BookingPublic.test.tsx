import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { BookingPublic, byDay, linkFromHash } from './BookingPublic';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  history.replaceState(null, '', '/');
});

type Handler = (init?: RequestInit) => Response;
function mockFetch(handlers: Record<string, Handler>) {
  const calls: { key: string; body: unknown }[] = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const key = `${init?.method ?? 'GET'} ${String(input)}`;
    calls.push({ key, body: init?.body ? JSON.parse(String(init.body)) : undefined });
    const h = handlers[key];
    if (!h) throw new Error(`unexpected ${key}`);
    return h(init);
  });
  return calls;
}
const json = (v: unknown, status = 200) => new Response(JSON.stringify(v), { status });

const id = '11111111-2222-3333-4444-555555555555';
const token = 'a'.repeat(64);

describe('linkFromHash', () => {
  it('reads only a well-formed id.token fragment', () => {
    expect(linkFromHash(`#${id}.${token}`)).toEqual({ id, token });
    for (const bad of ['', '#', `#${id}`, `#${id}.short`, `#${id}.${token}x`, `#../${token}`]) expect(linkFromHash(bad)).toBeNull();
  });
});

describe('byDay', () => {
  it('groups slots by the visitor day in order', () => {
    const days = byDay([
      { start: '2026-11-17T09:00:00Z', end: '2026-11-17T10:00:00Z' },
      { start: '2026-11-17T10:00:00Z', end: '2026-11-17T11:00:00Z' },
      { start: '2026-11-18T09:00:00Z', end: '2026-11-18T10:00:00Z' },
    ]);
    expect(days.map(([, s]) => s.length)).toEqual([2, 1]);
  });
});

describe('BookingPublic', () => {
  const slug = '0123456789abcdef0123456789abcdef';
  const page = { host: 'Sam', title: 'Intro <b>call</b>', duration_min: 30, slots: [{ start: '2026-11-17T09:00:00Z', end: '2026-11-17T09:30:00Z' }] };

  it('shows only the host, title and free times, then holds a time and asks to check mail', async () => {
    const calls = mockFetch({
      [`GET /api/book/${slug}`]: () => json(page),
      'GET /api/auth/me': () => json({ authenticated: false }),
      [`POST /api/book/${slug}/hold`]: () => json({ expires_at: '2026-11-15T12:15:00Z' }, 202),
    });
    render(<BookingPublic path={`/book/${slug}`} />);
    // Text, never markup: the title renders as typed.
    expect(await screen.findByRole('heading', { level: 1, name: 'Intro <b>call</b>' })).toBeTruthy();
    expect(screen.getByText(/with Sam/)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: new Date(page.slots[0].start).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' }) }));
    fireEvent.change(screen.getByLabelText('Your name'), { target: { value: 'Carol' } });
    fireEvent.change(screen.getByLabelText('Your email'), { target: { value: 'carol@example.net' } });
    fireEvent.click(screen.getByRole('button', { name: 'Hold this time' }));
    expect(await screen.findByRole('heading', { name: 'Check your email' })).toBeTruthy();
    const hold = calls.find((c) => c.key.startsWith('POST'));
    expect(hold?.body).toEqual({ start: page.slots[0].start, name: 'Carol', email: 'carol@example.net', note: '' });
  });

  it('says the page is gone on a 404, without detail', async () => {
    mockFetch({ [`GET /api/book/${slug}`]: () => json({ error: 'x', code: 'not_found' }, 404) });
    render(<BookingPublic path={`/book/${slug}`} />);
    expect((await screen.findByRole('alert')).textContent).toMatch(/does not exist or is turned off/);
  });

  it('confirms only when the visitor presses Confirm', async () => {
    history.replaceState(null, '', `/book/c#${id}.${token}`);
    const held = { status: 'held', host: 'Sam', title: 'Intro', start: '2026-11-17T09:00:00Z', end: '2026-11-17T09:30:00Z' };
    const confirm = vi.fn(() => json({ ...held, status: 'confirmed' }));
    mockFetch({ 'GET /api/auth/me': () => json({}), 'POST /api/book/confirm/view': () => json(held), 'POST /api/book/confirm': confirm });
    render(<BookingPublic path="/book/c" />);
    const button = await screen.findByRole('button', { name: 'Confirm booking' });
    expect(confirm).not.toHaveBeenCalled();
    fireEvent.click(button);
    expect((await screen.findByRole('status')).textContent).toMatch(/Booked/);
  });

  it('refuses a link without its token', async () => {
    history.replaceState(null, '', `/book/m#${id}`);
    const calls = mockFetch({});
    render(<BookingPublic path="/book/m" />);
    expect((await screen.findByRole('alert')).textContent).toMatch(/incomplete/);
    await waitFor(() => expect(calls).toHaveLength(0));
  });
});
