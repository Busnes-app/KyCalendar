// The repeat choices in the task form, as RFC 5545 RRULEs, and the words a task row shows.

// The one time format in Tasks, the calendar's: the browser's locale, hours and minutes. Ranges
// use Intl's range form ("12:30 – 1:15 PM"), with its en dash.
const TIME = new Intl.DateTimeFormat(undefined, { hour: 'numeric', minute: '2-digit' });
export const clockOf = (d: Date) => TIME.format(d);
export const rangeOf = (a: Date, b: Date) => TIME.formatRange(a, b);

export type Preset = 'none' | 'daily' | 'weekdays' | 'weekly' | 'monthly' | 'monthly-nth' | 'monthly-last' | 'yearly' | 'custom';
export interface Rule { preset: Preset; interval: number; until?: string; count?: number; raw?: string }

const CODES = ['SU', 'MO', 'TU', 'WE', 'TH', 'FR', 'SA'];
const DAY_NAMES = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];
const ORD = ['', 'first', 'second', 'third', 'fourth'];

// The nth weekday of anchor's month: 1..4, or -1 for the last one.
export function nthOf(anchor: Date): number {
  const n = Math.ceil(anchor.getDate() / 7);
  const sameNextWeek = new Date(anchor.getFullYear(), anchor.getMonth(), anchor.getDate() + 7);
  return sameNextWeek.getMonth() !== anchor.getMonth() ? -1 : n;
}

export function presetLabel(p: Preset, anchor: Date): string {
  const day = DAY_NAMES[anchor.getDay()];
  switch (p) {
    case 'none': return 'Does not repeat';
    case 'daily': return 'Daily';
    case 'weekdays': return 'Every weekday (Mon–Fri)';
    case 'weekly': return `Weekly on ${day}`;
    case 'monthly': return `Monthly on day ${anchor.getDate()}`;
    case 'monthly-nth': { const n = nthOf(anchor); return `Monthly on the ${n === -1 ? 'last' : ORD[n]} ${day}`; }
    case 'monthly-last': return 'Monthly on the last day';
    case 'yearly': return 'Yearly';
    case 'custom': return 'Custom (kept as set in your app)';
  }
}

function parts(rrule: string): Map<string, string> {
  return new Map(rrule.split(';').map((p) => { const [k, v = ''] = p.split('='); return [k.toUpperCase(), v.toUpperCase()] as [string, string]; }));
}

export function readRule(rrule: string | undefined): Rule {
  if (!rrule) return { preset: 'none', interval: 1 };
  const m = parts(rrule);
  const known = new Set(['FREQ', 'INTERVAL', 'UNTIL', 'COUNT', 'BYDAY', 'BYMONTHDAY', 'WKST']);
  const interval = Number(m.get('INTERVAL') ?? 1) || 1;
  const until = m.get('UNTIL');
  const r: Rule = { preset: 'custom', interval, raw: rrule };
  if (until && /^\d{8}/.test(until)) r.until = `${until.slice(0, 4)}-${until.slice(4, 6)}-${until.slice(6, 8)}`;
  if (m.has('COUNT')) r.count = Number(m.get('COUNT'));
  if ([...m.keys()].some((k) => !known.has(k))) return r;
  const freq = m.get('FREQ'), byday = m.get('BYDAY'), bymd = m.get('BYMONTHDAY');
  if (freq === 'DAILY' && !byday && !bymd) r.preset = 'daily';
  else if (freq === 'WEEKLY' && byday === 'MO,TU,WE,TH,FR' && interval === 1) r.preset = 'weekdays';
  else if (freq === 'WEEKLY' && (!byday || /^(SU|MO|TU|WE|TH|FR|SA)$/.test(byday)) && !bymd) r.preset = 'weekly';
  else if (freq === 'MONTHLY' && !byday && (!bymd || /^\d+$/.test(bymd))) r.preset = 'monthly';
  else if (freq === 'MONTHLY' && byday && /^(-1|[1-4])(SU|MO|TU|WE|TH|FR|SA)$/.test(byday) && !bymd) r.preset = 'monthly-nth';
  else if (freq === 'MONTHLY' && bymd === '-1' && !byday) r.preset = 'monthly-last';
  else if (freq === 'YEARLY' && !byday && !bymd) r.preset = 'yearly';
  if (r.preset !== 'custom') delete r.raw;
  return r;
}

