import { act, cleanup, render, screen, waitFor, fireEvent } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const fcProps: Record<string, unknown>[] = [];
vi.mock('@fullcalendar/react', () => ({
  default: (props: Record<string, unknown>) => {
    fcProps.push(props);
    // Render the custom toolbar buttons (New event) as FullCalendar does: a button per entry.
    const custom = (props.customButtons ?? {}) as Record<string, { text: string; click: () => void }>;
    return <div data-testid="fullcalendar">{Object.entries(custom).map(([name, b]) => <button key={name} type="button" className={`fc-${name}-button`} onClick={b.click}>{b.text}</button>)}</div>;
  },
}));

import { CalendarPage, HIDDEN_KEY, loadHidden, toFcEvent, zoneAbbr } from './CalendarPage';
import type { EventInfo } from '../calendarApi';

const calendars = [
  { id: 'cal_p', name: 'Mine', color: '#00aa11', description: '', kind: 'personal', role: 'owner', dav_path: '/dav/u/calendars/default/', components: ['VEVENT'] },
  { id: 'cal_g', name: 'Team', color: '', description: '', kind: 'group', role: 'reader', dav_path: '/dav/u/calendars/_cal_g/', components: ['VEVENT'] },
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

// The app shell's sidebar slot, which the calendar list always renders into.
function renderPage() {
  const slot = document.body.appendChild(document.createElement('div'));
  return { slot, ...render(<CalendarPage sidebarSlot={slot} />) };
}

// A controllable (max-width: 720px) media query.
function stubNarrow(matches: boolean) {
  const listeners = new Set<() => void>();
  const m = { matches, addEventListener: (_: string, f: () => void) => listeners.add(f), removeEventListener: (_: string, f: () => void) => listeners.delete(f) };
  vi.stubGlobal('matchMedia', () => m);
  return (next: boolean) => act(() => { m.matches = next; listeners.forEach((f) => f()); });
}

beforeEach(() => {
  fcProps.length = 0;
  localStorage.clear();
});
afterEach(() => {
  cleanup();
  document.body.replaceChildren();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('CalendarPage', () => {
  it('maps API events to FullCalendar events, read-only for readers', () => {
    const fc = toFcEvent(ev, '#123456');
    expect(fc).toMatchObject({ title: '<b>x</b>', allDay: false, editable: false, backgroundColor: '#123456' });
    expect(toFcEvent({ ...ev, title: '' }, '#123456').title).toBe('(no title)');
    expect(toFcEvent({ ...ev, editable: true, partial: true }, '#1').editable).toBe(false);
  });

  it('puts the calendar list only in the app sidebar slot', async () => {
    mockFetch({ '/api/calendars': () => calendars, '/api/events': () => [ev] });
    const { slot, container } = renderPage();
    await waitFor(() => expect(slot.querySelector('aside[aria-label="Calendars"]')).not.toBeNull());
    expect(container.querySelector('aside')).toBeNull();
  });

  it('keeps the same calendar list and its drafts across the phone breakpoint', async () => {
    const narrow = stubNarrow(false);
    mockFetch({ '/api/calendars': () => calendars, '/api/events': () => [ev] });
    const { slot } = renderPage();
    const aside = await screen.findByRole('complementary', { name: 'Calendars' });
    const details = slot.querySelector('details')!;
    fireEvent.click(screen.getByRole('button', { name: 'Edit Mine' }));
    const rename = screen.getByLabelText('Name');
    fireEvent.change(rename, { target: { value: 'Draft' } });
    expect(details.open).toBe(true);

    // Focus in the list keeps the disclosure open when the screen narrows; Escape closes it.
    rename.focus();
    narrow(true);
    expect(details.open).toBe(true);
    fireEvent.keyDown(rename, { key: 'Escape' });
    expect(details.open).toBe(false);
    expect(document.activeElement).toBe(slot.querySelector('summary'));
    fireEvent.click(slot.querySelector('summary')!);
    expect(details.open).toBe(true);

    // The subscribe dialog keeps its draft across the breakpoint, and Escape in it leaves the list open.
    fireEvent.click(screen.getByRole('button', { name: 'Add a calendar' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Subscribe to a calendar' }));
    const link = screen.getByLabelText('Link');
    fireEvent.change(link, { target: { value: 'https://example.test/a.ics' } });
    narrow(false);
    narrow(true);
    expect(details.open).toBe(true);
    expect(link).toHaveProperty('value', 'https://example.test/a.ics');
    fireEvent.keyDown(link, { key: 'Escape' });
    expect(details.open).toBe(true);

    narrow(false);
    expect(screen.getByRole('complementary', { name: 'Calendars' })).toBe(aside);
    expect(slot.contains(aside)).toBe(true);
    expect(aside.contains(rename)).toBe(true);
    expect(rename).toHaveProperty('value', 'Draft');
  });

  it('loads calendars, offers the four views and fetches only visible calendars', async () => {
    mockFetch({ '/api/calendars': () => calendars, '/api/events': () => [ev] });
    renderPage();
    await screen.findByLabelText('Show Team');
    const props = fcProps.at(-1)!;
    expect(String((props.headerToolbar as { right: string }).right)).toBe('dayGridMonth,timeGridWeek,timeGridDay,listWeek newEvent');
    // The time grid's corner names the zone; Month and List have no corner.
    expect((props.views as { timeGrid: { weekNumbers: boolean } }).timeGrid.weekNumbers).toBe(true);
    expect(zoneAbbr(new Date('2026-07-01T12:00:00Z'))).not.toBe('');
    expect(screen.getByRole('heading', { level: 1, name: 'Calendar' })).toBeTruthy();
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

  it('opens day and week views on the working day in a scrolling grid, month and list at full height', async () => {
    mockFetch({ '/api/calendars': () => calendars, '/api/events': () => [] });
    renderPage();
    await screen.findByLabelText('Show Team');
    expect(fcProps.at(-1)!.scrollTime).toBe('07:00:00');
    const datesSet = fcProps.at(-1)!.datesSet as (arg: { view: { type: string; currentStart: Date; currentEnd: Date } }) => void;
    act(() => datesSet({ view: { type: 'dayGridMonth', currentStart: new Date(2026, 9, 1), currentEnd: new Date(2026, 10, 1) } }));
    expect(fcProps.at(-1)!.height).toBe('auto');
    act(() => datesSet({ view: { type: 'timeGridWeek', currentStart: new Date(2026, 9, 25), currentEnd: new Date(2026, 10, 1) } }));
    expect(fcProps.at(-1)!.height).not.toBe('auto');
  });

  it('colours each calendar distinctly, with readable event text', async () => {
    mockFetch({ '/api/calendars': () => calendars, '/api/events': () => [ev, { ...ev, uid: 'u2', calendar_id: 'cal_p' }] });
    renderPage();
    await screen.findByLabelText('Show Team');
    const success = vi.fn();
    const fetchEvents = fcProps.at(-1)!.events as (info: { start: Date; end: Date }, ok: (e: unknown[]) => void, fail: (e: Error) => void) => void;
    fetchEvents({ start: new Date('2026-10-05T00:00:00Z'), end: new Date('2026-10-12T00:00:00Z') }, success, vi.fn());
    await waitFor(() => expect(success).toHaveBeenCalled());
    const [team, mine] = success.mock.calls[0][0] as { backgroundColor: string; textColor?: string }[];
    expect(mine.backgroundColor).toBe('#00aa11');
    expect(mine.textColor).toBe('#182326');
    // The first calendar without a colour takes the theme accent, whose text is the theme's button text.
    expect(team.backgroundColor).toBe('var(--accent)');
    expect(team.textColor).toBeUndefined();
  });

  it('shows an alert when calendars cannot load', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('{}', { status: 500 }));
    renderPage();
    expect((await screen.findByRole('alert')).textContent).toMatch(/could not load/i);
  });

  it('does not request events when there are no calendars', async () => {
    mockFetch({ '/api/calendars': () => [] });
    renderPage();
    await waitFor(() => expect(fcProps.length).toBeGreaterThan(0));
    const success = vi.fn();
    const fetchEvents = fcProps.at(-1)!.events as (info: { start: Date; end: Date }, ok: (e: unknown[]) => void, fail: (e: Error) => void) => void;
    fetchEvents({ start: new Date(), end: new Date() }, success, vi.fn());
    expect(success).toHaveBeenCalledWith([]);
    const urls = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls.map((c) => String(c[0]));
    expect(urls.some((u) => u.startsWith('/api/events'))).toBe(false);
  });

  it('keeps the same events function across unrelated renders', async () => {
    mockFetch({ '/api/calendars': () => calendars, '/api/events': () => [] });
    renderPage();
    await screen.findByLabelText('Show Team');
    const first = fcProps.at(-1)!.events;
    fireEvent.click(screen.getByLabelText('Show Team'));
    expect(fcProps.at(-1)!.events).not.toBe(first);
    const second = fcProps.at(-1)!.events;
    fireEvent.click(screen.getByRole('button', { name: 'New event' }));
    expect(fcProps.at(-1)!.events).toBe(second);
  });

  it('offers no subscribed or read-only calendar for a new event', async () => {
    const sub = { id: 'cal_s', name: 'Work', color: '', description: '', kind: 'personal', role: 'owner', dav_path: '', components: ['VEVENT'], subscription: { host: 'outlook.office365.com', refreshed_at: null, checked_at: null } };
    mockFetch({ '/api/calendars': () => [sub, ...calendars], '/api/events': () => [] });
    renderPage();
    await screen.findByLabelText('Show Work');
    fireEvent.click(screen.getByRole('button', { name: 'New event' }));
    const select = screen.getByLabelText('Calendar') as HTMLSelectElement;
    expect([...select.options].map((o) => o.value)).toEqual(['cal_p']);
    expect(select.value).toBe('cal_p');
  });

  it('disables New event when only subscribed calendars exist', async () => {
    const sub = { id: 'cal_s', name: 'Work', color: '', description: '', kind: 'personal', role: 'owner', dav_path: '', components: ['VEVENT'], subscription: { host: 'h', refreshed_at: null, checked_at: null } };
    mockFetch({ '/api/calendars': () => [sub], '/api/events': () => [] });
    renderPage();
    await screen.findByLabelText('Show Work');
    expect((screen.getByRole('button', { name: 'New event' }) as HTMLButtonElement).disabled).toBe(true);
    expect(fcProps.at(-1)!.selectable).toBe(false);
  });

  it.each(['not json', '{}', '[1,"a"]'])('loadHidden survives %s', (raw) => {
    localStorage.setItem(HIDDEN_KEY, raw);
    expect([...loadHidden()]).toEqual(raw === '[1,"a"]' ? ['a'] : []);
  });
});
