import { secureFetch } from './api';
import { ApiError } from './calendarApi';

// The tasks routes (internal/api/tasks.go). Times are YYYY-MM-DD (a day) or RFC 3339.
export type Status = 'NEEDS-ACTION' | 'IN-PROCESS' | 'COMPLETED' | 'CANCELLED';
export interface Reminder { before_min?: number; at?: string }
export interface FileLink { name: string; url: string; type?: string }
export interface Task {
  calendar_id: string; uid: string; etag: string; title: string; notes?: string; status: Status;
  completed_at?: string; percent?: number; priority?: number; due?: string; start?: string;
  duration_min?: number; estimate_min?: number; tags?: string[]; parent?: string; rrule?: string;
  repeat_from_completion?: boolean; habit?: boolean; reminders?: Reminder[]; url?: string; source?: string;
  files?: FileLink[]; created?: string; modified?: string; editable: boolean; subtasks?: number; subtasks_done?: number;
  images?: { name: string; type: string }[];
}
// A change: absent keeps the stored value; '' or [] removes it.
export interface TaskBody {
  title?: string; notes?: string; status?: Status; priority?: number; due?: string; start?: string; zone?: string;
  duration_min?: number; estimate_min?: number; tags?: string[]; parent?: string; rrule?: string;
  repeat_from_completion?: boolean; habit?: boolean; reminders?: Reminder[]; files?: FileLink[];
  image?: { name: string; data: string }; // create only: base64 PNG, JPEG or WebP, at most 512 KiB
}
export type PriorityName = 'high' | 'medium' | 'low' | 'none';
export type StatusName = 'needs-action' | 'in-process' | 'completed' | 'cancelled';
export interface TaskFilter {
  q?: string; lists?: string[]; area?: string; tags?: string[]; priority?: PriorityName[];
  due_from?: string; due_to?: string; status?: StatusName[]; source?: 'kypost';
  sort?: 'smart' | 'due' | 'priority' | 'created' | 'title'; layout?: 'list' | 'kanban';
}
export interface Area { id: string; name: string; lists: string[] }
export interface SavedView { id: string; name: string; query: TaskFilter }

const JSON_HEADERS = { 'Content-Type': 'application/json' };
const send = (url: string, init?: RequestInit) => secureFetch(url, init).catch(() => null);

async function parse<T>(res: Response | null): Promise<T> {
  if (!res) throw new ApiError(0, 'network', 'The server could not be reached');
  if (res.ok) return (res.status === 204 ? undefined : await res.json()) as T;
  const body = await res.json().catch(() => null);
  throw new ApiError(res.status, body?.code ?? '', body?.error ?? `Request failed (${res.status})`);
}

const zone = () => Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
const taskPath = (t: Pick<Task, 'calendar_id' | 'uid'>) => `/api/tasks/${encodeURIComponent(t.calendar_id)}/${encodeURIComponent(t.uid)}`;

export function filterQuery(f: TaskFilter): string {
  const q = new URLSearchParams({ tz: zone() });
  for (const [k, v] of Object.entries(f)) {
    if (k === 'layout' || v === undefined || v === '' || (Array.isArray(v) && v.length === 0)) continue;
    q.set(k, Array.isArray(v) ? v.join(',') : String(v));
  }
  return q.toString();
}

export const listTasks = (f: TaskFilter = {}) => send(`/api/tasks?${filterQuery(f)}`).then((r) => parse<{ tasks: Task[] }>(r)).then((b) => b.tasks ?? []);
export const createTask = (calendarId: string, b: TaskBody) =>
  send(`/api/calendars/${encodeURIComponent(calendarId)}/tasks`, { method: 'POST', headers: JSON_HEADERS, body: JSON.stringify({ zone: zone(), ...b }) })
    .then((r) => parse<{ calendar_id: string; uid: string; etag: string }>(r));
export const patchTask = (t: Pick<Task, 'calendar_id' | 'uid' | 'etag'>, b: TaskBody) =>
  send(`${taskPath(t)}?tz=${encodeURIComponent(zone())}`, { method: 'PATCH', headers: { ...JSON_HEADERS, 'If-Match': `"${t.etag}"` }, body: JSON.stringify(b) })
    .then((r) => parse<Task>(r));
export const deleteTask = (t: Pick<Task, 'calendar_id' | 'uid' | 'etag'>) =>
  send(taskPath(t), { method: 'DELETE', headers: { 'If-Match': `"${t.etag}"` } }).then((r) => parse<void>(r));
export const inbox = () => send('/api/tasks/inbox', { method: 'POST' }).then((r) => parse<{ calendar_id: string }>(r)).then((b) => b.calendar_id);
export const listAreas = () => send('/api/tasks/areas').then((r) => parse<Area[]>(r));
export const saveAreas = (a: Area[]) => send('/api/tasks/areas', { method: 'PUT', headers: JSON_HEADERS, body: JSON.stringify(a) }).then((r) => parse<Area[]>(r));
export const listViews = () => send('/api/tasks/views').then((r) => parse<SavedView[]>(r));
export const createView = (name: string, query: TaskFilter) =>
  send('/api/tasks/views', { method: 'POST', headers: JSON_HEADERS, body: JSON.stringify({ name, query }) }).then((r) => parse<SavedView>(r));
export const deleteView = (id: string) => send(`/api/tasks/views/${encodeURIComponent(id)}`, { method: 'DELETE' }).then((r) => parse<void>(r));

export const zoneName = zone;
export const imageURL = (t: Pick<Task, 'calendar_id' | 'uid'>, n: number) => `${taskPath(t)}/images/${n}`;
export const MAX_IMAGE_BYTES = 512 << 10;

export interface TaskStats { done_today: number; done_this_week: number; streak: number }
export const taskStats = () => send(`/api/tasks/stats?tz=${encodeURIComponent(zone())}`).then((r) => parse<TaskStats>(r));
