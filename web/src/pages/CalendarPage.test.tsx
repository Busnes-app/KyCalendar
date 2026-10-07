import { cleanup, render, screen, waitFor, fireEvent } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const fcProps: Record<string, unknown>[] = [];
vi.mock('@fullcalendar/react', () => ({
  default: (props: Record<string, unknown>) => {
    fcProps.push(props);
    return <div data-testid="fullcalendar" />;
  },
}));

import { CalendarPage, HIDDEN_KEY, toFcEvent } from './CalendarPage';
import type { EventInfo } from '../calendarApi';

const calendars = [
  { id: 'cal_p', name: 'Mine', color: '#00aa11', description: '', kind: 'personal', role: 'owner', dav_path: '/dav/u/calendars/default/' },
  { id: 'cal_g', name: 'Team', color: '', description: '', kind: 'group', role: 'reader', dav_path: '/dav/u/calendars/_cal_g/' },
];
const ev: EventInfo = {
  calendar_id: 'cal_g', uid: 'u1', etag: 'e', title: '<b>x</b>', start: '2026-10-07T09:00:00+02:00', end: '2026-10-07T10:00:00+02:00',
  series_start: '2026-10-07T09:00:00+02:00', series_end: '2026-10-07T10:00:00+02:00', all_day: false, recurring: false,
  repeat: { freq: '' }, editable: false,
};

function mockFetch(handlers: Record<string, () => unknown>) {
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const path = String(input).split('?')[0];
    const handler = handlers[path];
    if (!handler) throw new Error(`unexpected ${path}`);
    return new Response(JSON.stringify(handler()), { status: 200 });
  });
}

beforeEach(() => {
  fcProps.length = 0;
  localStorage.clear();
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe('CalendarPage', () => {
  it('maps API events to FullCalendar events, read-only for readers', () => {
    const fc = toFcEvent(ev, '#123456');
    expect(fc).toMatchObject({ title: '<b>x</b>', allDay: false, editable: false, backgroundColor: '#123456' });
    expect(toFcEvent({ ...ev, title: '' }, '#123456').title).toBe('(no title)');
    expect(toFcEvent({ ...ev, editable: true, partial: true }, '#1').editable).toBe(false);
  });

  it('loads calendars, offers the four views and fetches only visible calendars', async () => {
    mockFetch({ '/api/calendars': () => calendars, '/api/events': () => [ev] });
    render(<CalendarPage />);
    await screen.findByLabelText('Show Team');
    const props = fcProps.at(-1)!;
    expect(String((props.headerToolbar as { right: string }).right)).toBe('dayGridMonth,timeGridWeek,timeGridDay,listWeek');
    expect(props.timeZone).toBe('local');
    expect(props.eventInteractive).toBe(true);

    fireEvent.click(screen.getByLabelText('Show Team'));
    expect(JSON.parse(localStorage.getItem(HIDDEN_KEY)!)).toEqual(['cal_g']);
    const success = vi.fn();
    const fetchEvents = fcProps.at(-1)!.events as (info: { start: Date; end: Date }, ok: (e: unknown[]) => void, fail: (e: Error) => void) => void;
    fetchEvents({ start: new Date('2026-10-05T00:00:00Z'), end: new Date('2026-10-12T00:00:00Z') }, success, vi.fn());
    await waitFor(() => expect(success).toHaveBeenCalled());
    const eventsCall = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls.map((c) => String(c[0])).find((u) => u.startsWith('/api/events'))!;
    expect(new URL(eventsCall, 'http://x').searchParams.get('calendar')).toBe('cal_p');
  });

  it('shows an alert when calendars cannot load', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('{}', { status: 500 }));
    render(<CalendarPage />);
    expect((await screen.findByRole('alert')).textContent).toMatch(/could not load/i);
  });
});
