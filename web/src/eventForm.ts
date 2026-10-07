import type { EventBody, EventInfo, Freq } from './calendarApi';

export const WEEKDAYS = ['MO', 'TU', 'WE', 'TH', 'FR', 'SA', 'SU'];

export interface FormState {
  title: string; location: string; description: string; allDay: boolean;
  start: string; end: string; // 'YYYY-MM-DDTHH:mm' local, or 'YYYY-MM-DD' (all-day, end inclusive)
  calendarId: string; freq: Freq; weekdays: string[];
}

const pad = (n: number) => String(n).padStart(2, '0');
export const localDate = (d: Date) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
export const localDateTime = (d: Date) => `${localDate(d)}T${pad(d.getHours())}:${pad(d.getMinutes())}`;

// parseLocal reads a form value in the browser's zone.
export function parseLocal(s: string): Date {
  const [date, time = '00:00'] = s.split('T');
  const [y, m, d] = date.split('-').map(Number);
  const [h, mi] = time.split(':').map(Number);
  const t = new Date(y, m - 1, d, h, mi);
  t.setFullYear(y, m - 1, d); // the constructor maps years 0-99 to 1900-1999
  return t;
}

export const addDays = (d: Date, n: number) => new Date(d.getFullYear(), d.getMonth(), d.getDate() + n);

export function validZone(zone: string | undefined): boolean {
  if (!zone) return false;
  try {
    new Intl.DateTimeFormat('en', { timeZone: zone });
    return zone.includes('/') || zone === 'UTC';
  } catch {
    return false;
  }
}

// emptyForm starts a new event; an all-day end arrives exclusive (as FullCalendar gives it).
export function emptyForm(start: Date, end: Date, allDay: boolean, calendarId: string): FormState {
  return {
    title: '', location: '', description: '', allDay, calendarId, freq: '', weekdays: [],
    start: allDay ? localDate(start) : localDateTime(start),
    end: allDay ? localDate(addDays(end, -1)) : localDateTime(end),
  };
}

// formFromEvent fills the form for one occurrence ('this') or the whole series ('all').
// An object with only overrides has no master, so empty series times fall back to the occurrence.
export function formFromEvent(ev: EventInfo, scope: 'this' | 'all'): FormState {
  const s = scope === 'all' && ev.series_start ? ev.series_start : ev.start;
  const e = scope === 'all' && ev.series_end ? ev.series_end : ev.end;
  const start = ev.all_day ? parseLocal(s) : new Date(s);
  const end = ev.all_day ? parseLocal(e) : new Date(e);
  return {
    title: ev.title, location: ev.location ?? '', description: ev.description ?? '', allDay: ev.all_day,
    calendarId: ev.calendar_id, freq: ev.repeat.freq, weekdays: ev.repeat.weekdays ?? [],
    start: ev.all_day ? localDate(start) : localDateTime(start),
    end: ev.all_day ? localDate(addDays(end, -1)) : localDateTime(end),
  };
}

// bodyFromForm builds the API body, or returns a message for the user.
export function bodyFromForm(f: FormState, zone: string, scope?: 'all' | 'this', recurrenceId?: string): EventBody | string {
  const start = parseLocal(f.start);
  const end = parseLocal(f.end);
  if (Number.isNaN(start.getTime()) || Number.isNaN(end.getTime())) return 'Enter a start and end';
  if (end < start) return 'End must not be before start';
  const repeat = f.freq === 'weekly' && f.weekdays.length ? { freq: f.freq, weekdays: f.weekdays } : { freq: f.freq };
  const body: EventBody = { title: f.title, location: f.location, description: f.description, all_day: f.allDay, repeat, start: '', end: '' };
  if (f.allDay) {
    body.start = localDate(start);
    body.end = localDate(addDays(end, 1));
  } else {
    body.start = start.toISOString();
    body.end = end.toISOString();
    body.zone = zone;
  }
  if (scope) body.scope = scope;
  if (scope === 'this' && recurrenceId) body.recurrence_id = recurrenceId;
  return body;
}
