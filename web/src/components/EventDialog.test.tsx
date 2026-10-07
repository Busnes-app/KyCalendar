import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { EventDialog } from './EventDialog';
import { emptyForm, formFromEvent } from '../eventForm';
import type { CalendarInfo, EventInfo } from '../calendarApi';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const cals: CalendarInfo[] = [
  { id: 'cal_p', name: 'Mine', color: '', description: '', kind: 'personal', role: 'owner', dav_path: '' },
  { id: 'cal_r', name: 'News', color: '', description: '', kind: 'group', role: 'reader', dav_path: '' },
];
const recurring: EventInfo = {
  calendar_id: 'cal_p', uid: 'u1', recurrence_id: '20261012T070000Z', etag: 'e1', title: 'Standup',
  description: 'notes https://example.com <img src=x onerror=alert(1)>', start: '2026-10-12T09:00:00+02:00',
  end: '2026-10-12T10:00:00+02:00', series_start: '2026-10-05T09:00:00+02:00', series_end: '2026-10-05T10:00:00+02:00',
  all_day: false, recurring: true, repeat: { freq: 'custom' }, zone: 'Europe/Berlin', editable: true,
};

function capture() {
  const calls: { method: string; url: string; body?: unknown; ifMatch?: string | null }[] = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const headers = new Headers(init?.headers);
    calls.push({ method: init?.method ?? 'GET', url: String(input), body: init?.body ? JSON.parse(String(init.body)) : undefined, ifMatch: headers.get('If-Match') });
    return new Response(JSON.stringify({ etag: 'e2', uid: 'new' }), { status: init?.method === 'POST' ? 201 : 200 });
  });
  return calls;
}

