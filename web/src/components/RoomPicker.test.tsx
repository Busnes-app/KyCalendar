import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { EventDialog } from './EventDialog';
import { emptyForm } from '../eventForm';
import type { CalendarInfo, EventInfo } from '../calendarApi';
import RoomRequests from '../pages/RoomRequests';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const cals: CalendarInfo[] = [{ id: 'cal_p', name: 'Mine', color: '', description: '', kind: 'personal', role: 'owner', dav_path: '', components: ['VEVENT'] }];
const rooms = [
  { id: 'r1', kind: 'room', name: 'Fjord', location: 'Floor 2', capacity: 8, tags: ['screen'], approval: 'auto', address: 'a', free: true },
  { id: 'r2', kind: 'room', name: 'Harbour', location: 'Floor 4', capacity: 20, tags: ['screen', 'video'], approval: 'approvers', address: 'b', free: true },
  { id: 'r3', kind: 'room', name: 'Busy one', location: '', capacity: 30, tags: [], approval: 'auto', address: 'c', free: false },
];

function capture() {
  const calls: { method: string; url: string; body?: unknown }[] = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const url = String(input);
    calls.push({ method: init?.method ?? 'GET', url, body: init?.body ? JSON.parse(String(init.body)) : undefined });
    if (url.startsWith('/api/resources?')) return new Response(JSON.stringify({ resources: rooms }), { status: 200 });
    return new Response(JSON.stringify({ etag: 'e2', uid: 'new' }), { status: init?.method === 'POST' ? 201 : 200 });
  });
  return calls;
}

