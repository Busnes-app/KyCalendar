import { clockOf } from './taskRepeat';

// Quick add: parses "Call Ana tomorrow 3pm !high #work @Errands every 2 weeks" into fields.
// Pure: the preview shows exactly what Save sends. Words that parse as nothing stay in the title,
// and text that would leave no title is all title.

export interface ListRef { id: string; name: string }
export interface Parsed {
  title: string;
  due?: string; // YYYY-MM-DD, or RFC 3339 with the local offset
  priority?: number; // 1, 5, 9
  tags: string[];
  list?: ListRef;
  rrule?: string;
  chips: string[]; // what was understood, in words
}

const DAYS = ['sun', 'mon', 'tue', 'wed', 'thu', 'fri', 'sat'];
const DAY_CODES = ['SU', 'MO', 'TU', 'WE', 'TH', 'FR', 'SA'];
const MONTHS = ['jan', 'feb', 'mar', 'apr', 'may', 'jun', 'jul', 'aug', 'sep', 'oct', 'nov', 'dec'];
const UNITS: Record<string, string> = { day: 'DAILY', week: 'WEEKLY', month: 'MONTHLY', year: 'YEARLY' };

const pad = (n: number) => String(n).padStart(2, '0');
export const dateOnly = (d: Date) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
export function localRFC3339(d: Date): string {
  const off = -d.getTimezoneOffset();
  const sign = off >= 0 ? '+' : '-';
  return `${dateOnly(d)}T${pad(d.getHours())}:${pad(d.getMinutes())}:00${sign}${pad(Math.floor(Math.abs(off) / 60))}:${pad(Math.abs(off) % 60)}`;
}
const addDays = (d: Date, n: number) => new Date(d.getFullYear(), d.getMonth(), d.getDate() + n);

function dayIndex(w: string): number {
  const i = DAYS.indexOf(w.slice(0, 3));
  // "mon", "monday", "mondays"; not "month" or "sunny"
  return i >= 0 && /^(sun|mon|tue|tues|wed|thu|thur|thurs|fri|sat)(day|days|s|nesday|urday|sday|rsday)?$/.test(w) ? i : -1;
}
const unitOf = (w: string) => UNITS[w.replace(/s$/, '')];

function parseTime(words: string[], i: number): { h: number; m: number; used: number } | null {
  const w = words[i];
  if (w === 'noon') return { h: 12, m: 0, used: 1 };
  if (w === 'midnight') return { h: 0, m: 0, used: 1 };
  let m = /^(\d{1,2})(?::(\d{2}))?(am|pm)?$/.exec(w);
  if (!m) return null;
  let used = 1;
  let ampm = m[3];
  if (!ampm && (words[i + 1] === 'am' || words[i + 1] === 'pm')) { ampm = words[i + 1]; used = 2; }
  if (!ampm && !m[2]) return null; // a bare number is not a time
  let h = Number(m[1]);
  const min = Number(m[2] ?? 0);
  if (min > 59 || (ampm ? h < 1 || h > 12 : h > 23)) return null;
  if (ampm === 'pm' && h < 12) h += 12;
  if (ampm === 'am' && h === 12) h = 0;
  return { h, m: min, used };
}

