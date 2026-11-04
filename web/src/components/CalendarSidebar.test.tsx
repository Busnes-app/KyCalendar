import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { CalendarSidebar, ago } from './CalendarSidebar';
import { browserDateOrder, browserZone, type CalendarInfo } from '../calendarApi';
import { PALETTE, calendarColors, nextColor } from '../calendarColors';

// The colour a new calendar gets: one none of the listed calendars shows.
const unused = (list: CalendarInfo[]) => nextColor([...calendarColors(list).values()]);

// Expected relative times in the test machine's own locale, as the sidebar renders them.
const rel = (n: number, unit: Intl.RelativeTimeFormatUnit) => new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' }).format(n, unit);

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const cals: CalendarInfo[] = [
  { id: 'cal_p', name: 'Mine', color: '#00aa11', description: '', kind: 'personal', role: 'owner', dav_path: '', components: ['VEVENT'] },
  { id: 'cal_m', name: 'Team', color: '', description: '', kind: 'group', role: 'manager', dav_path: '', components: ['VEVENT'] },
  { id: 'cal_r', name: 'News', color: '', description: '', kind: 'group', role: 'reader', dav_path: '', components: ['VEVENT'] },
];

describe('CalendarSidebar', () => {
  it('adds through one + menu that opens a dialog, and returns focus to the + on cancel', async () => {
    render(<CalendarSidebar calendars={cals} hidden={new Set()} onToggle={vi.fn()} onChanged={vi.fn()} />);
    const plus = screen.getByRole('button', { name: 'Add a calendar' });
    fireEvent.click(plus);
    expect(screen.getAllByRole('menuitem').map((m) => m.textContent)).toEqual(['New calendar', 'Subscribe to a calendar', 'Import a CSV file']);
    fireEvent.click(screen.getByRole('menuitem', { name: 'New calendar' }));
    const dialog = screen.getByRole('dialog', { name: 'New calendar' });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('dialog')).toBeNull();
    await waitFor(() => expect(document.activeElement).toBe(plus));
  });

  it('shows calendars stored without a colour in distinct colours, and gives a new one an unused colour', async () => {
    const post = vi.fn((_init?: RequestInit) => new Response(JSON.stringify(cals[0]), { status: 201 }));
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (_i, init) => post(init));
    const { container } = render(<CalendarSidebar calendars={cals} hidden={new Set()} onToggle={vi.fn()} onChanged={vi.fn()} />);
    const shown = [...container.querySelectorAll<HTMLElement>('.kc-swatch')].map((el) => el.style.background);
    expect(new Set(shown).size).toBe(cals.length);
    fireEvent.click(screen.getByRole('button', { name: 'Add a calendar' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'New calendar' }));
    fireEvent.change(within(screen.getByRole('dialog', { name: 'New calendar' })).getByLabelText('Name'), { target: { value: 'Work' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add calendar' }));
    await waitFor(() => expect(post).toHaveBeenCalled());
    const color = JSON.parse(String(post.mock.calls[0][0]?.body)).color;
    expect(PALETTE).toContain(color);
    expect([...calendarColors(cals).values()]).not.toContain(color);
  });

  it('toggles visibility and offers edits only where the role allows', () => {
    const onToggle = vi.fn();
    render(<CalendarSidebar calendars={cals} hidden={new Set(['cal_r'])} onToggle={onToggle} onChanged={vi.fn()} />);
    expect((screen.getByLabelText('Show News') as HTMLInputElement).checked).toBe(false);
    fireEvent.click(screen.getByLabelText('Show Mine'));
    expect(onToggle).toHaveBeenCalledWith('cal_p');
    expect(screen.getByRole('button', { name: 'Edit Mine' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Edit Team' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Edit News' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Delete Mine' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Delete Team' })).toBeNull();
  });

  it('creates a personal calendar', async () => {
    const post = vi.fn((_init?: RequestInit) => new Response(JSON.stringify(cals[0]), { status: 201 }));
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (_i, init) => post(init));
    const onChanged = vi.fn();
    render(<CalendarSidebar calendars={cals} hidden={new Set()} onToggle={vi.fn()} onChanged={onChanged} />);
    fireEvent.click(screen.getByRole('button', { name: 'Add a calendar' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'New calendar' }));
    fireEvent.change(within(screen.getByRole('dialog', { name: 'New calendar' })).getByLabelText('Name'), { target: { value: 'Work' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add calendar' }));
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
  });

  it('creates a task list and marks it', async () => {
    const post = vi.fn((_init?: RequestInit) => new Response(JSON.stringify(cals[0]), { status: 201 }));
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (_i, init) => post(init));
    const onChanged = vi.fn();
    const list: CalendarInfo = { ...cals[0], id: 'cal_t', name: 'Todo', components: ['VTODO'] };
    render(<CalendarSidebar calendars={[...cals, list]} hidden={new Set()} onToggle={vi.fn()} onChanged={onChanged} />);
    expect(screen.getByText('Tasks · open in a tasks app')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Add a calendar' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'New calendar' }));
    fireEvent.change(within(screen.getByRole('dialog', { name: 'New calendar' })).getByLabelText('Name'), { target: { value: 'Shared' } });
    fireEvent.change(screen.getByLabelText('Holds'), { target: { value: 'tasks' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add calendar' }));
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
    expect(JSON.parse(String(post.mock.calls[0][0]?.body))).toEqual({ name: 'Shared', components: ['VTODO'], color: unused([...cals, list]) });
  });

  it('subscribes with the link, name and colour, and shows why a link failed', async () => {
    const post = vi.fn((_url: string, _init?: RequestInit) => new Response(JSON.stringify({ error: 'The link did not return a calendar' }), { status: 422 }));
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (i, init) => post(String(i), init));
    const onChanged = vi.fn();
    render(<CalendarSidebar calendars={cals} hidden={new Set()} onToggle={vi.fn()} onChanged={onChanged} />);
    fireEvent.click(screen.getByRole('button', { name: 'Add a calendar' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Subscribe to a calendar' }));
    expect(screen.getByRole('dialog', { name: 'Subscribe to a calendar' })).toBeTruthy();
    fireEvent.change(screen.getByLabelText('Link'), { target: { value: 'webcal://outlook.office365.com/owa/calendar/x/reachcalendar.ics' } });
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Work' } });
    fireEvent.click(screen.getByRole('button', { name: 'Subscribe' }));
    expect((await screen.findByRole('alert')).textContent).toBe('The link did not return a calendar');
    const [url, init] = post.mock.calls[0];
    expect(url).toBe('/api/subscriptions');
    expect(init?.method).toBe('POST');
    expect(JSON.parse(String(init?.body))).toEqual({ url: 'webcal://outlook.office365.com/owa/calendar/x/reachcalendar.ics', name: 'Work', color: unused(cals) });
    expect(onChanged).not.toHaveBeenCalled();
    expect((screen.getByRole('button', { name: 'Subscribe' }) as HTMLButtonElement).disabled).toBe(false);
  });

  const sub: CalendarInfo = {
    id: 'cal_s', name: 'Work', color: '', description: '', kind: 'personal', role: 'owner', dav_path: '', components: ['VEVENT'],
    subscription: { host: 'outlook.office365.com', refreshed_at: new Date(Date.now() - 2 * 3_600_000).toISOString(), checked_at: null, error: 'The link returned 404' },
  };

  it('shows a subscribed calendar with its host, update time, error and actions', async () => {
    const calls: string[] = [];
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (i, init) => {
      calls.push(`${init?.method} ${String(i)}`);
      return new Response(JSON.stringify(sub), { status: 200 });
    });
    const onChanged = vi.fn();
    render(<CalendarSidebar calendars={[sub, { ...sub, id: 'cal_n', name: 'New', subscription: { host: 'x.example', refreshed_at: null, checked_at: null } }]} hidden={new Set()} onToggle={vi.fn()} onChanged={onChanged} />);
    expect(screen.getByRole('alert').textContent).toBe('The link returned 404');
    expect(screen.queryByText('Subscribed · outlook.office365.com')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Refresh Work' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Delete Work' })).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Edit New' }));
    expect(screen.getByText('Not yet updated')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Edit Work' }));
    expect(screen.queryByText('Not yet updated')).toBeNull();
    expect(screen.getByText('Subscribed · outlook.office365.com')).toBeTruthy();
    expect(screen.getByText(`Updated ${rel(-2, 'hour')}`)).toBeTruthy();
    expect(screen.getAllByRole('alert').map((a) => a.textContent)).toEqual(['The link returned 404']);
    fireEvent.click(screen.getByRole('button', { name: 'Refresh Work' }));
    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByRole('button', { name: 'Change link of Work' }));
    fireEvent.change(screen.getByLabelText('Link'), { target: { value: 'https://outlook.office365.com/new.ics' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save link' }));
    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(2));
    expect(calls).toEqual(['POST /api/calendars/cal_s/refresh', 'PATCH /api/subscriptions/cal_s']);
  });

  it('closes an open link form with the edit panel', () => {
    render(<CalendarSidebar calendars={[{ ...sub, subscription: { ...sub.subscription!, error: undefined } }]} hidden={new Set()} onToggle={vi.fn()} onChanged={vi.fn()} />);
    fireEvent.click(screen.getByRole('button', { name: 'Edit Work' }));
    fireEvent.click(screen.getByRole('button', { name: 'Change link of Work' }));
    expect(screen.getByLabelText('Link')).toBeTruthy();
    fireEvent.click(screen.getAllByRole('button', { name: 'Cancel' }).at(-1)!);
    expect(screen.queryByLabelText('Link')).toBeNull();
    expect(screen.queryByText('Subscribed · outlook.office365.com')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Edit Work' }));
    expect(screen.queryByLabelText('Link')).toBeNull();
    expect(screen.getByRole('button', { name: 'Change link of Work' })).toBeTruthy();
  });

  it('explains a refresh refused as too soon', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({ error: 'Refreshed less than a minute ago' }), { status: 429 }));
    const onChanged = vi.fn();
    render(<CalendarSidebar calendars={[{ ...sub, subscription: { ...sub.subscription!, error: undefined } }]} hidden={new Set()} onToggle={vi.fn()} onChanged={onChanged} />);
    fireEvent.click(screen.getByRole('button', { name: 'Edit Work' }));
    fireEvent.click(screen.getByRole('button', { name: 'Refresh Work' }));
    expect((await screen.findByRole('alert')).textContent).toBe('Work was refreshed less than a minute ago; try again shortly.');
    expect(onChanged).not.toHaveBeenCalled();
  });

  it('formats update times relative to now', () => {
    const now = Date.parse('2026-10-15T12:00:00Z');
    expect(ago('2026-10-15T11:59:30Z', now)).toBe('just now');
    expect(ago('2026-10-15T11:55:00Z', now)).toBe(rel(-5, 'minute'));
    expect(ago('2026-10-14T12:00:00Z', now)).toBe(rel(-1, 'day'));
  });

  const csv = () => new File(['Subject,Start Date\nX,10/29/2026\n'], 'Work trips.csv', { type: 'text/csv' });
  const openImport = () => {
    fireEvent.click(screen.getByRole('button', { name: 'Add a calendar' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Import a CSV file' }));
    return screen.getByRole('dialog', { name: 'Import a CSV file' });
  };

  it('imports a CSV into an existing calendar and reports the rows', async () => {
    const calls: { url: string; init?: RequestInit }[] = [];
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (i, init) => {
      calls.push({ url: String(i), init });
      return new Response(JSON.stringify({ imported: 2, duplicates: 1, skipped: [{ row: 4, reason: 'Start Date is missing or not a date' }], skipped_count: 3 }), { status: 200 });
    });
    const onChanged = vi.fn();
    render(<CalendarSidebar calendars={cals} hidden={new Set()} onToggle={vi.fn()} onChanged={onChanged} />);
    const plus = screen.getByRole('button', { name: 'Add a calendar' });
    openImport();
    // Readers cannot be imported into; a new calendar always can.
    const options = Array.from((screen.getByLabelText('Add to') as HTMLSelectElement).options).map((o) => o.textContent);
    expect(options).toEqual(['Mine', 'Team', 'New calendar…']);
    const file = csv();
    fireEvent.change(screen.getByLabelText('File'), { target: { files: [file] } });
    fireEvent.click(screen.getByRole('button', { name: 'Import' }));
    const status = await screen.findByRole('status');
    expect(status.textContent).toContain('Imported 2 events. 1 already there. 3 rows skipped.');
    expect(status.textContent).toContain('Row 4: Start Date is missing or not a date');
    expect(status.textContent).toContain('…and 2 more');
    expect(onChanged).toHaveBeenCalledTimes(1);
    expect(calls).toHaveLength(1);
    const url = new URL(calls[0].url, 'http://x');
    expect(url.pathname).toBe('/api/calendars/cal_p/import');
    expect(url.searchParams.get('tz')).toBe(browserZone());
    expect(url.searchParams.get('date_order')).toBe(browserDateOrder());
    expect(calls[0].init?.method).toBe('POST');
    expect(calls[0].init?.body).toBe(file);
    fireEvent.click(screen.getByRole('button', { name: 'Done' }));
    expect(screen.queryByRole('dialog')).toBeNull();
    await waitFor(() => expect(document.activeElement).toBe(plus));
  });

  it('renders server text in the result as text, and the stop as an alert', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({
      imported: 1, duplicates: 0, skipped: [{ row: 2, reason: '<img src=x onerror=alert(1)>' }], skipped_count: 1, stopped: 'Stopped at row 3: Calendar storage is full',
    }), { status: 200 }));
    render(<CalendarSidebar calendars={cals} hidden={new Set()} onToggle={vi.fn()} onChanged={vi.fn()} />);
    const dialog = openImport();
    fireEvent.change(within(dialog).getByLabelText('File'), { target: { files: [csv()] } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Import' }));
    expect((await within(dialog).findByRole('alert')).textContent).toBe('Stopped at row 3: Calendar storage is full');
    expect(within(dialog).getByRole('status').textContent).toContain('Row 2: <img src=x onerror=alert(1)>');
    expect(dialog.querySelector('img')).toBeNull();
  });

  it('refuses a file over the server cap before creating anything', () => {
    const fetch = vi.spyOn(globalThis, 'fetch');
    render(<CalendarSidebar calendars={cals} hidden={new Set()} onToggle={vi.fn()} onChanged={vi.fn()} />);
    const dialog = openImport();
    const big = new File([new Uint8Array((1 << 20) + 1)], 'big.csv', { type: 'text/csv' });
    fireEvent.change(within(dialog).getByLabelText('File'), { target: { files: [big] } });
    fireEvent.change(within(dialog).getByLabelText('Add to'), { target: { value: 'new' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Import' }));
    expect(within(dialog).getByRole('alert').textContent).toBe('The file is too large; the limit is 1 MiB.');
    expect(fetch).not.toHaveBeenCalled();
  });

  it('deletes a new calendar again when its import fails', async () => {
    const calls: string[] = [];
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (i, init) => {
      const call = `${init?.method ?? 'GET'} ${String(i).split('?')[0]}`;
      calls.push(call);
      if (call === 'POST /api/calendars') return new Response(JSON.stringify({ ...cals[0], id: 'cal_new', name: 'Work trips' }), { status: 201 });
      if (call === 'DELETE /api/calendars/cal_new') return new Response(null, { status: 204 });
      return new Response(JSON.stringify({ error: 'This is not an Outlook or Google Calendar CSV' }), { status: 422 });
    });
    const onChanged = vi.fn();
    render(<CalendarSidebar calendars={cals} hidden={new Set()} onToggle={vi.fn()} onChanged={onChanged} />);
    openImport();
    fireEvent.change(screen.getByLabelText('File'), { target: { files: [csv()] } });
    fireEvent.change(screen.getByLabelText('Add to'), { target: { value: 'new' } });
    expect((screen.getByLabelText('Name') as HTMLInputElement).value).toBe('Work trips');
    fireEvent.click(screen.getByRole('button', { name: 'Import' }));
    expect((await screen.findByRole('alert')).textContent).toBe('This is not an Outlook or Google Calendar CSV');
    expect(calls).toEqual(['POST /api/calendars', 'POST /api/calendars/cal_new/import', 'DELETE /api/calendars/cal_new']);
    expect(onChanged).not.toHaveBeenCalled();
  });

  it('keeps a new calendar when the import may have finished (no answer or a 5xx)', async () => {
    for (const fail of [() => Promise.reject(new TypeError('Failed to fetch')), () => Promise.resolve(new Response('Bad gateway', { status: 502 }))]) {
      const calls: string[] = [];
      vi.spyOn(globalThis, 'fetch').mockImplementation(async (i, init) => {
        const call = `${init?.method ?? 'GET'} ${String(i).split('?')[0]}`;
        calls.push(call);
        if (call === 'POST /api/calendars') return new Response(JSON.stringify({ ...cals[0], id: 'cal_new', name: 'Work trips' }), { status: 201 });
        return fail();
      });
      const onChanged = vi.fn();
      render(<CalendarSidebar calendars={cals} hidden={new Set()} onToggle={vi.fn()} onChanged={onChanged} />);
      openImport();
      fireEvent.change(screen.getByLabelText('File'), { target: { files: [csv()] } });
      fireEvent.change(screen.getByLabelText('Add to'), { target: { value: 'new' } });
      fireEvent.click(screen.getByRole('button', { name: 'Import' }));
      expect((await screen.findByRole('alert')).textContent).toMatch(/may have finished.*Work trips/);
      expect(calls).toEqual(['POST /api/calendars', 'POST /api/calendars/cal_new/import']);
      expect(onChanged).toHaveBeenCalled();
      cleanup();
      vi.restoreAllMocks();
    }
  });

  it('reads the date order from the locale', () => {
    expect(browserDateOrder('en-US')).toBe('mdy');
    expect(browserDateOrder('en-GB')).toBe('dmy');
    expect(browserDateOrder('de-DE')).toBe('dmy');
  });
});
