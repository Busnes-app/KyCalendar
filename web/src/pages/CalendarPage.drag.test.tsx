import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

const fcProps: Record<string, unknown>[] = [];
vi.mock('@fullcalendar/react', () => ({
  default: (props: Record<string, unknown>) => {
    fcProps.push(props);
    return <div />;
  },
}));

import { CalendarPage } from './CalendarPage';
import type { EventInfo } from '../calendarApi';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  fcProps.length = 0;
});

const single: EventInfo = {
  calendar_id: 'cal_p', uid: 'u1', etag: 'e1', title: 'A', start: '2026-10-07T09:00:00Z', end: '2026-10-07T10:00:00Z',
  series_start: '2026-10-07T09:00:00Z', series_end: '2026-10-07T10:00:00Z', all_day: false, recurring: false,
  repeat: { freq: '' }, zone: 'UTC', editable: true,
};
const series: EventInfo = { ...single, uid: 'u2', recurring: true, recurrence_id: '20261014T090000Z', start: '2026-10-14T09:00:00Z', end: '2026-10-14T10:00:00Z', repeat: { freq: 'weekly' } };

function setup(putStatus = 200) {
  const puts: { url: string; body: Record<string, unknown> }[] = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const url = String(input);
    if (init?.method === 'PUT') {
      puts.push({ url, body: JSON.parse(String(init.body)) });
      return new Response(JSON.stringify({ etag: 'e2', error: 'precondition_failed' }), { status: putStatus });
    }
    if (url.startsWith('/api/calendars')) return new Response(JSON.stringify([{ id: 'cal_p', name: 'Mine', color: '', description: '', kind: 'personal', role: 'owner', dav_path: '' }]), { status: 200 });
    return new Response('[]', { status: 200 });
  });
  return puts;
}

function drag(ev: EventInfo, shiftHours: number) {
  const revert = vi.fn();
  const start = new Date(ev.start);
  const end = new Date(ev.end);
  const move = (d: Date) => new Date(d.getTime() + shiftHours * 3600_000);
  const arg = {
    event: { start: move(start), end: move(end), allDay: false, extendedProps: { info: ev } },
    oldEvent: { start, end, allDay: false },
    revert,
  };
  (fcProps.at(-1)!.eventDrop as (a: unknown) => void)(arg);
  return revert;
}

describe('drag', () => {
  it('moves a single event with If-Match and scope all', async () => {
    const puts = setup();
    render(<CalendarPage />);
    await waitFor(() => expect(fcProps.length).toBeGreaterThan(0));
    drag(single, 2);
    await waitFor(() => expect(puts).toHaveLength(1));
    expect(puts[0].url).toBe('/api/events/cal_p/u1');
    expect(new Date(puts[0].body.start as string).toISOString()).toBe('2026-10-07T11:00:00.000Z');
  });

  it('asks for a recurring event and shifts the whole series by the drag delta', async () => {
    const puts = setup();
    render(<CalendarPage />);
    await waitFor(() => expect(fcProps.length).toBeGreaterThan(0));
    drag(series, 1);
    fireEvent.click(await screen.findByRole('button', { name: 'All events' }));
    await waitFor(() => expect(puts).toHaveLength(1));
    expect(puts[0].body).toMatchObject({ scope: 'all', repeat: { freq: 'custom' } });
    expect(new Date(puts[0].body.start as string).toISOString()).toBe('2026-10-07T10:00:00.000Z');
  });

  it('moves one occurrence with scope this', async () => {
    const puts = setup();
    render(<CalendarPage />);
    await waitFor(() => expect(fcProps.length).toBeGreaterThan(0));
    drag(series, 1);
    fireEvent.click(await screen.findByRole('button', { name: 'This event' }));
    await waitFor(() => expect(puts).toHaveLength(1));
    expect(puts[0].body).toMatchObject({ scope: 'this', recurrence_id: '20261014T090000Z' });
    expect(new Date(puts[0].body.start as string).toISOString()).toBe('2026-10-14T10:00:00.000Z');
  });

  it('reverts when the user cancels', async () => {
    const puts = setup();
    render(<CalendarPage />);
    await waitFor(() => expect(fcProps.length).toBeGreaterThan(0));
    const revert = drag(series, 1);
    fireEvent.click(await screen.findByRole('button', { name: 'Cancel' }));
    expect(revert).toHaveBeenCalled();
    expect(puts).toHaveLength(0);
  });

  it('reverts on Escape', async () => {
    const puts = setup();
    render(<CalendarPage />);
    await waitFor(() => expect(fcProps.length).toBeGreaterThan(0));
    const revert = drag(series, 1);
    const dlg = (await screen.findByRole('button', { name: 'Cancel' })).closest('dialog')!;
    fireEvent(dlg, new Event('cancel', { cancelable: true }));
    expect(revert).toHaveBeenCalled();
    expect(puts).toHaveLength(0);
  });

  it('reverts and shows the conflict message on 412', async () => {
    const puts = setup(412);
    render(<CalendarPage />);
    await waitFor(() => expect(fcProps.length).toBeGreaterThan(0));
    const revert = drag(single, 1);
    await waitFor(() => expect(puts).toHaveLength(1));
    expect((await screen.findByRole('alert')).textContent).toContain('changed elsewhere');
    expect(revert).toHaveBeenCalled();
  });

  it('moves an all-day event by whole days', async () => {
    const puts = setup();
    render(<CalendarPage />);
    await waitFor(() => expect(fcProps.length).toBeGreaterThan(0));
    const ev: EventInfo = { ...single, all_day: true, start: '2026-10-07', end: '2026-10-08', series_start: '2026-10-07', series_end: '2026-10-08' };
    const arg = {
      event: { start: new Date(2026, 9, 9), end: null, allDay: true, extendedProps: { info: ev } },
      oldEvent: { start: new Date(2026, 9, 7), end: null, allDay: true },
      revert: vi.fn(),
    };
    (fcProps.at(-1)!.eventDrop as (a: unknown) => void)(arg);
    await waitFor(() => expect(puts).toHaveLength(1));
    expect(puts[0].body).toMatchObject({ all_day: true, start: '2026-10-09', end: '2026-10-10' });
  });
});