function parseDate(words: string[], i: number, today: Date): { d: Date; used: number } | null {
  const w = words[i];
  const next = words[i + 1] ?? '';
  if (w === 'today' || w === 'tonight') return { d: today, used: 1 };
  if (w === 'tomorrow' || w === 'tmrw' || w === 'tmr') return { d: addDays(today, 1), used: 1 };
  if ((w === 'next' || w === 'this') && next === 'week') return { d: addDays(today, ((8 - today.getDay()) % 7) || 7), used: 2 };
  if (w === 'next' && next === 'month') return { d: new Date(today.getFullYear(), today.getMonth() + 1, 1), used: 2 };
  const wanted = (w === 'next' || w === 'this' ? dayIndex(next) : dayIndex(w));
  if (wanted >= 0) {
    let ahead = (wanted - today.getDay() + 7) % 7;
    if (w === 'next') ahead += ahead === 0 ? 7 : 7; // the one in the following week
    return { d: addDays(today, ahead), used: w === 'next' || w === 'this' ? 2 : 1 };
  }
  if (w === 'in' && /^\d+$/.test(next) && unitOf(words[i + 2] ?? '')) {
    const n = Number(next);
    const unit = unitOf(words[i + 2]);
    const d = unit === 'DAILY' ? addDays(today, n) : unit === 'WEEKLY' ? addDays(today, 7 * n)
      : unit === 'MONTHLY' ? new Date(today.getFullYear(), today.getMonth() + n, today.getDate()) : new Date(today.getFullYear() + n, today.getMonth(), today.getDate());
    return n > 0 && n < 1000 ? { d, used: 3 } : null;
  }
  let m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(w);
  if (m) {
    const d = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]));
    return d.getMonth() === Number(m[2]) - 1 ? { d, used: 1 } : null;
  }
  const monthAt = (s: string) => (s.length >= 3 ? MONTHS.indexOf(s.slice(0, 3)) : -1);
  const dayNum = (s: string) => (/^\d{1,2}(st|nd|rd|th)?$/.test(s) ? parseInt(s, 10) : 0);
  let mon = monthAt(w), day = dayNum(next);
  if (mon < 0 || !day) { mon = monthAt(next); day = dayNum(w); }
  if (mon >= 0 && day >= 1 && day <= 31 && /^[a-z]+$/.test(words[i + (dayNum(w) ? 1 : 0)])) {
    let d = new Date(today.getFullYear(), mon, day);
    if (d.getMonth() !== mon) return null;
    if (d < today) d = new Date(today.getFullYear() + 1, mon, day);
    return { d, used: 2 };
  }
  return null;
}

