import { describe, expect, it } from 'vitest';
import { bodyFromForm, emptyForm, formFromEvent, localDate, parseLocal, validZone } from './eventForm';
import type { EventInfo } from './calendarApi';

const base: EventInfo = {
  calendar_id: 'cal_1', uid: 'u1', recurrence_id: '20261012T070000Z', etag: 'e1', title: 'Standup',
  location: 'Room 1', description: 'd', start: '2026-10-12T09:00:00+02:00', end: '2026-10-12T10:00:00+02:00',
  series_start: '2026-10-05T09:00:00+02:00', series_end: '2026-10-05T10:00:00+02:00', all_day: false,
  recurring: true, repeat: { freq: 'weekly', weekdays: ['MO'] }, zone: 'Europe/Berlin', editable: true,
};

describe('eventForm', () => {
  it('edits all occurrences from the series start, one occurrence from its own', () => {
    const all = formFromEvent(base, 'all');
    const one = formFromEvent(base, 'this');
    expect(parseLocal(all.start).getTime()).toBe(new Date('2026-10-05T07:00:00Z').getTime());
    expect(parseLocal(one.start).getTime()).toBe(new Date('2026-10-12T07:00:00Z').getTime());
    expect(all.freq).toBe('weekly');
    expect(all.weekdays).toEqual(['MO']);
  });

  it('falls back to the occurrence times when there is no series master', () => {
    const f = formFromEvent({ ...base, series_start: '', series_end: '' }, 'all');
    expect(parseLocal(f.start).getTime()).toBe(new Date('2026-10-12T07:00:00Z').getTime());
    expect(parseLocal(f.end).getTime()).toBe(new Date('2026-10-12T08:00:00Z').getTime());
  });

  it('turns a timed form into an API body with instants and a zone', () => {
    const f = { ...emptyForm(new Date('2026-10-07T09:00:00Z'), new Date('2026-10-07T10:00:00Z'), false, 'cal_1'), title: 'A' };
    const b = bodyFromForm(f, 'Europe/Berlin');
    expect(typeof b).not.toBe('string');
    if (typeof b === 'string') return;
    expect(new Date(b.start).getTime()).toBe(new Date('2026-10-07T09:00:00Z').getTime());
    expect(b.zone).toBe('Europe/Berlin');
    expect(b.all_day).toBe(false);
    expect(b.repeat).toEqual({ freq: '' });
  });

  it('uses inclusive all-day end dates in the form and exclusive ones on the wire', () => {
    const f = emptyForm(new Date(2026, 9, 7), new Date(2026, 9, 9), true, 'cal_1');
    expect(f.end).toBe('2026-10-08');
    const b = bodyFromForm({ ...f, title: 'Trip' }, 'UTC');
    if (typeof b === 'string') throw new Error(b);
    expect([b.start, b.end, b.all_day, b.zone]).toEqual(['2026-10-07', '2026-10-09', true, undefined]);
  });

  it('refuses an end before the start and keeps custom rules', () => {
    const f = emptyForm(new Date('2026-10-07T10:00:00Z'), new Date('2026-10-07T09:00:00Z'), false, 'cal_1');
    expect(bodyFromForm(f, 'UTC')).toBe('End must not be before start');
    const custom = formFromEvent({ ...base, repeat: { freq: 'custom' } }, 'all');
    const b = bodyFromForm(custom, 'Europe/Berlin', 'all');
    if (typeof b === 'string') throw new Error(b);
    expect(b.repeat).toEqual({ freq: 'custom' });
    expect(b.scope).toBe('all');
  });

  it('asks for a start and end when either is empty or invalid', () => {
    const timed = { ...emptyForm(new Date('2026-10-07T09:00:00Z'), new Date('2026-10-07T10:00:00Z'), false, 'cal_1'), start: '' };
    expect(bodyFromForm(timed, 'UTC')).toBe('Enter a start and end');
    const allDay = { ...emptyForm(new Date(2026, 9, 7), new Date(2026, 9, 9), true, 'cal_1'), end: '' };
    expect(bodyFromForm(allDay, 'UTC')).toBe('Enter a start and end');
  });

  it('accepts only IANA zones the browser knows', () => {
    expect(validZone('Europe/Berlin')).toBe(true);
    expect(validZone('Eastern Standard Time')).toBe(false);
    expect(validZone('')).toBe(false);
    expect(localDate(new Date(2026, 0, 2))).toBe('2026-01-02');
  });
});
