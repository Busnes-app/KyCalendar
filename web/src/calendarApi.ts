import { secureFetch } from './api';

export interface CalendarInfo {
  id: string; name: string; color: string; description: string;
  kind: 'personal' | 'group'; role: 'owner' | 'manager' | 'editor' | 'reader'; dav_path: string;
}
export type Freq = '' | 'daily' | 'weekly' | 'monthly' | 'yearly' | 'custom';
export interface RepeatInfo { freq: Freq; weekdays?: string[] }
export interface EventInfo {
  calendar_id: string; uid: string; recurrence_id?: string; etag: string; title: string;
  location?: string; description?: string; start: string; end: string; series_start: string; series_end: string;
  all_day: boolean; recurring: boolean; override?: boolean; repeat: RepeatInfo; floating?: boolean;
  unknown_zone?: boolean; partial?: boolean; zone?: string; editable: boolean;
}
export interface EventBody {
  title: string; location: string; description: string; start: string; end: string; all_day: boolean;
  zone?: string; repeat: RepeatInfo; scope?: 'all' | 'this'; recurrence_id?: string;
}

export class ApiError extends Error {
  constructor(public status: number, public code: string, message: string) {
    super(message);
  }
}

const JSON_HEADERS = { 'Content-Type': 'application/json' };

// Resolves to null on a network error so every caller handles one failure path.
const send = (url: string, init?: RequestInit) => secureFetch(url, init).catch(() => null);

async function parse<T>(res: Response | null): Promise<T> {
  if (!res) throw new ApiError(0, 'network', 'The server could not be reached');
  if (res.ok) return (res.status === 204 ? undefined : await res.json()) as T;
  const body = await res.json().catch(() => null);
  throw new ApiError(res.status, body?.code ?? '', body?.error ?? `Request failed (${res.status})`);
}

const eventPath = (ev: EventInfo) => `/api/events/${encodeURIComponent(ev.calendar_id)}/${encodeURIComponent(ev.uid)}`;
const ifMatch = (ev: EventInfo) => ({ 'If-Match': `"${ev.etag}"` });

export const listCalendars = () => send('/api/calendars').then((r) => parse<CalendarInfo[]>(r));
export const createCalendar = (b: { name: string; color?: string }) =>
  send('/api/calendars', { method: 'POST', headers: JSON_HEADERS, body: JSON.stringify(b) }).then((r) => parse<CalendarInfo>(r));
export const patchCalendar = (id: string, b: { name?: string; color?: string }) =>
  send(`/api/calendars/${encodeURIComponent(id)}`, { method: 'PATCH', headers: JSON_HEADERS, body: JSON.stringify(b) }).then((r) => parse<CalendarInfo>(r));
export const deleteCalendar = (id: string) =>
  send(`/api/calendars/${encodeURIComponent(id)}`, { method: 'DELETE' }).then((r) => parse<void>(r));

export function listEvents(start: Date, end: Date, tz: string, ids: string[]) {
  const q = new URLSearchParams({ start: start.toISOString(), end: end.toISOString(), tz });
  if (ids.length) q.set('calendar', ids.join(','));
  return send(`/api/events?${q}`).then((r) => parse<EventInfo[]>(r));
}
export const createEvent = (calendarId: string, b: EventBody) =>
  send(`/api/calendars/${encodeURIComponent(calendarId)}/events`, { method: 'POST', headers: JSON_HEADERS, body: JSON.stringify(b) })
    .then((r) => parse<{ uid: string; etag: string }>(r));
export const updateEvent = (ev: EventInfo, b: EventBody) =>
  send(eventPath(ev), { method: 'PUT', headers: { ...JSON_HEADERS, ...ifMatch(ev) }, body: JSON.stringify(b) })
    .then((r) => parse<{ etag: string }>(r));
export function deleteEvent(ev: EventInfo, scope: 'all' | 'this') {
  const q = new URLSearchParams({ scope });
  if (scope === 'this' && ev.recurrence_id) q.set('recurrence_id', ev.recurrence_id);
  return send(`${eventPath(ev)}?${q}`, { method: 'DELETE', headers: ifMatch(ev) }).then((r) => parse<void>(r));
}

export const browserZone = () => Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