function parseRepeat(words: string[], i: number): { rrule: string; label: string; used: number; byday?: number[] } | null {
  const w = words[i];
  const simple: Record<string, [string, string]> = { daily: ['FREQ=DAILY', 'Every day'], weekly: ['FREQ=WEEKLY', 'Every week'], monthly: ['FREQ=MONTHLY', 'Every month'], yearly: ['FREQ=YEARLY', 'Every year'], annually: ['FREQ=YEARLY', 'Every year'] };
  if (simple[w]) return { rrule: simple[w][0], label: simple[w][1], used: 1 };
  if (w === 'weekdays') return { rrule: 'FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR', label: 'Every weekday', used: 1, byday: [1, 2, 3, 4, 5] };
  if (w !== 'every') return null;
  const a = words[i + 1] ?? '', b = words[i + 2] ?? '';
  if (a === 'weekday') return { rrule: 'FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR', label: 'Every weekday', used: 2, byday: [1, 2, 3, 4, 5] };
  if (unitOf(a)) return { rrule: `FREQ=${unitOf(a)}`, label: `Every ${a.replace(/s$/, '')}`, used: 2 };
  if (a === 'other' && unitOf(b)) return { rrule: `FREQ=${unitOf(b)};INTERVAL=2`, label: `Every 2 ${b.replace(/s$/, '')}s`, used: 3 };
  if (/^\d+$/.test(a) && unitOf(b) && Number(a) >= 1 && Number(a) <= 999) {
    const n = Number(a);
    return { rrule: n === 1 ? `FREQ=${unitOf(b)}` : `FREQ=${unitOf(b)};INTERVAL=${n}`, label: n === 1 ? `Every ${b.replace(/s$/, '')}` : `Every ${n} ${b.replace(/s$/, '')}s`, used: 3 };
  }
  const days: number[] = [];
  let j = i + 1;
  while (j < words.length) {
    const d = dayIndex(words[j].replace(/,$/, ''));
    if (d < 0) { if (words[j] === 'and' && days.length) { j++; continue; } break; }
    if (!days.includes(d)) days.push(d);
    j++;
  }
  if (days.length) {
    days.sort((x, y) => ((x + 6) % 7) - ((y + 6) % 7));
    const names = days.map((d) => ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'][d]);
    return { rrule: `FREQ=WEEKLY;BYDAY=${days.map((d) => DAY_CODES[d]).join(',')}`, label: `Every ${names.join(', ')}`, used: j - i, byday: days };
  }
  return null;
}

const PRIORITIES: Record<string, [number, string]> = {
  '!high': [1, 'High priority'], '!h': [1, 'High priority'], '!1': [1, 'High priority'], '!!!': [1, 'High priority'],
  '!medium': [5, 'Medium priority'], '!med': [5, 'Medium priority'], '!m': [5, 'Medium priority'], '!2': [5, 'Medium priority'], '!!': [5, 'Medium priority'],
  '!low': [9, 'Low priority'], '!l': [9, 'Low priority'], '!3': [9, 'Low priority'],
};
const FILLER = new Set(['on', 'at', 'by', 'due']);

export function parseQuick(text: string, lists: ListRef[], now = new Date()): Parsed {
  const raw = text.trim().split(/\s+/).filter(Boolean);
  const words = raw.map((w) => w.toLowerCase());
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const out: Parsed = { title: '', tags: [], chips: [] };
  const keep: string[] = [];
  let date: Date | undefined;
  let time: { h: number; m: number } | undefined;
  let byday: number[] | undefined;
  for (let i = 0; i < words.length;) {
    const w = words[i];
    if (PRIORITIES[w] && out.priority === undefined) { out.priority = PRIORITIES[w][0]; i++; continue; }
    if (/^#[\p{L}\p{N}_-]{1,64}$/u.test(raw[i]) && out.tags.length < 20) {
      const tag = raw[i].slice(1);
      if (!out.tags.some((t) => t.toLowerCase() === tag.toLowerCase())) out.tags.push(tag);
      i++; continue;
    }
    if (/^@\S+$/.test(raw[i]) && !out.list) {
      const want = w.slice(1).replace(/[-_]/g, ' ');
      const flat = (s: string) => s.toLowerCase().replace(/\s+/g, ' ');
      const exact = lists.filter((l) => flat(l.name) === want || flat(l.name).replace(/ /g, '') === want.replace(/ /g, ''));
      const prefix = lists.filter((l) => flat(l.name).startsWith(want));
      const hit = exact.length === 1 ? exact[0] : prefix.length === 1 ? prefix[0] : undefined;
      if (hit) { out.list = hit; i++; continue; }
    }
    const filler = FILLER.has(w) ? 1 : 0;
    const rep = !out.rrule ? parseRepeat(words, i) : null;
    if (rep) { out.rrule = rep.rrule; byday = rep.byday; out.chips.push(rep.label); i += rep.used; continue; }
    const d = !date ? parseDate(words, i + filler, today) : null;
    if (d) { date = d.d; i += filler + d.used; continue; }
    const t = !time ? parseTime(words, i + filler) : null;
    if (t) { time = { h: t.h, m: t.m }; i += filler + t.used; continue; }
    keep.push(raw[i]);
    i++;
  }
  if (!date && (time || out.rrule)) {
    date = today;
    if (byday?.length) date = addDays(today, Math.min(...byday.map((d) => (d - today.getDay() + 7) % 7)));
  }
  if (date) {
    if (time) {
      const at = new Date(date.getFullYear(), date.getMonth(), date.getDate(), time.h, time.m);
      out.due = localRFC3339(at);
      out.chips.unshift(`Due ${at.toLocaleDateString(undefined, { weekday: 'short', day: 'numeric', month: 'short' })}, ${clockOf(at)}`);
    } else {
      out.due = dateOnly(date);
      out.chips.unshift(`Due ${date.toLocaleDateString(undefined, { weekday: 'short', day: 'numeric', month: 'short' })}`);
    }
  }
  if (out.priority) out.chips.push(PRIORITIES[Object.keys(PRIORITIES).find((k) => PRIORITIES[k][0] === out.priority)!][1]);
  for (const t of out.tags) out.chips.push(`#${t}`);
  if (out.list) out.chips.push(`In ${out.list.name}`);
  out.title = keep.join(' ');
  if (!out.title) return { title: text.trim(), tags: [], chips: [] }; // nothing left to call it: all title
  return out;
}
