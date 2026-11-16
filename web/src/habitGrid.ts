import { dateOnly } from './taskParse';
import type { Habit } from './tasksApi';

// A habit grid cell: done, missed, the current day or week while still open ("now", not yet a
// miss), a day before the habit began, or one still to come.
export type HabitState = 'done' | 'missed' | 'now' | 'before' | 'future';
export interface HabitCell { key: string; start: Date; state: HabitState }

const DAY = 86400000;

/**
 * habitGrid lays out a habit from the week it began (the earlier of its creation and its first
 * completion), at most 12 weeks, ending with the week of `now`: one cell per day, or per week
 * for a weekly habit. The rate counts only ended periods and done ones.
 */
export function habitGrid(h: Habit, created: string | undefined, now: Date) {
  const done = new Set(h.days);
  const today = dateOnly(now);
  const mondayOf = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate() - ((d.getDay() + 6) % 7));
  const monday = mondayOf(now);
  const began = [created ? dateOnly(new Date(created)) : today, ...h.days].sort()[0];
  const weeks = Math.min(12, Math.max(1, Math.round((monday.getTime() - mondayOf(new Date(`${began}T12:00`)).getTime()) / (7 * DAY)) + 1));
  const first = new Date(monday.getFullYear(), monday.getMonth(), monday.getDate() - 7 * (weeks - 1));
  const days = Array.from({ length: weeks * 7 }, (_, i) => new Date(first.getFullYear(), first.getMonth(), first.getDate() + i));
  const weekly = h.freq === 'weekly';
  const state = (keys: string[]): HabitState => {
    if (keys.some((k) => done.has(k))) return 'done';
    if (keys[0] > today) return 'future';
    if (keys[keys.length - 1] < began) return 'before';
    return keys[keys.length - 1] >= today ? 'now' : 'missed';
  };
  const cells: HabitCell[] = weekly
    ? Array.from({ length: weeks }, (_, w) => { const span = days.slice(w * 7, w * 7 + 7); const keys = span.map(dateOnly); return { key: keys[0], start: span[0], state: state(keys) }; })
    : days.map((d) => { const k = dateOnly(d); return { key: k, start: d, state: state([k]) }; });
  const counted = cells.filter((c) => c.state === 'done' || c.state === 'missed');
  const hits = counted.filter((c) => c.state === 'done').length;
  const thisWeek = days.slice(-7).map(dateOnly);
  return {
    weeks, first, cells, weekly, hits, counted: counted.length,
    rate: counted.length ? Math.round((hits / counted.length) * 100) : 0,
    week: weekly ? (thisWeek.some((k) => done.has(k)) ? 'Done' : 'Not yet') : `${thisWeek.filter((k) => done.has(k)).length} of ${thisWeek.filter((k) => k <= today).length}`,
  };
}
