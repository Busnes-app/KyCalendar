import { describe as group, expect, it } from 'vitest';
import { formatDate } from '@fullcalendar/core';
import { buildRule, describe, dueLabel, nthOf, readRule } from './taskRepeat';

// Tuesday 10 November 2026: the second Tuesday.
const tue = new Date(2026, 10, 10);
const lastFri = new Date(2026, 10, 27);

group('repeat presets', () => {
  it('builds every tududi pattern', () => {
    expect(buildRule({ preset: 'daily', interval: 1 }, tue)).toBe('FREQ=DAILY');
    expect(buildRule({ preset: 'weekdays', interval: 1 }, tue)).toBe('FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR');
    expect(buildRule({ preset: 'weekly', interval: 2 }, tue)).toBe('FREQ=WEEKLY;BYDAY=TU;INTERVAL=2');
    expect(buildRule({ preset: 'monthly', interval: 1 }, tue)).toBe('FREQ=MONTHLY;BYMONTHDAY=10');
    expect(buildRule({ preset: 'monthly-nth', interval: 1 }, tue)).toBe('FREQ=MONTHLY;BYDAY=2TU');
    expect(buildRule({ preset: 'monthly-nth', interval: 1 }, lastFri)).toBe('FREQ=MONTHLY;BYDAY=-1FR');
    expect(buildRule({ preset: 'monthly-last', interval: 1, until: '2027-06-30' }, tue)).toBe('FREQ=MONTHLY;BYMONTHDAY=-1;UNTIL=20270630');
    expect(buildRule({ preset: 'yearly', interval: 1, count: 5 }, tue)).toBe('FREQ=YEARLY;COUNT=5');
    expect(buildRule({ preset: 'none', interval: 1 }, tue)).toBe('');
  });
  it('reads them back, and keeps anything else as custom', () => {
    for (const p of ['daily', 'weekdays', 'weekly', 'monthly', 'monthly-nth', 'monthly-last', 'yearly'] as const) {
      expect(readRule(buildRule({ preset: p, interval: 1 }, tue)).preset).toBe(p);
    }
    expect(readRule('FREQ=WEEKLY;INTERVAL=3;COUNT=4')).toMatchObject({ preset: 'weekly', interval: 3, count: 4 });
    const custom = readRule('FREQ=MONTHLY;BYDAY=MO;BYSETPOS=1');
    expect(custom.preset).toBe('custom');
    expect(buildRule(custom, tue)).toBe('FREQ=MONTHLY;BYDAY=MO;BYSETPOS=1');
  });
  it('names the nth weekday', () => {
    expect(nthOf(tue)).toBe(2);
    expect(nthOf(lastFri)).toBe(-1);
    expect(describe('FREQ=MONTHLY;BYDAY=2TU', tue)).toBe('Monthly on the second Tuesday');
    expect(describe('FREQ=WEEKLY;BYDAY=TU;INTERVAL=2', tue)).toBe('Every 2 weeks');
  });
});

group('dueLabel', () => {
  const now = new Date(2026, 10, 15, 10, 0);
  it('is relative and flags late tasks', () => {
    expect(dueLabel('2026-11-15', now)).toMatchObject({ text: 'Today', overdue: false, today: true });
    expect(dueLabel('2026-11-16', now)).toMatchObject({ text: 'Tomorrow', overdue: false });
    expect(dueLabel('2026-11-13', now)).toMatchObject({ text: '2 days ago', overdue: true });
    expect(dueLabel(new Date(2026, 10, 15, 9, 0).toISOString(), now)?.overdue).toBe(true);
    expect(dueLabel(undefined, now)).toBeNull();
  });
});

group('one time format', () => {
  it('writes times exactly as the calendar\'s FullCalendar formatter does', () => {
    const at = new Date(2026, 10, 16, 15, 30);
    const fc = formatDate(at, { hour: 'numeric', minute: '2-digit', meridiem: 'short' });
    expect(dueLabel(at.toISOString(), new Date(2026, 10, 16, 9, 0))?.text).toBe(`Today ${fc}`);
  });
});