describe('EventDialog', () => {
  it('creates an event in a writable calendar only', async () => {
    const calls = capture();
    const onDone = vi.fn();
    render(<EventDialog calendars={cals} initial={emptyForm(new Date('2026-10-07T09:00:00Z'), new Date('2026-10-07T10:00:00Z'), false, 'cal_p')} onDone={onDone} onClose={vi.fn()} />);
    expect(screen.queryByRole('option', { name: 'News' })).toBeNull();
    fireEvent.change(screen.getByLabelText('Title'), { target: { value: 'Lunch' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(onDone).toHaveBeenCalled());
    expect(calls[0]).toMatchObject({ method: 'POST', url: '/api/calendars/cal_p/events' });
    expect((calls[0].body as { title: string }).title).toBe('Lunch');
  });

  it('edits one occurrence or the series, keeping a custom rule, with If-Match', async () => {
    const calls = capture();
    render(<EventDialog calendars={cals} event={recurring} initial={formFromEvent(recurring, 'this')} onDone={vi.fn()} onClose={vi.fn()} />);
    expect(screen.getByText('Custom (edit on your device)')).toBeTruthy();
    fireEvent.click(screen.getByLabelText('All events'));
    expect((screen.getByLabelText('Start') as HTMLInputElement).value).toContain('T');
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(calls).toHaveLength(1));
    expect(calls[0]).toMatchObject({ method: 'PUT', url: '/api/events/cal_p/u1', ifMatch: '"e1"' });
    expect(calls[0].body).toMatchObject({ scope: 'all', repeat: { freq: 'custom' }, zone: 'Europe/Berlin' });
    expect(new Date((calls[0].body as { start: string }).start).toISOString()).toBe('2026-10-05T07:00:00.000Z');
  });

  it('deletes one occurrence', async () => {
    const calls = capture();
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    render(<EventDialog calendars={cals} event={recurring} initial={formFromEvent(recurring, 'this')} onDone={vi.fn()} onClose={vi.fn()} />);
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(calls).toHaveLength(1));
    expect(calls[0].method).toBe('DELETE');
    expect(calls[0].url).toBe('/api/events/cal_p/u1?scope=this&recurrence_id=20261012T070000Z');
  });

  it('reports a conflict and asks to reload', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({ error: 'changed', code: 'conflict' }), { status: 412 }));
    const onDone = vi.fn();
    render(<EventDialog calendars={cals} event={recurring} initial={formFromEvent(recurring, 'this')} onDone={onDone} onClose={vi.fn()} />);
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect((await screen.findByRole('alert')).textContent).toMatch(/changed elsewhere/i);
    expect(onDone).not.toHaveBeenCalled();
  });

  it('shows a read-only event as text with safe links and no actions', () => {
    const ro = { ...recurring, calendar_id: 'cal_r', editable: false };
    const { container } = render(<EventDialog calendars={cals} event={ro} initial={formFromEvent(ro, 'this')} onDone={vi.fn()} onClose={vi.fn()} />);
    expect(screen.queryByRole('button', { name: 'Save' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Delete' })).toBeNull();
    expect(container.querySelector('img')).toBeNull();
    expect([...container.querySelectorAll('a')].map((a) => a.getAttribute('href'))).toEqual(['https://example.com']);
  });

  it('focuses Title when editing, Close when read-only, and Escape closes', () => {
    const onClose = vi.fn();
    const { unmount } = render(<EventDialog calendars={cals} initial={emptyForm(new Date('2026-10-07T09:00:00Z'), new Date('2026-10-07T10:00:00Z'), false, 'cal_p')} onDone={vi.fn()} onClose={onClose} />);
    expect(document.activeElement).toBe(screen.getByLabelText('Title'));
    fireEvent(document.querySelector('dialog')!, new Event('cancel', { cancelable: true }));
    expect(onClose).toHaveBeenCalledTimes(1);
    unmount();
    const ro = { ...recurring, editable: false };
    render(<EventDialog calendars={cals} event={ro} initial={formFromEvent(ro, 'this')} onDone={vi.fn()} onClose={onClose} />);
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Close' }));
  });

  it('restores focus to the opener on unmount', () => {
    const opener = document.createElement('button');
    document.body.append(opener);
    opener.focus();
    const { unmount } = render(<EventDialog calendars={cals} initial={emptyForm(new Date(), new Date(), false, 'cal_p')} onDone={vi.fn()} onClose={vi.fn()} />);
    unmount();
    expect(document.activeElement).toBe(opener);
    opener.remove();
  });

  it('toggles all-day values', () => {
    render(<EventDialog calendars={cals} initial={emptyForm(new Date('2026-10-07T09:00:00Z'), new Date('2026-10-07T10:00:00Z'), false, 'cal_p')} onDone={vi.fn()} onClose={vi.fn()} />);
    fireEvent.click(screen.getByLabelText('All day'));
    const start = screen.getByLabelText('Start') as HTMLInputElement;
    expect(start.value).toMatch(/^\d{4}-\d{2}-\d{2}$/);
    fireEvent.click(screen.getByLabelText('All day'));
    expect(start.value).toMatch(/T09:00$/);
    expect((screen.getByLabelText('End') as HTMLInputElement).value).toMatch(/T10:00$/);
  });

  it('sends scope all without recurrence_id for a non-recurring edit', async () => {
    const calls = capture();
    const one = { ...recurring, recurring: false, recurrence_id: undefined, repeat: { freq: '' as const } };
    render(<EventDialog calendars={cals} event={one} initial={formFromEvent(one, 'this')} onDone={vi.fn()} onClose={vi.fn()} />);
    expect(screen.queryByLabelText('All events')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(calls).toHaveLength(1));
    expect(calls[0].body).toMatchObject({ scope: 'all' });
    expect(calls[0].body).not.toHaveProperty('recurrence_id');
  });

  it('warns that All events drops single-event changes, and confirms deleting every occurrence', async () => {
    const calls = capture();
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);
    render(<EventDialog calendars={cals} event={recurring} initial={formFromEvent(recurring, 'this')} onDone={vi.fn()} onClose={vi.fn()} />);
    expect(screen.queryByText(/removes changes made to single events/)).toBeNull();
    fireEvent.click(screen.getByLabelText('All events'));
    expect(screen.getByText('Moving all events removes changes made to single events, including deleted ones.')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
    expect(confirm).toHaveBeenCalledWith('Delete every occurrence of this event?');
    await waitFor(() => expect(calls).toHaveLength(1));
    expect(calls[0].url).toBe('/api/events/cal_p/u1?scope=all');
  });

  it('keeps typed text when the scope changes', () => {
    render(<EventDialog calendars={cals} event={recurring} initial={formFromEvent(recurring, 'this')} onDone={vi.fn()} onClose={vi.fn()} />);
    fireEvent.change(screen.getByLabelText('Title'), { target: { value: 'Typed' } });
    fireEvent.click(screen.getByLabelText('All events'));
    expect((screen.getByLabelText('Title') as HTMLInputElement).value).toBe('Typed');
  });

  it('shows the new event when the dialog is reopened with a new key', () => {
    const b = { ...recurring, uid: 'u2', title: 'Other' };
    const el = (ev: EventInfo, key: number) => <EventDialog key={key} calendars={cals} event={ev} initial={formFromEvent(ev, 'this')} onDone={vi.fn()} onClose={vi.fn()} />;
    const { rerender } = render(el(recurring, 1));
    fireEvent.change(screen.getByLabelText('Title'), { target: { value: 'edited A' } });
    rerender(el(b, 2));
    expect((screen.getByLabelText('Title') as HTMLInputElement).value).toBe('Other');
  });
});