describe('RoomPicker', () => {
  it('finds a free room for the time, filters by people and needs, and books it', async () => {
    const calls = capture();
    const onDone = vi.fn();
    render(<EventDialog calendars={cals} initial={emptyForm(new Date('2026-12-07T09:00:00Z'), new Date('2026-12-07T10:00:00Z'), false, 'cal_p')} onDone={onDone} onClose={vi.fn()} />);
    fireEvent.change(screen.getByLabelText('Title'), { target: { value: 'Review' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add room' }));
    await screen.findByRole('button', { name: 'Add Fjord' });
    const finder = calls.find((c) => c.url.startsWith('/api/resources?'))!;
    expect(new URL(finder.url, 'http://x').searchParams.get('start')).toBe('2026-12-07T09:00:00.000Z');
    expect(screen.queryByText('Busy one')).toBeNull();
    expect(screen.getByText('1 more is busy then.')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Request Harbour' }).previousElementSibling!.querySelector('.kc-hint')!.firstElementChild!.textContent).toBe('Needs approval');
    fireEvent.change(screen.getByLabelText('People'), { target: { value: '10' } });
    expect(screen.queryByRole('button', { name: 'Add Fjord' })).toBeNull();
    fireEvent.change(screen.getByLabelText('People'), { target: { value: '500' } });
    expect(screen.getByText('Nothing matches these filters.')).toBeTruthy();
    fireEvent.change(screen.getByLabelText('People'), { target: { value: '' } });
    fireEvent.click(screen.getByLabelText(/video/));
    expect(screen.queryByRole('button', { name: 'Add Fjord' })).toBeNull();
    fireEvent.click(screen.getByLabelText(/video/));
    fireEvent.click(screen.getByRole('button', { name: 'Add Fjord' }));
    expect((screen.getByLabelText(/^Location/) as HTMLInputElement).value).toBe('Fjord, Floor 2');
    expect(screen.getByText('Checked when you save')).toBeTruthy();
    // Location already names the room's place, so the room line does not repeat it.
    expect(screen.getByText('Fjord', { selector: '.kc-room-name' })).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Add room' }));
    fireEvent.click(screen.getByRole('button', { name: 'Close room finder' }));
    expect(screen.queryByRole('group', { name: 'Find a room' })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(onDone).toHaveBeenCalled());
    const post = calls.find((c) => c.method === 'POST')!;
    expect(post.body).toMatchObject({ resources: ['r1'], location: 'Fjord, Floor 2' });
  });

  it('says why a room was declined, and keeps rooms out of an unchanged save', async () => {
    const calls = capture();
    const ev: EventInfo = {
      calendar_id: 'cal_p', uid: 'u1', etag: 'e1', title: 'Review', start: '2026-12-07T09:00:00Z', end: '2026-12-07T10:00:00Z',
      series_start: '2026-12-07T09:00:00Z', series_end: '2026-12-07T10:00:00Z', all_day: false, recurring: false, repeat: { freq: '' }, editable: true,
      resources: [{ id: 'r1', name: 'Fjord', kind: 'room', location: 'Floor 2', status: 'declined', reason: 'busy', detail: '2026-12-07' }],
    };
    const onDone = vi.fn();
    render(<EventDialog calendars={cals} event={ev} initial={emptyForm(new Date(), new Date(), false, 'cal_p')} onDone={onDone} onClose={vi.fn()} />);
    // A single event's conflict is its own date: the answer leads with Declined in weight and names no date.
    expect(screen.getByText('Declined').tagName).toBe('STRONG');
    expect(screen.getByText('Declined').parentElement!.textContent).toBe('Declined · busy then');
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(onDone).toHaveBeenCalled());
    expect(calls.find((c) => c.method === 'PUT')!.body).not.toHaveProperty('resources');
  });

  it('flags a Location filled from a declined room, and clears it on request', () => {
    capture();
    const ev: EventInfo = {
      calendar_id: 'cal_p', uid: 'u1', etag: 'e1', title: 'Review', location: 'Fjord, Floor 2', start: '2026-12-07T09:00:00Z', end: '2026-12-07T10:00:00Z',
      series_start: '2026-12-07T09:00:00Z', series_end: '2026-12-07T10:00:00Z', all_day: false, recurring: false, repeat: { freq: '' }, editable: true,
      resources: [{ id: 'r1', name: 'Fjord', kind: 'room', location: 'Floor 2', status: 'declined', reason: 'busy' }],
    };
    const form = { ...emptyForm(new Date('2026-12-07T09:00:00Z'), new Date('2026-12-07T10:00:00Z'), false, 'cal_p'), location: 'Fjord, Floor 2' };
    render(<EventDialog calendars={cals} event={ev} initial={form} onDone={vi.fn()} onClose={vi.fn()} />);
    expect(screen.getByRole('note').textContent).toMatch(/^Location names Fjord, which is declined\./);
    fireEvent.click(screen.getByRole('button', { name: 'Clear location' }));
    expect((screen.getByLabelText(/^Location/) as HTMLInputElement).value).toBe('');
    expect(screen.queryByRole('note')).toBeNull();
  });

  it('keeps a Location the user typed even when the room is declined', () => {
    capture();
    const ev: EventInfo = {
      calendar_id: 'cal_p', uid: 'u1', etag: 'e1', title: 'Review', location: 'Café downstairs', start: '2026-12-07T09:00:00Z', end: '2026-12-07T10:00:00Z',
      series_start: '2026-12-07T09:00:00Z', series_end: '2026-12-07T10:00:00Z', all_day: false, recurring: false, repeat: { freq: '' }, editable: true,
      resources: [{ id: 'r1', name: 'Fjord', kind: 'room', location: 'Floor 2', status: 'declined', reason: 'busy' }],
    };
    const form = { ...emptyForm(new Date('2026-12-07T09:00:00Z'), new Date('2026-12-07T10:00:00Z'), false, 'cal_p'), location: 'Café downstairs' };
    render(<EventDialog calendars={cals} event={ev} initial={form} onDone={vi.fn()} onClose={vi.fn()} />);
    expect(screen.queryByText(/which is declined/)).toBeNull();
  });

  it('shows the rooms of an event the user cannot edit, read only', () => {
    capture();
    const ev: EventInfo = {
      calendar_id: 'cal_p', uid: 'u1', etag: 'e1', title: 'Review', start: '2026-12-07T09:00:00Z', end: '2026-12-07T10:00:00Z',
      series_start: '2026-12-07T09:00:00Z', series_end: '2026-12-07T10:00:00Z', all_day: false, recurring: false, repeat: { freq: '' }, editable: false,
      resources: [{ id: 'r2', name: 'Harbour', kind: 'room', location: 'Floor 4', status: 'pending' }],
    };
    render(<EventDialog calendars={cals} event={ev} initial={emptyForm(new Date(), new Date(), false, 'cal_p')} onDone={vi.fn()} onClose={vi.fn()} />);
    expect(screen.getByText('Harbour, Floor 4')).toBeTruthy();
    expect(screen.getByText('Waiting for approval')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Add room' })).toBeNull();
  });

  it('names the conflicting dates of a declined series', () => {
    capture();
    const ev: EventInfo = {
      calendar_id: 'cal_p', uid: 'u1', etag: 'e1', title: 'Review', start: '2026-12-07T09:00:00Z', end: '2026-12-07T10:00:00Z',
      series_start: '2026-12-07T09:00:00Z', series_end: '2026-12-07T10:00:00Z', all_day: false, recurring: true, repeat: { freq: 'weekly' }, editable: false,
      resources: [{ id: 'r1', name: 'Fjord', kind: 'room', location: 'Floor 2', status: 'declined', reason: 'busy', detail: '2026-12-14, 2026-12-21' }],
    };
    render(<EventDialog calendars={cals} event={ev} initial={emptyForm(new Date(), new Date(), false, 'cal_p')} onDone={vi.fn()} onClose={vi.fn()} />);
    const day = (d: string) => new Date(`${d}T00:00:00Z`).toLocaleDateString([], { dateStyle: 'medium', timeZone: 'UTC' });
    expect(screen.getByText('Declined').parentElement!.textContent).toBe(`Declined · busy on ${day('2026-12-14')}, ${day('2026-12-21')}`);
  });
});

describe('RoomRequests', () => {
  it('lists requests without titles and accepts one', async () => {
    const posted: string[] = [];
    let pending = [{ id: 'rbk_1', resource_id: 'r2', resource_name: 'Harbour', organizer: 'Ann', first: { start: '2026-12-07T09:00:00Z', end: '2026-12-07T10:00:00Z' }, last: { start: '2026-12-07T09:00:00Z', end: '2026-12-07T10:00:00Z' }, occurrences: 1 }];
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      if (init?.method === 'POST') {
        posted.push(`${input} ${init.body}`);
        pending = [];
        return new Response(null, { status: 204 });
      }
      return new Response(JSON.stringify({ requests: pending }), { status: 200 });
    });
    render(<RoomRequests />);
    await screen.findByRole('heading', { name: 'Harbour' });
    expect(screen.getByText('Requested by Ann')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Accept' }));
    await screen.findByText('No requests waiting.');
    expect(screen.getByRole('status').textContent).toMatch(/^Accepted: Harbour, /);
    expect(posted).toEqual(['/api/resources/requests/rbk_1 {"decision":"accept"}']);
  });
});
