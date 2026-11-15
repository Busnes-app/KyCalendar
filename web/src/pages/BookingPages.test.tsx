import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import BookingPages, { hoursText } from './BookingPages';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const json = (v: unknown, status = 200) => new Response(JSON.stringify(v), { status });
const cal = { id: 'cal1', name: 'Calendar', kind: 'personal', components: ['VEVENT'] };
const type = {
  id: 't1', enabled: true, url: 'https://cal.example/book/abc', title: 'Intro', duration_min: 30, zone: 'Europe/London',
  windows: [{ day: 1, start: '09:00', end: '17:00' }, { day: 2, start: '09:00', end: '17:00' }],
  buffer_before_min: 0, buffer_after_min: 0, notice_min: 240, horizon_days: 30, daily_cap: 0, calendar_id: 'cal1', meeting_room: false,
};

describe('hoursText', () => {
  it('groups days that share hours', () => {
    expect(hoursText(type.windows)).toBe('Mon, Tue 09:00–17:00');
  });
});

describe('BookingPages', () => {
  it('says why booking pages are off', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) =>
      String(input) === '/api/calendars' ? json([cal]) : json({ available: false, reason: 'kypost_unpaired', reason_text: 'this server is not paired with KyPost, which mails the invitations', types: [] }));
    render(<BookingPages />);
    expect((await screen.findByRole('note')).textContent).toMatch(/not paired with KyPost/);
  });

  it('lists a page with its link and creates one with the server defaults', async () => {
    const posted: unknown[] = [];
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      if (String(input) === '/api/calendars') return json([cal, { id: 'sub', name: 'Feed', kind: 'personal', subscription: {} }]);
      if (init?.method === 'POST') {
        posted.push(JSON.parse(String(init.body)));
        return json(type, 201);
      }
      return json({ available: true, reason: '', reason_text: '', types: [type] });
    });
    render(<BookingPages />);
    expect(await screen.findByText('https://cal.example/book/abc')).toBeTruthy();
    expect(screen.getByText('Taking bookings')).toBeTruthy();
    await waitFor(() => expect((screen.getByRole('button', { name: /New booking page/ }) as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(screen.getByRole('button', { name: /New booking page/ }));
    expect(screen.queryByRole('option', { name: 'Feed' })).toBeNull();
    fireEvent.change(screen.getByLabelText('Title'), { target: { value: 'Chat' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(posted).toHaveLength(1));
    expect(posted[0]).toMatchObject({ title: 'Chat', duration_min: 30, notice_min: 240, horizon_days: 30, calendar_id: 'cal1', enabled: true });
  });

  it('offers the meeting room only when video meetings are available', async () => {
    for (const meetings of [false, true]) {
      vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) =>
        String(input) === '/api/calendars' ? json([cal]) : json({ available: true, reason: '', reason_text: '', meetings, types: [type] }));
      render(<BookingPages />);
      fireEvent.click(await screen.findByRole('button', { name: 'Edit' }));
      expect(!!screen.queryByLabelText('Add a KyMessages meeting room')).toBe(meetings);
      cleanup();
      vi.restoreAllMocks();
    }
  });
});