// buildRule writes r for a task anchored (due, else start) on anchor; custom keeps its raw rule.
export function buildRule(r: Rule, anchor: Date): string {
  if (r.preset === 'none') return '';
  if (r.preset === 'custom') return r.raw ?? '';
  const code = CODES[anchor.getDay()];
  const base: Record<Exclude<Preset, 'none' | 'custom'>, string> = {
    daily: 'FREQ=DAILY',
    weekdays: 'FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR',
    weekly: `FREQ=WEEKLY;BYDAY=${code}`,
    monthly: `FREQ=MONTHLY;BYMONTHDAY=${anchor.getDate()}`,
    'monthly-nth': `FREQ=MONTHLY;BYDAY=${nthOf(anchor)}${code}`,
    'monthly-last': 'FREQ=MONTHLY;BYMONTHDAY=-1',
    yearly: 'FREQ=YEARLY',
  };
  let s = base[r.preset];
  if (r.interval > 1 && r.preset !== 'weekdays') s += `;INTERVAL=${Math.min(r.interval, 999)}`;
  if (r.until) s += `;UNTIL=${r.until.replace(/-/g, '')}`;
  else if (r.count) s += `;COUNT=${Math.min(r.count, 999)}`;
  return s;
}

// describe is the short text a task row and the detail show for a rule.
export function describe(rrule: string | undefined, anchor: Date): string {
  const r = readRule(rrule);
  if (r.preset === 'none') return '';
  if (r.preset === 'custom') return 'Repeats';
  const unit: Partial<Record<Preset, string>> = { daily: 'days', weekly: 'weeks', monthly: 'months', 'monthly-nth': 'months', 'monthly-last': 'months', yearly: 'years' };
  const head = r.interval > 1 && unit[r.preset] ? `Every ${r.interval} ${unit[r.preset]}` : presetLabel(r.preset, anchor);
  return head + (r.until ? `, until ${r.until}` : r.count ? `, ${r.count} times` : '');
}

// When: a task's due (or start) as words relative to now, and whether it is late.
export function dueLabel(due: string | undefined, now = new Date()): { text: string; overdue: boolean; today: boolean } | null {
  if (!due) return null;
  const allDay = due.length === 10;
  const at = allDay ? new Date(Number(due.slice(0, 4)), Number(due.slice(5, 7)) - 1, Number(due.slice(8, 10))) : new Date(due);
  if (Number.isNaN(at.getTime())) return null;
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const day = new Date(at.getFullYear(), at.getMonth(), at.getDate());
  const days = Math.round((day.getTime() - today.getTime()) / 86400000);
  const time = allDay ? '' : ` ${clockOf(at)}`;
  const overdue = allDay ? days < 0 : at.getTime() < now.getTime();
  let word: string;
  if (days === 0) word = 'Today';
  else if (days === 1) word = 'Tomorrow';
  else if (days === -1) word = 'Yesterday';
  else if (days > 1 && days < 7) word = at.toLocaleDateString(undefined, { weekday: 'long' });
  else if (days < 0 && days > -7) word = `${-days} days ago`;
  else word = at.toLocaleDateString(undefined, { day: 'numeric', month: 'short', ...(at.getFullYear() !== now.getFullYear() ? { year: 'numeric' } : {}) });
  return { text: word + time, overdue, today: days === 0 };
}

export function priorityName(p: number | undefined): 'High' | 'Medium' | 'Low' | '' {
  if (!p) return '';
  return p <= 4 ? 'High' : p === 5 ? 'Medium' : 'Low';
}
