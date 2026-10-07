(globalThis as unknown as { process: { env: Record<string, string> } }).process.env.TZ = 'Europe/Berlin'; // vitest runs in Node

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
    delta: { years: 0, months: 0, days: 0, milliseconds: shiftHours * 3600_000 },
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
      delta: { years: 0, months: 0, days: 2, milliseconds: 0 },
      revert: vi.fn(),
    };
    (fcProps.at(-1)!.eventDrop as (a: unknown) => void)(arg);
    await waitFor(() => expect(puts).toHaveLength(1));
    expect(puts[0].body).toMatchObject({ all_day: true, start: '2026-10-09', end: '2026-10-10' });
  });

  const dayDelta = (days: number, ms = 0) => ({ years: 0, months: 0, days, milliseconds: ms });
  const fire = (name: string, arg: unknown) => (fcProps.at(-1)![name] as (a: unknown) => void)(arg);

  it('keeps the series wall clock across DST (Berlin, Oct 24 09:00 to Oct 25 09:00)', async () => {
    expect(new Date(2026, 9, 24, 9).getTimezoneOffset()).toBe(-120);
    expect(new Date(2026, 9, 25, 9).getTimezoneOffset()).toBe(-60);
    const puts = setup();
    render(<CalendarPage />);
    await waitFor(() => expect(fcProps.length).toBeGreaterThan(0));
    const ev: EventInfo = { ...series, start: '2026-10-24T07:00:00Z', end: '2026-10-24T08:00:00Z', series_start: '2026-10-03T07:00:00Z', series_end: '2026-10-03T08:00:00Z' };
    fire('eventDrop', {
      event: { start: new Date(2026, 9, 25, 9), end: new Date(2026, 9, 25, 10), allDay: false, extendedProps: { info: ev } },
      oldEvent: { start: new Date(2026, 9, 24, 9), end: new Date(2026, 9, 24, 10), allDay: false },
      delta: dayDelta(1),
      revert: vi.fn(),
    });
    fireEvent.click(await screen.findByRole('button', { name: 'All events' }));
    await waitFor(() => expect(puts).toHaveLength(1));
    expect(puts[0].body.start).toBe('2026-10-04T07:00:00.000Z'); // still 09:00 CEST
  });

  it('shifts an all-day series by whole days without an off-by-one', async () => {
    const puts = setup();
    render(<CalendarPage />);
    await waitFor(() => expect(fcProps.length).toBeGreaterThan(0));
    const ev: EventInfo = { ...series, all_day: true, start: '2026-10-14', end: '2026-10-15', series_start: '2026-10-07', series_end: '2026-10-09' };
    fire('eventDrop', {
      event: { start: new Date(2026, 9, 15), end: new Date(2026, 9, 16), allDay: true, extendedProps: { info: ev } },
      oldEvent: { start: new Date(2026, 9, 14), end: new Date(2026, 9, 15), allDay: true },
      delta: dayDelta(1),
      revert: vi.fn(),
    });
    fireEvent.click(await screen.findByRole('button', { name: 'All events' }));
    await waitFor(() => expect(puts).toHaveLength(1));
    expect(puts[0].body).toMatchObject({ all_day: true, start: '2026-10-08', end: '2026-10-10' });
  });

  it('resize changes only the end', async () => {
    const puts = setup();
    render(<CalendarPage />);
    await waitFor(() => expect(fcProps.length).toBeGreaterThan(0));
    fire('eventResize', {
      event: { start: new Date(single.start), end: new Date('2026-10-07T11:00:00Z'), allDay: false, extendedProps: { info: single } },
      oldEvent: { start: new Date(single.start), end: new Date(single.end), allDay: false },
      startDelta: dayDelta(0),
      endDelta: dayDelta(0, 3600_000),
      revert: vi.fn(),
    });
    await waitFor(() => expect(puts).toHaveLength(1));
    expect(puts[0].body.start).toBe('2026-10-07T09:00:00.000Z');
    expect(puts[0].body.end).toBe('2026-10-07T11:00:00.000Z');
  });

  it('gives an all-day event dragged into the time grid one hour', async () => {
    const puts = setup();
    render(<CalendarPage />);
    await waitFor(() => expect(fcProps.length).toBeGreaterThan(0));
    const ev: EventInfo = { ...single, all_day: true, start: '2026-10-07', end: '2026-10-08', series_start: '2026-10-07', series_end: '2026-10-08' };
    fire('eventDrop', {
      event: { start: new Date(2026, 9, 7, 9), end: null, allDay: false, extendedProps: { info: ev } },
      oldEvent: { start: new Date(2026, 9, 7), end: null, allDay: true },
      delta: dayDelta(0, 9 * 3600_000),
      revert: vi.fn(),
    });
    await waitFor(() => expect(puts).toHaveLength(1));
    expect(puts[0].body).toMatchObject({ all_day: false, start: '2026-10-07T07:00:00.000Z', end: '2026-10-07T08:00:00.000Z' });
  });

  it('refuses an all-day toggle on a repeating event with All events', async () => {
    const puts = setup();
    render(<CalendarPage />);
    await waitFor(() => expect(fcProps.length).toBeGreaterThan(0));
    const revert = vi.fn();
    fire('eventDrop', {
      event: { start: new Date(2026, 9, 14), end: null, allDay: true, extendedProps: { info: series } },
      oldEvent: { start: new Date(series.start), end: new Date(series.end), allDay: false },
      delta: dayDelta(0),
      revert,
    });
    fireEvent.click(await screen.findByRole('button', { name: 'All events' }));
    expect((await screen.findByRole('alert')).textContent).toContain('event form');
    expect(revert).toHaveBeenCalled();
    expect(puts).toHaveLength(0);
  });
});
